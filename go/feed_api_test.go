package isureal

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

func TestNonRankedFeedQueryMatchesGoFilter(t *testing.T) {
	dsn := os.Getenv("T0006_TEST_DSN")
	if dsn == "" {
		t.Skip("set T0006_TEST_DSN to run the PostgreSQL feed comparison")
	}
	db, err := sqlx.Connect("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	conn, err := db.Connx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, statement := range []string{
		`CREATE TEMP TABLE users (id text PRIMARY KEY, username text NOT NULL, secret_key_hash text NOT NULL, display_name text NOT NULL, bio text NOT NULL, profile_image_id text, region text NOT NULL, account_type text NOT NULL, status text NOT NULL)`,
		`CREATE TEMP TABLE posting_windows (id text PRIMARY KEY, on_time_deadline timestamptz NOT NULL, ended_at timestamptz)`,
		`CREATE TEMP TABLE posts (id text PRIMARY KEY, author_id text NOT NULL, posting_window_id text NOT NULL, image_id text NOT NULL, caption text NOT NULL, visibility text NOT NULL, location_kind text NOT NULL, latitude numeric, longitude numeric, locality text, posted_at timestamptz NOT NULL, deleted_at timestamptz)`,
	} {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	const secretHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, user := range []struct{ id, status string }{
		{"viewer", "active"}, {"friend", "active"}, {"fof", "active"}, {"followed", "active"},
		{"blocked", "active"}, {"stranger", "active"}, {"inactive", "deleted"},
	} {
		if _, err := conn.ExecContext(ctx, `INSERT INTO users VALUES ($1,$1,$2,$1,'',NULL,'america','regular',$3)`, user.id, secretHash, user.status); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO posting_windows VALUES ('active', $1, NULL), ('ended', $1, $1)`, time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	baseTime := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 240; i++ {
		author, visibility := "stranger", "public"
		switch {
		case i < 62:
			author = "blocked"
		case i < 127:
			author = "friend"
			visibility = []string{"public", "friends", "friends_of_friends"}[i%3]
		case i < 192:
			author = "followed"
			if i%3 != 0 {
				visibility = []string{"friends", "friends_of_friends"}[i%2]
			}
		case i < 207:
			author, visibility = "fof", "friends_of_friends"
		case i < 222:
			author, visibility = "inactive", "public"
		case i < 232:
			author, visibility = "viewer", "public"
		case i < 237:
			author, visibility = "stranger", "unknown"
		}
		window := "active"
		var deleted any
		if i%17 == 0 {
			window = "ended"
		}
		if i%19 == 0 {
			deleted = baseTime
		}
		postedAt := baseTime.Add(-time.Duration(i/2) * time.Minute)
		id := fmt.Sprintf("post-%03d", i)
		if _, err := conn.ExecContext(ctx, `INSERT INTO posts VALUES ($1,$2,$3,$4,'',$5,'none',NULL,NULL,NULL,$6,$7)`, id, author, window, "image-"+id, visibility, postedAt, deleted); err != nil {
			t.Fatal(err)
		}
	}
	viewer := viewerContext{
		User: User{ID: "viewer"}, HasPostedToday: true,
		Friends:          map[string]bool{"friend": true},
		FriendsOfFriends: map[string]bool{"fof": true},
		Blocked:          map[string]bool{"blocked": true},
	}
	following := map[string]bool{"friend": true, "followed": true, "fof": true, "blocked": true}
	feedNames := []string{"friends", "public", "following"}
	for _, feed := range feedNames {
		for _, cursor := range []struct {
			postedAt *time.Time
			id       string
		}{{nil, ""}, {ptrTime(baseTime.Add(-20 * time.Minute)), "post-040"}, {ptrTime(baseTime.Add(-60 * time.Minute)), "post-120"}} {
			input := getFeedRequest{Feed: feed, BeforePostedAt: cursor.postedAt, BeforeID: cursor.id}
			got := runNonRankedFeedQuery(t, ctx, conn, viewer, following, input)
			want := runOldFeedFilter(t, ctx, conn, viewer, following, input)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("feed %s cursor (%v,%q): SQL page %+v, Go page %+v", feed, cursor.postedAt, cursor.id, got, want)
			}
		}
	}
	viewer.HasPostedToday = false
	input := getFeedRequest{Feed: "public"}
	if got, want := runNonRankedFeedQuery(t, ctx, conn, viewer, following, input), runOldFeedFilter(t, ctx, conn, viewer, following, input); !reflect.DeepEqual(got, want) || len(got.IDs) != 0 || got.HasMore {
		t.Errorf("no-post-today: SQL page %+v, Go page %+v", got, want)
	}
}

func TestNonRankedFeedQueryTextStableForArrayLengths(t *testing.T) {
	emptyViewer := viewerContext{User: User{ID: "viewer"}, Friends: map[string]bool{}, FriendsOfFriends: map[string]bool{}, Blocked: map[string]bool{}}
	populatedViewer := viewerContext{User: User{ID: "viewer"}, Friends: map[string]bool{"friend-1": true}, FriendsOfFriends: map[string]bool{"fof": true}, Blocked: map[string]bool{"blocked": true}}
	inputTime := ptrTime(time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC))
	empty := map[string]bool{}
	populated := map[string]bool{"blocked": true, "friend-1": true, "friend-2": true, "fof": true}
	for _, feed := range []string{"public", "friends", "following"} {
		input := getFeedRequest{Feed: feed, BeforePostedAt: inputTime, BeforeID: "post"}
		queryEmpty, argsEmpty, err := nonRankedFeedQuery(emptyViewer, empty, input, inputTime, "post")
		if err != nil {
			t.Fatal(err)
		}
		queryPopulated, argsPopulated, err := nonRankedFeedQuery(populatedViewer, populated, input, inputTime, "post")
		if err != nil {
			t.Fatal(err)
		}
		if queryEmpty != queryPopulated || len(argsEmpty) != len(argsPopulated) {
			t.Errorf("%s query shape changed with ID cardinality", feed)
		}
		for _, args := range [][]any{argsEmpty, argsPopulated} {
			for _, index := range arrayArgIndexes(feed) {
				ids, ok := args[index].([]string)
				if !ok || ids == nil {
					t.Errorf("%s arg %d is not a non-nil []string: %#v", feed, index, args[index])
				}
			}
		}
	}
}

func TestRankedFeedQueryTextStableForArrayLengths(t *testing.T) {
	empty := viewerContext{User: User{ID: "viewer"}, Friends: map[string]bool{}, FriendsOfFriends: map[string]bool{}, Blocked: map[string]bool{}, MutualFriends: map[string]int{}}
	populated := viewerContext{
		User:             User{ID: "viewer"},
		Friends:          map[string]bool{"friend-a": true, "friend-b": true},
		FriendsOfFriends: map[string]bool{"fof-a": true, "fof-b": true},
		Blocked:          map[string]bool{"blocked": true},
		MutualFriends:    map[string]int{"friend-a": 2, "fof-a": 3},
	}
	input := getFeedRequest{Feed: "friends-of-friends"}
	queryEmpty, argsEmpty := rankedFeedQuery(empty, input)
	queryPopulated, argsPopulated := rankedFeedQuery(populated, input)
	if queryEmpty != queryPopulated || len(argsEmpty) != len(argsPopulated) {
		t.Fatal("ranked query shape changed with snapshot array cardinality")
	}
	for _, args := range [][]any{argsEmpty, argsPopulated} {
		for _, index := range []int{1, 2, 3, 4} {
			ids, ok := args[index].([]string)
			if !ok || ids == nil {
				t.Errorf("arg %d is not a non-nil []string: %#v", index, args[index])
			}
		}
		counts, ok := args[5].([]int64)
		if !ok || counts == nil {
			t.Errorf("mutual-count arg is not a non-nil []int64: %#v", args[5])
		}
	}
}

func arrayArgIndexes(feed string) []int {
	switch feed {
	case "public":
		return []int{5}
	case "friends":
		return []int{5, 6}
	case "following":
		return []int{5, 6, 7, 8}
	default:
		return nil
	}
}

func TestRecommendationScoresMatchesIN(t *testing.T) {
	dsn := os.Getenv("T0009_TEST_DSN")
	if dsn == "" {
		t.Skip("set T0009_TEST_DSN to run the PostgreSQL recommendation comparison")
	}
	db, err := sqlx.Connect("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	conn, err := db.Connx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, statement := range []string{
		`CREATE TEMP TABLE users (id text PRIMARY KEY, status text NOT NULL)`,
		`CREATE TEMP TABLE friendships (user_low_id text NOT NULL, user_high_id text NOT NULL)`,
		`CREATE TEMP TABLE realmojis (post_id text NOT NULL, reactor_id text NOT NULL, replaced_at timestamptz, deleted_at timestamptz)`,
		`CREATE TEMP TABLE comments (post_id text NOT NULL, author_id text NOT NULL, deleted_at timestamptz)`,
	} {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, user := range []struct{ id, status string }{
		{"friend-a", "active"}, {"friend-b", "active"}, {"inactive-friend", "deleted"}, {"stranger", "active"},
	} {
		if _, err := conn.ExecContext(ctx, `INSERT INTO users VALUES ($1,$2)`, user.id, user.status); err != nil {
			t.Fatal(err)
		}
	}
	for _, edge := range [][2]string{
		{"friend-a", "author-a"}, {"author-a", "common"}, {"author-b", "common"},
		{"author-b", "friend-b"}, {"author-c", "common"}, {"unrelated-a", "unrelated-b"},
	} {
		if _, err := conn.ExecContext(ctx, `INSERT INTO friendships VALUES ($1,$2)`, edge[0], edge[1]); err != nil {
			t.Fatal(err)
		}
	}
	for _, reaction := range []struct {
		post, user        string
		replaced, deleted any
	}{
		{"post-a", "friend-a", nil, nil}, {"post-a", "friend-b", nil, nil},
		{"post-a", "inactive-friend", nil, nil}, {"post-a", "friend-a", time.Now(), nil},
		{"post-a", "friend-b", nil, time.Now()}, {"post-b", "friend-a", nil, nil},
	} {
		if _, err := conn.ExecContext(ctx, `INSERT INTO realmojis VALUES ($1,$2,$3,$4)`, reaction.post, reaction.user, reaction.replaced, reaction.deleted); err != nil {
			t.Fatal(err)
		}
	}
	for _, comment := range []struct {
		post, user string
		deleted    any
	}{
		{"post-a", "friend-a", nil}, {"post-a", "inactive-friend", nil},
		{"post-b", "friend-b", nil}, {"post-b", "friend-a", time.Now()}, {"post-c", "stranger", nil},
	} {
		if _, err := conn.ExecContext(ctx, `INSERT INTO comments VALUES ($1,$2,$3)`, comment.post, comment.user, comment.deleted); err != nil {
			t.Fatal(err)
		}
	}
	viewer := viewerContext{
		Friends:       map[string]bool{"friend-a": true, "friend-b": true},
		MutualFriends: map[string]int{"friend-a": 2, "friend-b": 3, "common": 4},
	}
	posts := []PostWithAuthor{
		{Post: Post{ID: "post-a", AuthorID: "author-a"}},
		{Post: Post{ID: "post-b", AuthorID: "author-a"}},
		{Post: Post{ID: "post-c", AuthorID: "author-b"}},
		{Post: Post{ID: "post-a", AuthorID: "author-a"}},
	}
	want, err := recommendationScoresWithIN(ctx, conn, viewer, posts)
	if err != nil {
		t.Fatal(err)
	}
	got, err := recommendationScores(ctx, conn, viewer, posts)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ANY scores %v differ from IN scores %v", got, want)
	}
}

func recommendationScoresWithIN(ctx context.Context, db sqlx.QueryerContext, viewer viewerContext, posts []PostWithAuthor) (map[string]int64, error) {
	authorIDs, postIDs := make([]string, 0, len(posts)), make([]string, 0, len(posts))
	for _, post := range posts {
		authorIDs = append(authorIDs, post.AuthorID)
		postIDs = append(postIDs, post.ID)
	}
	query, args, err := sqlx.In(`SELECT user_low_id, user_high_id FROM friendships WHERE user_low_id IN (?) OR user_high_id IN (?)`, authorIDs, authorIDs)
	if err != nil {
		return nil, err
	}
	var friendships []struct {
		UserLowID  string `db:"user_low_id"`
		UserHighID string `db:"user_high_id"`
	}
	if err := sqlx.SelectContext(ctx, db, &friendships, sqlx.Rebind(sqlx.DOLLAR, query), args...); err != nil {
		return nil, err
	}
	connections := map[string]int64{}
	for _, friendship := range friendships {
		connections[friendship.UserLowID] += int64(viewer.MutualFriends[friendship.UserHighID])
		connections[friendship.UserHighID] += int64(viewer.MutualFriends[friendship.UserLowID])
	}
	query, args, err = sqlx.In(`
  SELECT r.post_id, r.reactor_id AS user_id FROM realmojis r
  JOIN users u ON u.id = r.reactor_id AND u.status = 'active'
  WHERE r.post_id IN (?) AND r.replaced_at IS NULL AND r.deleted_at IS NULL
  UNION
  SELECT c.post_id, c.author_id AS user_id FROM comments c
  JOIN users u ON u.id = c.author_id AND u.status = 'active'
  WHERE c.post_id IN (?) AND c.deleted_at IS NULL
 `, postIDs, postIDs)
	if err != nil {
		return nil, err
	}
	var engagements []struct {
		PostID string `db:"post_id"`
		UserID string `db:"user_id"`
	}
	if err := sqlx.SelectContext(ctx, db, &engagements, sqlx.Rebind(sqlx.DOLLAR, query), args...); err != nil {
		return nil, err
	}
	friendEngagements := map[string]int64{}
	for _, engagement := range engagements {
		if viewer.Friends[engagement.UserID] {
			friendEngagements[engagement.PostID]++
		}
	}
	scores := map[string]int64{}
	for _, post := range posts {
		scores[post.ID] = 10*friendEngagements[post.ID] + 3*int64(viewer.MutualFriends[post.AuthorID]) + min(connections[post.AuthorID], 30)
	}
	return scores, nil
}

func TestRankedFeedQueryMatchesGoOracle(t *testing.T) {
	dsn := os.Getenv("T0012_TEST_DSN")
	if dsn == "" {
		t.Skip("set T0012_TEST_DSN to run the PostgreSQL ranked feed comparison")
	}
	db, err := sqlx.Connect("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	conn, err := db.Connx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, statement := range []string{
		`CREATE TEMP TABLE users (id text PRIMARY KEY, username text NOT NULL, secret_key_hash text NOT NULL, display_name text NOT NULL, bio text NOT NULL, profile_image_id text, region text NOT NULL, account_type text NOT NULL, status text NOT NULL)`,
		`CREATE TEMP TABLE posting_windows (id text PRIMARY KEY, on_time_deadline timestamptz NOT NULL, ended_at timestamptz)`,
		`CREATE TEMP TABLE posts (id text PRIMARY KEY, author_id text NOT NULL, posting_window_id text NOT NULL, image_id text NOT NULL, caption text NOT NULL, visibility text NOT NULL, location_kind text NOT NULL, latitude numeric, longitude numeric, locality text, posted_at timestamptz NOT NULL, deleted_at timestamptz)`,
		`CREATE TEMP TABLE friendships (user_low_id text NOT NULL, user_high_id text NOT NULL)`,
		`CREATE TEMP TABLE realmojis (post_id text NOT NULL, reactor_id text NOT NULL, replaced_at timestamptz, deleted_at timestamptz)`,
		`CREATE TEMP TABLE comments (post_id text NOT NULL, author_id text NOT NULL, deleted_at timestamptz)`,
	} {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	const secretHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, user := range []struct{ id, status string }{
		{"viewer", "active"}, {"friend-a", "active"}, {"friend-b", "active"},
		{"fof-a", "active"}, {"fof-b", "active"}, {"blocked-a", "active"},
		{"blocked-b", "active"}, {"direct", "active"}, {"unrelated", "active"},
		{"inactive", "deleted"}, {"stranger", "active"}, {"nonfriend-engager", "active"},
	} {
		if _, err := conn.ExecContext(ctx, `INSERT INTO users VALUES ($1,$1,$2,$1,'',NULL,'america','regular',$3)`, user.id, secretHash, user.status); err != nil {
			t.Fatal(err)
		}
	}
	baseTime := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	if _, err := conn.ExecContext(ctx, `INSERT INTO posting_windows VALUES ('active',$1,NULL),('ended',$1,$1)`, baseTime); err != nil {
		t.Fatal(err)
	}
	insertPost := func(id, author, visibility, window string, postedAt time.Time, deleted any) {
		t.Helper()
		if _, err := conn.ExecContext(ctx, `INSERT INTO posts VALUES ($1,$2,$3,$4,'',$5,'none',NULL,NULL,NULL,$6,$7)`, id, author, window, "image-"+id, visibility, postedAt, deleted); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 120; i++ {
		author, visibility := "fof-a", "public"
		if i%2 == 0 {
			author = "fof-b"
		}
		if i%3 == 0 {
			visibility = "friends_of_friends"
		}
		window := "active"
		var deleted any
		if i%40 == 0 {
			window = "ended"
		}
		if i%37 == 0 {
			deleted = baseTime
		}
		insertPost(fmt.Sprintf("rank-%03d", i), author, visibility, window, baseTime.Add(-time.Duration(i/4)*time.Minute), deleted)
	}
	insertPost("public-unrelated", "unrelated", "public", "active", baseTime.Add(time.Hour), nil)
	insertPost("public-direct-friend", "direct", "public", "active", baseTime.Add(time.Hour), nil)
	insertPost("fof-direct-private", "direct", "friends_of_friends", "active", baseTime.Add(time.Hour), nil)
	insertPost("fof-friends-private", "fof-a", "friends", "active", baseTime.Add(time.Hour), nil)
	insertPost("fof-invalid-visibility", "fof-a", "unknown", "active", baseTime.Add(time.Hour), nil)
	insertPost("blocked-outgoing", "blocked-a", "public", "active", baseTime.Add(time.Hour), nil)
	insertPost("blocked-incoming", "blocked-b", "friends_of_friends", "active", baseTime.Add(time.Hour), nil)
	insertPost("inactive-author", "inactive", "public", "active", baseTime.Add(time.Hour), nil)
	insertPost("ended-author", "fof-a", "public", "ended", baseTime.Add(time.Hour), nil)
	insertPost("deleted-author", "fof-a", "public", "active", baseTime.Add(time.Hour), baseTime)
	insertPost("own-author", "viewer", "public", "active", baseTime.Add(time.Hour), nil)
	for _, edge := range [][2]string{
		{"fof-a", "friend-a"}, {"friend-b", "fof-a"}, {"fof-a", "fof-b"},
		{"fof-b", "common"}, {"fof-b", "fof-b"}, {"unrelated-a", "unrelated-b"},
	} {
		if _, err := conn.ExecContext(ctx, `INSERT INTO friendships VALUES ($1,$2)`, edge[0], edge[1]); err != nil {
			t.Fatal(err)
		}
	}
	for _, engagement := range []struct {
		post, user        string
		replaced, deleted any
	}{
		{"rank-001", "friend-a", nil, nil}, {"rank-001", "friend-b", nil, nil},
		{"rank-001", "friend-a", nil, nil}, {"rank-001", "nonfriend-engager", nil, nil},
		{"rank-001", "inactive", nil, nil}, {"rank-001", "friend-a", baseTime, nil},
		{"rank-001", "friend-b", nil, baseTime}, {"rank-002", "friend-a", nil, nil},
	} {
		if _, err := conn.ExecContext(ctx, `INSERT INTO realmojis VALUES ($1,$2,$3,$4)`, engagement.post, engagement.user, engagement.replaced, engagement.deleted); err != nil {
			t.Fatal(err)
		}
	}
	for _, comment := range []struct {
		post, user string
		deleted    any
	}{
		{"rank-001", "friend-a", nil}, {"rank-001", "nonfriend-engager", nil},
		{"rank-001", "inactive", nil}, {"rank-002", "friend-a", nil},
		{"rank-002", "friend-b", baseTime},
	} {
		if _, err := conn.ExecContext(ctx, `INSERT INTO comments VALUES ($1,$2,$3)`, comment.post, comment.user, comment.deleted); err != nil {
			t.Fatal(err)
		}
	}
	viewer := viewerContext{
		User:             User{ID: "viewer"},
		HasPostedToday:   true,
		Friends:          map[string]bool{"friend-a": true, "friend-b": true, "direct": true},
		FriendsOfFriends: map[string]bool{"fof-a": true, "fof-b": true, "direct": true, "blocked-a": true, "blocked-b": true, "inactive": true},
		Blocked:          map[string]bool{"blocked-a": true, "blocked-b": true},
		MutualFriends:    map[string]int{"fof-a": 2, "fof-b": 5, "friend-a": 4, "friend-b": 3, "common": 37},
	}
	all := runOldRankedFeed(t, ctx, conn, viewer, getFeedRequest{Feed: "friends-of-friends"})
	if !all.HasMore {
		t.Fatalf("fixture does not contain more than 50 eligible ranked posts")
	}
	basePosts, baseScores := oldRankedCandidates(t, ctx, conn, viewer)
	if baseScores["rank-004"] != 45 {
		t.Fatalf("expected connection cap score 45 for rank-004, got %d", baseScores["rank-004"])
	}
	cursorRequest := func(post PostWithAuthor) getFeedRequest {
		score, postedAt := baseScores[post.ID], post.PostedAt
		return getFeedRequest{Feed: "friends-of-friends", BeforeScore: &score, BeforePostedAt: &postedAt, BeforeID: post.ID}
	}
	inputs := []getFeedRequest{{Feed: "friends-of-friends"}, cursorRequest(basePosts[31]), cursorRequest(basePosts[len(basePosts)-3])}
	tieCursorFound := false
	for i := 1; i < len(basePosts); i++ {
		if baseScores[basePosts[i-1].ID] == baseScores[basePosts[i].ID] && basePosts[i-1].PostedAt.Equal(basePosts[i].PostedAt) {
			inputs = append(inputs, cursorRequest(basePosts[i]))
			tieCursorFound = true
			break
		}
	}
	if !tieCursorFound {
		t.Fatal("fixture did not produce an equal-score, equal-time cursor tie")
	}
	for _, input := range inputs {
		got := runNewRankedFeed(t, ctx, conn, viewer, input)
		want := runOldRankedFeed(t, ctx, conn, viewer, input)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("cursor(score=%v,time=%v,id=%q): SQL page %+v, Go page %+v", input.BeforeScore, input.BeforePostedAt, input.BeforeID, got, want)
		}
	}
	viewer.HasPostedToday = false
	input := getFeedRequest{Feed: "friends-of-friends"}
	if got, want := runNewRankedFeed(t, ctx, conn, viewer, input), runOldRankedFeed(t, ctx, conn, viewer, input); !reflect.DeepEqual(got, want) || len(got.IDs) != 0 {
		t.Errorf("no-post-today: SQL page %+v, Go page %+v", got, want)
	}
	viewer.HasPostedToday = true
	query, args := rankedFeedQuery(viewer, getFeedRequest{Feed: "friends-of-friends"})
	var plan []string
	if err := sqlx.SelectContext(ctx, conn, &plan, "EXPLAIN (ANALYZE, BUFFERS, COSTS OFF, SUMMARY OFF) "+query, args...); err != nil {
		t.Fatal(err)
	}
	t.Log("ranked feed local plan:\n" + strings.Join(plan, "\n"))
}

type rankedTestPage struct {
	IDs     []string
	Scores  []int64
	HasMore bool
}

func runNewRankedFeed(t *testing.T, ctx context.Context, db sqlx.QueryerContext, viewer viewerContext, input getFeedRequest) rankedTestPage {
	t.Helper()
	posts, scores, err := selectRankedFeedWithScore(ctx, db, viewer, input)
	if err != nil {
		t.Fatal(err)
	}
	return rankedPage(posts, scores, input)
}

func runOldRankedFeed(t *testing.T, ctx context.Context, db sqlx.QueryerContext, viewer viewerContext, input getFeedRequest) rankedTestPage {
	t.Helper()
	posts, scores := oldRankedCandidates(t, ctx, db, viewer)
	return rankedPage(posts, scores, input)
}

func oldRankedCandidates(t *testing.T, ctx context.Context, db sqlx.QueryerContext, viewer viewerContext) ([]PostWithAuthor, map[string]int64) {
	t.Helper()
	posts, err := selectPostsWithAuthor(ctx, db, `AND p.author_id <> $1 AND w.ended_at IS NULL ORDER BY p.posted_at DESC, p.id DESC`, viewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	candidates := make([]PostWithAuthor, 0, len(posts))
	for _, post := range posts {
		if viewer.canView(post.Post) && matchesFeed(viewer, nil, post.Post, "friends-of-friends") {
			candidates = append(candidates, post)
		}
	}
	if len(candidates) == 0 {
		return candidates, map[string]int64{}
	}
	scores, err := recommendationScoresWithIN(ctx, db, viewer, candidates)
	if err != nil {
		t.Fatal(err)
	}
	sort.SliceStable(candidates, func(i, j int) bool { return scores[candidates[i].ID] > scores[candidates[j].ID] })
	return candidates, scores
}

func rankedPage(posts []PostWithAuthor, scores map[string]int64, input getFeedRequest) rankedTestPage {
	page := rankedTestPage{IDs: make([]string, 0, 51), Scores: make([]int64, 0, 51)}
	for _, post := range posts {
		score := scores[post.ID]
		if input.BeforeScore != nil && !rankedBefore(score, post.Post, *input.BeforeScore, *input.BeforePostedAt, input.BeforeID) {
			continue
		}
		page.IDs = append(page.IDs, post.ID)
		page.Scores = append(page.Scores, score)
		if len(page.IDs) == 51 {
			break
		}
	}
	page.HasMore = len(page.IDs) > 50
	if page.HasMore {
		page.IDs = page.IDs[:50]
		page.Scores = page.Scores[:50]
	}
	return page
}

type testFeedPage struct {
	IDs     []string
	HasMore bool
}

func pageFromIDs(ids []string) testFeedPage {
	hasMore := len(ids) > 50
	if hasMore {
		ids = ids[:50]
	}
	return testFeedPage{IDs: ids, HasMore: hasMore}
}

func runNonRankedFeedQuery(t *testing.T, ctx context.Context, db sqlx.QueryerContext, viewer viewerContext, following map[string]bool, input getFeedRequest) testFeedPage {
	t.Helper()
	conditions, args, err := nonRankedFeedQuery(viewer, following, input, input.BeforePostedAt, input.BeforeID)
	if err != nil {
		t.Fatal(err)
	}
	posts, err := selectPostsWithAuthor(ctx, db, conditions, args...)
	if err != nil {
		t.Fatal(err)
	}
	return pageFromIDs(postIDs(posts))
}

func runOldFeedFilter(t *testing.T, ctx context.Context, db sqlx.QueryerContext, viewer viewerContext, following map[string]bool, input getFeedRequest) testFeedPage {
	t.Helper()
	var posts []PostWithAuthor
	if err := sqlx.SelectContext(ctx, db, &posts, `
  SELECT `+postColumns+`, `+userColumnsAs("u", "author")+`
  FROM posts p JOIN posting_windows w ON w.id = p.posting_window_id
  JOIN users u ON u.id = p.author_id AND u.status = 'active'
  WHERE p.deleted_at IS NULL AND p.author_id <> $1 AND w.ended_at IS NULL
    AND ($2::timestamptz IS NULL OR (p.posted_at,p.id) < ($2,$3))
  ORDER BY p.posted_at DESC, p.id DESC
 `, viewer.ID, input.BeforePostedAt, input.BeforeID); err != nil {
		t.Fatal(err)
	}
	for i := range posts {
		if err := posts[i].Author.setImageKey(); err != nil {
			t.Fatal(err)
		}
	}
	ids := make([]string, 0, 51)
	for _, post := range posts {
		if viewer.canView(post.Post) && matchesFeed(viewer, following, post.Post, input.Feed) {
			ids = append(ids, post.ID)
			if len(ids) == 51 {
				break
			}
		}
	}
	return pageFromIDs(ids)
}

func postIDs(posts []PostWithAuthor) []string {
	ids := make([]string, len(posts))
	for i, post := range posts {
		ids[i] = post.ID
	}
	return ids
}

func ptrTime(value time.Time) *time.Time { return &value }
