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
		includeSecondHop := input.Feed == "following" || ranked
		viewer, err := loadViewerContextWithGraph(ctx, h.DB, GetSession(c).UserID, includeSecondHop)
		if errors.Is(err, sql.ErrNoRows) {
			return c.JSON(http.StatusOK, getFeedResponse{Items: []feedItemResponse{}})
		}
		if err != nil {
			return err
		}
		if !viewer.HasPostedToday {
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
		var candidates []PostWithAuthor
		var scores map[string]int64
		if ranked {
			candidates, scores, err = selectRankedFeedWithScore(ctx, h.DB, viewer, input)
		} else {
			conditions, args, queryErr := nonRankedFeedQuery(viewer, following, input, input.BeforePostedAt, input.BeforeID)
			if queryErr != nil {
				return queryErr
			}
			var posts []PostWithAuthor
			posts, err = selectPostsWithAuthor(ctx, h.DB, conditions, args...)
			if err == nil {
				candidates = make([]PostWithAuthor, 0, len(posts))
				for _, post := range posts {
					if viewer.canView(post.Post) && matchesFeed(viewer, following, post.Post, input.Feed) {
						candidates = append(candidates, post)
					}
				}
			}
		}
		if err != nil {
			return err
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

type rankedPostWithScore struct {
	PostWithAuthor
	Score int64 `db:"score"`
}

func selectRankedFeedWithScore(ctx context.Context, db sqlx.QueryerContext, viewer viewerContext, input getFeedRequest) ([]PostWithAuthor, map[string]int64, error) {
	query, args := rankedFeedQuery(viewer, input)
	var rows []rankedPostWithScore
	if err := sqlx.SelectContext(ctx, db, &rows, query, args...); err != nil {
		return nil, nil, err
	}
	posts := make([]PostWithAuthor, len(rows))
	scores := make(map[string]int64, len(rows))
	for i, row := range rows {
		if err := row.Author.setImageKey(); err != nil {
			return nil, nil, err
		}
		posts[i] = row.PostWithAuthor
		scores[row.ID] = row.Score
	}
	return posts, scores, nil
}

func rankedFeedQuery(viewer viewerContext, input getFeedRequest) (string, []any) {
	friendIDs := sortedMapIDs(viewer.Friends)
	fofIDs := sortedMapIDs(viewer.FriendsOfFriends)
	blockedIDs := sortedMapIDs(viewer.Blocked)
	mutualIDs, mutualCounts := sortedMutualCountArrays(viewer.MutualFriends)

	query := `
WITH candidate_posts AS MATERIALIZED (
  SELECT p.id, p.author_id, p.posted_at
  FROM posts p
  JOIN posting_windows w ON w.id = p.posting_window_id
  JOIN users u ON u.id = p.author_id AND u.status = 'active'
  WHERE p.deleted_at IS NULL AND p.author_id <> $1 AND w.ended_at IS NULL
    AND $10::boolean
    AND p.author_id = ANY($2::text[])
    AND NOT (p.author_id = ANY($3::text[]))
    AND NOT (p.author_id = ANY($4::text[]))
    AND p.visibility IN ('public', 'friends_of_friends')
),
candidate_authors AS MATERIALIZED (
  SELECT DISTINCT author_id FROM candidate_posts
),
mutual_counts AS MATERIALIZED (
  SELECT m.user_id, m.mutual_count
  FROM unnest($5::text[], $6::bigint[]) AS m(user_id, mutual_count)
),
author_edges AS (
  SELECT f.user_low_id AS author_id, f.user_high_id AS other_id
  FROM friendships f
  JOIN candidate_authors ca ON ca.author_id = f.user_low_id
  UNION ALL
  SELECT f.user_high_id AS author_id, f.user_low_id AS other_id
  FROM friendships f
  JOIN candidate_authors ca ON ca.author_id = f.user_high_id
),
connections AS (
  SELECT e.author_id, SUM(COALESCE(m.mutual_count, 0)) AS mutual_connections
  FROM author_edges e
  JOIN mutual_counts m ON m.user_id = e.other_id
  GROUP BY e.author_id
),
engagements AS (
  SELECT r.post_id, r.reactor_id AS user_id
  FROM realmojis r
  JOIN candidate_posts cp ON cp.id = r.post_id
  JOIN users u ON u.id = r.reactor_id AND u.status = 'active'
  WHERE r.reactor_id = ANY($3::text[]) AND r.replaced_at IS NULL AND r.deleted_at IS NULL
  UNION
  SELECT c.post_id, c.author_id AS user_id
  FROM comments c
  JOIN candidate_posts cp ON cp.id = c.post_id
  JOIN users u ON u.id = c.author_id AND u.status = 'active'
  WHERE c.author_id = ANY($3::text[]) AND c.deleted_at IS NULL
),
friend_engagements AS (
  SELECT e.post_id, COUNT(*) AS engagement_count
  FROM engagements e
  GROUP BY e.post_id
),
scored_candidates AS (
  SELECT cp.id, cp.author_id, cp.posted_at,
         10 * COALESCE(fe.engagement_count, 0)
           + 3 * COALESCE(author_mutual.mutual_count, 0)
           + LEAST(COALESCE(c.mutual_connections, 0), 30)::bigint AS score
  FROM candidate_posts cp
  LEFT JOIN friend_engagements fe ON fe.post_id = cp.id
  LEFT JOIN connections c ON c.author_id = cp.author_id
  LEFT JOIN mutual_counts author_mutual ON author_mutual.user_id = cp.author_id
),
page AS MATERIALIZED (
  SELECT * FROM scored_candidates
  WHERE ($7::bigint IS NULL OR (score, posted_at, id) < ($7, $8, $9))
  ORDER BY score DESC, posted_at DESC, id DESC
  LIMIT 51
)
SELECT ` + postColumns + `, ` + userColumnsAs("u", "author") + `, page.score
FROM page
JOIN posts p ON p.id = page.id
JOIN posting_windows w ON w.id = p.posting_window_id
JOIN users u ON u.id = p.author_id AND u.status = 'active'
ORDER BY page.score DESC, page.posted_at DESC, page.id DESC
`
	args := []any{
		viewer.ID, fofIDs, friendIDs, blockedIDs, mutualIDs, mutualCounts,
		input.BeforeScore, input.BeforePostedAt, input.BeforeID, viewer.HasPostedToday,
	}
	return query, args
}

func sortedMutualCountArrays(values map[string]int) ([]string, []int64) {
	ids := make([]string, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	counts := make([]int64, len(ids))
	for i, id := range ids {
		counts[i] = int64(values[id])
	}
	return ids, counts
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
