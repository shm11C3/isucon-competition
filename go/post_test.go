package isureal

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/jmoiron/sqlx"
)

// TestViewerContextCombinedQuery compares the optimized loader against the
// former query sequence using a disposable PostgreSQL database.
func TestViewerContextCombinedQuery(t *testing.T) {
	dsn := os.Getenv("T0003_TEST_DSN")
	if dsn == "" {
		t.Skip("set T0003_TEST_DSN to run the PostgreSQL 16 comparison")
	}
	db, err := sqlx.Connect("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	for _, statement := range []string{
		`DROP TABLE IF EXISTS posts, posting_windows, block_relations, friendships, users CASCADE`,
		`CREATE TABLE users (id varchar(36) PRIMARY KEY, username varchar(64) NOT NULL, secret_key_hash text NOT NULL, display_name text NOT NULL, bio text NOT NULL DEFAULT '', profile_image_id varchar(36), region text NOT NULL, account_type text NOT NULL DEFAULT 'regular', status text NOT NULL DEFAULT 'active')`,
		`CREATE TABLE friendships (id varchar(36) PRIMARY KEY, user_low_id varchar(36) NOT NULL, user_high_id varchar(36) NOT NULL, UNIQUE(user_low_id,user_high_id))`,
		`CREATE TABLE block_relations (id varchar(36) PRIMARY KEY, blocker_id varchar(36) NOT NULL, blocked_id varchar(36) NOT NULL)`,
		`CREATE TABLE posting_windows (id varchar(36) PRIMARY KEY, region text NOT NULL, local_date date NOT NULL, on_time_deadline timestamptz NOT NULL DEFAULT now(), ended_at timestamptz)`,
		`CREATE TABLE posts (id varchar(36) PRIMARY KEY, author_id varchar(36) NOT NULL, posting_window_id varchar(36) NOT NULL, image_id text NOT NULL DEFAULT 'image', caption text NOT NULL DEFAULT '', visibility text NOT NULL DEFAULT 'public', location_kind text NOT NULL DEFAULT 'none', latitude numeric, longitude numeric, locality text, posted_at timestamptz NOT NULL DEFAULT now(), deleted_at timestamptz)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	const key = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, user := range []struct{ id, region, status string }{
		{"viewer", "america", "active"}, {"friend-a", "europe", "active"}, {"friend-b", "asia", "active"},
		{"friend-c", "america", "active"}, {"blocked-out", "america", "active"}, {"blocked-in", "america", "active"},
		{"alone", "europe", "active"}, {"inactive", "america", "deleted"},
		{"old-only", "america", "active"}, {"deleted-only", "america", "active"},
		{"other-region-date", "america", "active"},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO users (id, username, secret_key_hash, display_name, region, status) VALUES ($1,$2,$3,$4,$5,$6)`, user.id, user.id, key, user.id, user.region, user.status); err != nil {
			t.Fatal(err)
		}
	}
	for i, edge := range [][2]string{{"friend-a", "viewer"}, {"friend-b", "viewer"}, {"friend-a", "friend-b"}, {"friend-a", "friend-c"}} {
		if _, err := db.ExecContext(ctx, `INSERT INTO friendships (id,user_low_id,user_high_id) VALUES ($1,$2,$3)`, string(rune('a'+i)), edge[0], edge[1]); err != nil {
			t.Fatal(err)
		}
	}
	for i, edge := range [][2]string{{"viewer", "blocked-out"}, {"blocked-in", "viewer"}} {
		if _, err := db.ExecContext(ctx, `INSERT INTO block_relations (id,blocker_id,blocked_id) VALUES ($1,$2,$3)`, string(rune('a'+i)), edge[0], edge[1]); err != nil {
			t.Fatal(err)
		}
	}
	for _, window := range []struct {
		id, region, date string
		ended            any
	}{
		{"current-america", "america", "2026-10-31", nil}, {"current-europe", "europe", "2026-10-31", nil},
		{"old-america", "america", "2026-10-30", "2026-10-30 00:00:00+00"},
		{"next-day-europe", "europe", "2026-11-01", nil},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO posting_windows (id,region,local_date,ended_at) VALUES ($1,$2,$3,$4)`, window.id, window.region, window.date, window.ended); err != nil {
			t.Fatal(err)
		}
	}
	for _, post := range []struct {
		id, window string
		deleted    any
	}{
		{"same-day", "current-america", nil}, {"old-day", "old-america", nil}, {"deleted-same-day", "current-america", "2026-10-31 01:00:00+00"},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO posts (id,author_id,posting_window_id,deleted_at) VALUES ($1,'viewer',$2,$3)`, post.id, post.window, post.deleted); err != nil {
			t.Fatal(err)
		}
	}
	for _, post := range []struct{ id, author string }{
		{"old-only-post", "old-only"}, {"deleted-only-post", "deleted-only"},
		{"other-region-date-post", "other-region-date"},
	} {
		deleted := any(nil)
		if post.author == "deleted-only" {
			deleted = "2026-10-31 01:00:00+00"
		}
		window := "old-america"
		if post.author == "deleted-only" {
			window = "current-america"
		}
		if post.author == "other-region-date" {
			window = "next-day-europe"
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO posts (id,author_id,posting_window_id,deleted_at) VALUES ($1,$2,$3,$4)`, post.id, post.author, window, deleted); err != nil {
			t.Fatal(err)
		}
	}
	for _, post := range []struct{ id, author, visibility, window string }{
		{"comment-own", "viewer", "public", "current-america"},
		{"comment-friend", "friend-a", "friends", "current-europe"},
		{"comment-fof", "friend-c", "friends_of_friends", "current-america"},
		{"comment-public", "friend-c", "public", "current-america"},
		{"comment-blocked", "blocked-out", "public", "current-america"},
		{"comment-ended", "friend-a", "public", "old-america"},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO posts (id,author_id,visibility,posting_window_id) VALUES ($1,$2,$3,$4)`, post.id, post.author, post.visibility, post.window); err != nil {
			t.Fatal(err)
		}
	}

	for _, viewerID := range []string{"viewer", "alone", "old-only", "deleted-only", "other-region-date", "inactive", "missing"} {
		old, oldErr := loadViewerContextFormer(ctx, db, viewerID)
		got, gotErr := loadViewerContext(ctx, db, viewerID)
		if !sameLoadError(oldErr, gotErr) {
			t.Fatalf("%s: errors differ: former=%v combined=%v", viewerID, oldErr, gotErr)
		}
		if oldErr == nil && !reflect.DeepEqual(old, got) {
			t.Errorf("%s: contexts differ\nformer=%#v\ncombined=%#v", viewerID, old, got)
		}
		if viewerID == "viewer" && oldErr == nil && len(got.User.ImageKey) == 0 {
			t.Error("setImageKey was not preserved")
		}
		if oldErr == nil && viewerID == "viewer" {
			for _, postID := range []string{"comment-own", "comment-friend", "comment-fof", "comment-public", "comment-blocked", "comment-ended"} {
				post, err := getPostWithAuthor(ctx, db, postID)
				if err != nil {
					t.Fatal(err)
				}
				want := old.canView(post.Post) && post.WindowEndedAt == nil
				if want {
					if old.ID == post.AuthorID {
						want = old.HasPostedToday
					} else if post.Visibility == "public" {
						want = true
					} else {
						want = old.Friends[post.AuthorID]
					}
				}
				gotComment, err := canComment(ctx, db, viewerID, postID)
				if err != nil {
					t.Fatal(err)
				}
				if gotComment != want {
					t.Errorf("canComment(%s,%s)=%v want %v", viewerID, postID, gotComment, want)
				}
				if got.canView(post.Post) != old.canView(post.Post) {
					t.Errorf("canView differs for %s", postID)
				}
			}
		}
	}
}

func sameLoadError(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	return errors.Is(a, sql.ErrNoRows) == errors.Is(b, sql.ErrNoRows)
}

// loadViewerContextFormer is the pre-optimization implementation retained only
// for the disposable database comparison.
func loadViewerContextFormer(ctx context.Context, db sqlx.QueryerContext, viewerID string) (viewerContext, error) {
	viewer := viewerContext{Friends: map[string]bool{}, FriendsOfFriends: map[string]bool{}, MutualFriends: map[string]int{}}
	var err error
	if viewer.User, err = getUser(ctx, db, viewerID); err != nil {
		return viewer, err
	}
	if err := sqlx.GetContext(ctx, db, &viewer.HasPostedToday, `SELECT EXISTS(SELECT 1 FROM posts p JOIN posting_windows posted ON posted.id=p.posting_window_id JOIN posting_windows current ON current.region=$2 AND current.ended_at IS NULL AND current.local_date=posted.local_date WHERE p.author_id=$1 AND p.deleted_at IS NULL)`, viewer.ID, viewer.Region); err != nil {
		return viewer, err
	}
	if viewer.Blocked, err = getBlockedUserIDs(ctx, db, viewer.ID); err != nil {
		return viewer, err
	}
	friends, err := getFriendIDs(ctx, db, viewer.ID)
	if err != nil {
		return viewer, err
	}
	if len(friends) == 0 {
		return viewer, nil
	}
	for _, id := range friends {
		viewer.Friends[id] = true
	}
	query, args, err := sqlx.In(`SELECT user_low_id, user_high_id FROM friendships WHERE user_low_id IN (?) OR user_high_id IN (?)`, friends, friends)
	if err != nil {
		return viewer, err
	}
	var friendships []struct {
		UserLowID  string `db:"user_low_id"`
		UserHighID string `db:"user_high_id"`
	}
	if err := sqlx.SelectContext(ctx, db, &friendships, sqlx.Rebind(sqlx.DOLLAR, query), args...); err != nil {
		return viewer, err
	}
	for _, friendship := range friendships {
		if viewer.Friends[friendship.UserLowID] {
			viewer.FriendsOfFriends[friendship.UserHighID] = true
			if friendship.UserHighID != viewer.ID {
				viewer.MutualFriends[friendship.UserHighID]++
			}
		}
		if viewer.Friends[friendship.UserHighID] {
			viewer.FriendsOfFriends[friendship.UserLowID] = true
			if friendship.UserLowID != viewer.ID {
				viewer.MutualFriends[friendship.UserLowID]++
			}
		}
	}
	return viewer, nil
}
