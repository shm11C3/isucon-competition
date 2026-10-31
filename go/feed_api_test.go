package isureal

import (
	"context"
	"fmt"
	"os"
	"reflect"
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
