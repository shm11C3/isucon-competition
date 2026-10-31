package isureal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

func TestFeedSessionViewerPostgresAndHTTP(t *testing.T) {
	dsn := os.Getenv("T0014_TEST_DSN")
	if dsn == "" {
		t.Skip("set T0014_TEST_DSN to run feed auth/viewer PostgreSQL and HTTP checks")
	}
	db, err := sqlx.Connect("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	ctx := context.Background()
	for _, stmt := range []string{
		`CREATE TEMP TABLE users (id text PRIMARY KEY, username text NOT NULL, secret_key_hash text NOT NULL, display_name text NOT NULL, bio text NOT NULL DEFAULT '', profile_image_id text, region text NOT NULL, account_type text NOT NULL DEFAULT 'regular', status text NOT NULL DEFAULT 'active')`,
		`CREATE TEMP TABLE login_sessions (id text PRIMARY KEY, user_id text NOT NULL, token_hash text NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), active boolean NOT NULL DEFAULT true)`,
		`CREATE TEMP TABLE friendships (id text PRIMARY KEY, user_low_id text NOT NULL, user_high_id text NOT NULL)`,
		`CREATE TEMP TABLE block_relations (id text PRIMARY KEY, blocker_id text NOT NULL, blocked_id text NOT NULL)`,
		`CREATE TEMP TABLE posting_windows (id text PRIMARY KEY, region text NOT NULL, local_date date NOT NULL, on_time_deadline timestamptz NOT NULL DEFAULT now(), ended_at timestamptz)`,
		`CREATE TEMP TABLE posts (id text PRIMARY KEY, author_id text NOT NULL, posting_window_id text NOT NULL, image_id text NOT NULL, caption text NOT NULL DEFAULT '', visibility text NOT NULL, location_kind text NOT NULL, latitude numeric, longitude numeric, locality text, posted_at timestamptz NOT NULL DEFAULT now(), deleted_at timestamptz)`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	const secret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, u := range []struct{ id, region, status string }{
		{"viewer", "america", "active"}, {"friend-a", "europe", "active"}, {"friend-b", "asia", "active"},
		{"fof", "america", "active"}, {"blocked", "america", "active"}, {"stranger", "america", "active"},
		{"inactive", "america", "deleted"}, {"alone", "america", "active"}, {"no-post", "america", "active"},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO users (id,username,secret_key_hash,display_name,region,status) VALUES ($1,$1,$2,$1,$3,$4)`, u.id, secret, u.region, u.status); err != nil {
			t.Fatal(err)
		}
	}
	for i, edge := range [][2]string{{"friend-a", "viewer"}, {"viewer", "friend-b"}, {"friend-a", "fof"}} {
		if _, err := db.ExecContext(ctx, `INSERT INTO friendships VALUES ($1,$2,$3)`, string(rune('a'+i)), edge[0], edge[1]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO friendships VALUES ('no-post-friend','friend-a','no-post')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO block_relations VALUES ('block','viewer','blocked')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO posting_windows (id,region,local_date,ended_at) VALUES ('current','america','2026-10-31',NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO posts (id,author_id,posting_window_id,image_id,visibility,location_kind) VALUES ('today','viewer','current','image-viewer','public','none')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO posts (id,author_id,posting_window_id,image_id,visibility,location_kind) VALUES ('friend-public','friend-a','current','image-friend','public','none')`); err != nil {
		t.Fatal(err)
	}

	tokenHash := func(token string) string { sum := sha256.Sum256([]byte(token)); return hex.EncodeToString(sum[:]) }
	for _, s := range []struct {
		id, user, token string
		active          bool
	}{
		{"ok", "viewer", "valid-token", true}, {"logout", "viewer", "logout-token", false},
		{"inactive", "inactive", "inactive-token", true}, {"unknown", "missing-user", "unknown-token", true},
		{"wrong-region", "friend-a", "wrong-region-token", true}, {"alone", "alone", "alone-token", true},
		{"no-post", "no-post", "no-post-token", true},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO login_sessions (id,user_id,token_hash,active) VALUES ($1,$2,$3,$4)`, s.id, s.user, tokenHash(s.token), s.active); err != nil {
			t.Fatal(err)
		}
	}

	// The specialized read returns session and first-hop viewer state together.
	row, err := loadFeedSessionViewerRow(ctx, db, tokenHash("valid-token"))
	if err != nil {
		t.Fatal(err)
	}
	old, err := loadViewerContext(ctx, db, "viewer")
	if err != nil {
		t.Fatal(err)
	}
	for _, feed := range []string{"public", "friends", "following", "friends-of-friends"} {
		needGraph := feed == "following" || feed == "friends-of-friends"
		got, err := decodeViewerContext(ctx, db, row.viewerContextRow, needGraph)
		if err != nil {
			t.Fatal(err)
		}
		if needGraph && !reflect.DeepEqual(old, got) {
			t.Errorf("feed %s viewer differs from full context", feed)
		}
		if !needGraph {
			if !reflect.DeepEqual(old.Friends, got.Friends) || !reflect.DeepEqual(old.Blocked, got.Blocked) || old.HasPostedToday != got.HasPostedToday {
				t.Errorf("feed %s core viewer differs", feed)
			}
			for _, author := range []string{"friend-a", "fof", "blocked", "stranger"} {
				for _, visibility := range []string{"public", "friends", "friends_of_friends"} {
					if feed == "public" && visibility != "public" || feed == "friends" && !old.Friends[author] {
						continue
					}
					post := Post{AuthorID: author, Visibility: visibility}
					if old.canView(post) != got.canView(post) || matchesFeed(old, nil, post, feed) != matchesFeed(got, nil, post, feed) {
						t.Errorf("feed %s visibility mismatch author=%s visibility=%s", feed, author, visibility)
					}
				}
			}
		}
	}
	noPostAuthRow, err := loadFeedSessionViewerRow(ctx, db, tokenHash("no-post-token"))
	if err != nil {
		t.Fatal(err)
	}
	noPostRow := noPostAuthRow.viewerContextRow
	if noPostRow.HasPostedToday {
		t.Fatal("no-post fixture unexpectedly has a current post")
	}
	noPostViewer, err := decodeViewerContext(ctx, db, noPostRow, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(noPostViewer.Friends) != 1 || !noPostViewer.Friends["friend-a"] || len(noPostViewer.FriendsOfFriends) != 0 || len(noPostViewer.MutualFriends) != 0 {
		t.Errorf("no-post viewer graph was not omitted: friends=%d fof=%d mutual=%d", len(noPostViewer.Friends), len(noPostViewer.FriendsOfFriends), len(noPostViewer.MutualFriends))
	}

	var logs bytes.Buffer
	h := &HandlerContext{DB: db, Config: &Config{Region: "america"}}
	e := echo.New()
	e.HTTPErrorHandler = APIErrorHandler
	e.Use(ActionLogMiddleware("debug", &logs))
	e.GET("/api/feeds/:feed", getFeedHandler(h), feedSessionViewerMiddleware(h))
	request := func(path, auth string) (int, string) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if auth != "" {
			req.Header.Set(echo.HeaderAuthorization, auth)
		}
		res := httptest.NewRecorder()
		e.ServeHTTP(res, req)
		return res.Code, res.Body.String()
	}
	cases := []struct {
		name, path, auth string
		want             int
	}{
		{"missing", "/api/feeds/public?beforeId=x", "", 401},
		{"malformed", "/api/feeds/public", "Basic valid-token", 401},
		{"logout", "/api/feeds/public", "Bearer logout-token", 401},
		{"inactive user", "/api/feeds/public", "Bearer inactive-token", 401},
		{"unknown user", "/api/feeds/public", "Bearer unknown-token", 401},
		{"region", "/api/feeds/public", "Bearer wrong-region-token", 403},
		{"auth before pagination", "/api/feeds/public?beforeId=x", "Bearer valid-token", 400},
		{"valid no-post feed", "/api/feeds/public", "Bearer alone-token", 200},
		{"valid no-post feed with friends", "/api/feeds/public", "Bearer no-post-token", 200},
		{"valid public feed", "/api/feeds/public", "Bearer valid-token", 200},
		{"valid friends feed", "/api/feeds/friends", "Bearer valid-token", 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := request(tc.path, tc.auth); got != tc.want {
				t.Fatalf("status=%d want=%d", got, tc.want)
			}
		})
	}
	for _, feed := range []string{"public", "friends"} {
		status, body := request("/api/feeds/"+feed, "Bearer valid-token")
		if status != http.StatusOK {
			t.Fatalf("%s feed status=%d body=%s", feed, status, body)
		}
		var response getFeedResponse
		if err := json.Unmarshal([]byte(body), &response); err != nil {
			t.Fatal(err)
		}
		if len(response.Items) != 1 || response.Items[0].ID != "friend-public" || response.HasMore {
			t.Errorf("%s feed response=%+v", feed, response)
		}
	}
	if !strings.Contains(logs.String(), `"user_id":"viewer"`) {
		t.Error("ActionLog did not receive feed session")
	}
	if strings.Contains(logs.String(), "valid-token") {
		t.Error("ActionLog exposed auth token")
	}
}
