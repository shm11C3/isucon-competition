package isureal

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

type getFeedRequest struct {
	Feed           string     `param:"feed"`
	BeforeScore    *int64     `query:"beforeScore"`
	BeforePostedAt *time.Time `query:"beforePostedAt"`
	BeforeID       string     `query:"beforeId"`
}

type feedItemResponse struct {
	createPostResponse
	Score *int64 `json:"score,omitempty"`
}

type getFeedResponse struct {
	Items   []feedItemResponse `json:"items"`
	HasMore bool               `json:"hasMore"`
}

func getFeedHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input getFeedRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		switch input.Feed {
		case "friends", "friends-of-friends", "public", "following":
		default:
			return NewAPIError(http.StatusBadRequest, "invalid_post")
		}
		if (input.BeforePostedAt == nil) != (input.BeforeID == "") {
			return NewAPIError(http.StatusBadRequest, "invalid_pagination")
		}
		ranked := input.Feed == "friends-of-friends"
		if ranked && (input.BeforeScore == nil) != (input.BeforeID == "") {
			return NewAPIError(http.StatusBadRequest, "invalid_pagination")
		}
		ctx := c.Request().Context()
		viewer, err := loadViewerContext(ctx, h.DB, GetSession(c).UserID)
		if errors.Is(err, sql.ErrNoRows) {
			return c.JSON(http.StatusOK, getFeedResponse{Items: []feedItemResponse{}})
		}
		if err != nil {
			return err
		}
		if !ranked && !viewer.HasPostedToday {
			return c.JSON(http.StatusOK, getFeedResponse{Items: []feedItemResponse{}})
		}
		following := map[string]bool{}
		if input.Feed == "following" {
			var followeeIDs []string
			if err := h.DB.SelectContext(ctx, &followeeIDs, `SELECT followee_id FROM follows WHERE follower_id = $1`, viewer.ID); err != nil {
				return err
			}
			for _, id := range followeeIDs {
				following[id] = true
			}
		}
		beforePostedAt, beforeID := input.BeforePostedAt, input.BeforeID
		if ranked {
			beforePostedAt, beforeID = nil, ""
		}
		var posts []PostWithAuthor
		if ranked {
			posts, err = selectPostsWithAuthor(ctx, h.DB, `
   AND p.author_id <> $1 AND w.ended_at IS NULL
   AND ($2::timestamptz IS NULL OR (p.posted_at, p.id) < ($2, $3))
   ORDER BY p.posted_at DESC, p.id DESC
  `, viewer.ID, beforePostedAt, beforeID)
		} else {
			conditions, args, queryErr := nonRankedFeedQuery(viewer, following, input, beforePostedAt, beforeID)
			if queryErr != nil {
				return queryErr
			}
			posts, err = selectPostsWithAuthor(ctx, h.DB, conditions, args...)
		}
		if err != nil {
			return err
		}
		candidates := make([]PostWithAuthor, 0)
		for _, post := range posts {
			if viewer.canView(post.Post) && matchesFeed(viewer, following, post.Post, input.Feed) {
				candidates = append(candidates, post)
			}
		}
		var scores map[string]int64
		if ranked {
			if scores, err = recommendationScores(ctx, h.DB, viewer, candidates); err != nil {
				return err
			}
			sort.SliceStable(candidates, func(i, j int) bool {
				return scores[candidates[i].ID] > scores[candidates[j].ID]
			})
		}
		items := make([]feedItemResponse, 0)
		for _, post := range candidates {
			score := scores[post.ID]
			if ranked && input.BeforeScore != nil && !rankedBefore(score, post.Post, *input.BeforeScore, *input.BeforePostedAt, input.BeforeID) {
				continue
			}
			item := feedItemResponse{createPostResponse: newPostResponse(post.Post, post.Author)}
			if ranked {
				item.Score = &score
			}
			items = append(items, item)
			if len(items) == 51 {
				break
			}
		}
		hasMore := len(items) > 50
		if hasMore {
			items = items[:50]
		}
		return c.JSON(http.StatusOK, getFeedResponse{Items: items, HasMore: hasMore})
	}
}

func nonRankedFeedQuery(viewer viewerContext, following map[string]bool, input getFeedRequest, beforePostedAt *time.Time, beforeID string) (string, []any, error) {
	common := `
   AND p.author_id <> $1 AND w.ended_at IS NULL
   AND ($2::timestamptz IS NULL OR (p.posted_at, p.id) < ($3, $4))
   AND $5
   AND NOT (p.author_id = ANY($6::text[]))
  `
	args := []any{viewer.ID, beforePostedAt, beforePostedAt, beforeID, viewer.HasPostedToday, sortedMapIDs(viewer.Blocked)}

	switch input.Feed {
	case "public":
		return common + ` AND p.visibility = 'public' ORDER BY p.posted_at DESC, p.id DESC LIMIT 51`, args, nil
	case "friends":
		friendIDs := sortedMapIDs(viewer.Friends)
		args = append(args, friendIDs)
		return common + ` AND p.author_id = ANY($7::text[]) AND p.visibility IN ('public', 'friends', 'friends_of_friends') ORDER BY p.posted_at DESC, p.id DESC LIMIT 51`, args, nil
	case "following":
		followeeIDs := sortedMapIDs(following)
		friendIDs := sortedMapIDs(viewer.Friends)
		fofIDs := sortedMapIDs(unionBoolMaps(viewer.Friends, viewer.FriendsOfFriends))
		args = append(args, followeeIDs, friendIDs, fofIDs)
		return common + ` AND p.author_id = ANY($7::text[])
   AND (p.visibility = 'public'
        OR (p.visibility = 'friends' AND p.author_id = ANY($8::text[]))
        OR (p.visibility = 'friends_of_friends' AND p.author_id = ANY($9::text[])))
   ORDER BY p.posted_at DESC, p.id DESC LIMIT 51`, args, nil
	default:
		return common + ` AND FALSE ORDER BY p.posted_at DESC, p.id DESC LIMIT 51`, args, nil
	}
}

func sortedMapIDs(values map[string]bool) []string {
	ids := make([]string, 0, len(values))
	for id, value := range values {
		if value {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func unionBoolMaps(first, second map[string]bool) map[string]bool {
	union := make(map[string]bool, len(first)+len(second))
	for id, value := range first {
		if value {
			union[id] = true
		}
	}
	for id, value := range second {
		if value {
			union[id] = true
		}
	}
	return union
}

func rankedBefore(score int64, post Post, beforeScore int64, beforePostedAt time.Time, beforeID string) bool {
	if score != beforeScore {
		return score < beforeScore
	}
	if !post.PostedAt.Equal(beforePostedAt) {
		return post.PostedAt.Before(beforePostedAt)
	}
	return post.ID < beforeID
}

func recommendationScores(ctx context.Context, db sqlx.QueryerContext, viewer viewerContext, posts []PostWithAuthor) (map[string]int64, error) {
	scores := make(map[string]int64, len(posts))
	if len(posts) == 0 {
		return scores, nil
	}
	authorIDs := make([]string, 0, len(posts))
	postIDs := make([]string, 0, len(posts))
	for _, post := range posts {
		authorIDs = append(authorIDs, post.AuthorID)
		postIDs = append(postIDs, post.ID)
	}

	var friendships []struct {
		UserLowID  string `db:"user_low_id"`
		UserHighID string `db:"user_high_id"`
	}
	if err := sqlx.SelectContext(ctx, db, &friendships, `SELECT user_low_id, user_high_id FROM friendships WHERE user_low_id = ANY($1::text[]) OR user_high_id = ANY($1::text[])`, authorIDs); err != nil {
		return nil, err
	}
	connections := map[string]int64{}
	for _, friendship := range friendships {
		connections[friendship.UserLowID] += int64(viewer.MutualFriends[friendship.UserHighID])
		connections[friendship.UserHighID] += int64(viewer.MutualFriends[friendship.UserLowID])
	}

	const engagementsQuery = `
  SELECT r.post_id, r.reactor_id AS user_id FROM realmojis r
  JOIN users u ON u.id = r.reactor_id AND u.status = 'active'
  WHERE r.post_id = ANY($1::text[]) AND r.replaced_at IS NULL AND r.deleted_at IS NULL
  UNION
  SELECT c.post_id, c.author_id AS user_id FROM comments c
  JOIN users u ON u.id = c.author_id AND u.status = 'active'
  WHERE c.post_id = ANY($1::text[]) AND c.deleted_at IS NULL
 `
	var engagements []struct {
		PostID string `db:"post_id"`
		UserID string `db:"user_id"`
	}
	if err := sqlx.SelectContext(ctx, db, &engagements, engagementsQuery, postIDs); err != nil {
		return nil, err
	}
	friendEngagements := map[string]int64{}
	for _, engagement := range engagements {
		if viewer.Friends[engagement.UserID] {
			friendEngagements[engagement.PostID]++
		}
	}

	for _, post := range posts {
		scores[post.ID] = 10*friendEngagements[post.ID] + 3*int64(viewer.MutualFriends[post.AuthorID]) + min(connections[post.AuthorID], 30)
	}
	return scores, nil
}

func matchesFeed(viewer viewerContext, following map[string]bool, post Post, feed string) bool {
	switch feed {
	case "public":
		return post.Visibility == "public"
	case "following":
		return following[post.AuthorID]
	case "friends":
		return viewer.Friends[post.AuthorID]
	case "friends-of-friends":
		return !viewer.Friends[post.AuthorID] && viewer.FriendsOfFriends[post.AuthorID]
	default:
		return false
	}
}
