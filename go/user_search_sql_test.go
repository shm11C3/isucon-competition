package isureal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

func TestNormalizeSearchTextPreservesNUL(t *testing.T) {
	if got := normalizeSearchText("a\x00b"); got != "a\x00b" {
		t.Fatalf("normalized NUL input = %q, want raw NUL preserved", got)
	}
}

func TestSearchUsersNULReturnsEmptyWithoutDatabase(t *testing.T) {
	e := echo.New()
	e.GET("/api/users", searchUsersHandler(&HandlerContext{}))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/users?q=%00", nil)
	e.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var response searchUsersResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Items == nil || len(response.Items) != 0 || response.HasMore {
		t.Fatalf("response = %+v, want empty items and hasMore=false", response)
	}
}

func TestNormalizeSearchText(t *testing.T) {
	cases := map[string]string{
		"Straße":  "strasse",
		"e\u0301": "é",
		"ΟΣ":      "οσ",
		"İ":       "i\u0307",
	}
	for input, want := range cases {
		if got := normalizeSearchText(input); got != want {
			t.Errorf("normalizeSearchText(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSearchUsersSQLMatchesGoOracle(t *testing.T) {
	if os.Getenv("ISUREAL_SEARCH_TEST") == "" {
		t.Skip("set ISUREAL_SEARCH_TEST=1 to run against the isolated PostgreSQL fixture")
	}
	db, err := sqlx.Connect("pgx", ConfigFromEnv().DatabaseDSN())
	if err != nil {
		t.Fatal("connect isolated PostgreSQL fixture")
	}
	defer db.Close()
	ctx := context.Background()
	for _, stmt := range []string{
		`CREATE TEMP TABLE users (id text PRIMARY KEY, username text NOT NULL, secret_key_hash text NOT NULL DEFAULT '', display_name text NOT NULL, bio text NOT NULL DEFAULT '', profile_image_id text, region text NOT NULL DEFAULT 'america', account_type text NOT NULL DEFAULT 'regular', status text NOT NULL DEFAULT 'active', search_username text, search_display_name text)`,
		`CREATE TEMP TABLE block_relations (blocker_id text NOT NULL, blocked_id text NOT NULL)`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatal("create temporary search fixture")
		}
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatal("begin fixture insert")
	}
	for i := 0; i < 5000; i++ {
		id, username, name := fmt.Sprintf("id-%05d", i), fmt.Sprintf("user-%05d", i), fmt.Sprintf("Member %05d", i)
		if i < 60 {
			name = fmt.Sprintf("needle %05d", i)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO users (id,username,display_name,search_username,search_display_name) VALUES ($1,$2,$3,$4,$5)`, id, username, name, normalizeSearchText(username), normalizeSearchText(name)); err != nil {
			t.Fatal("insert search fixture")
		}
	}
	for i, value := range []struct{ username, display string }{
		{"unicode-ss", "Straße"}, {"unicode-e", "Cafe\u0301"}, {"unicode-greek", "ΟΣ"},
		{"unicode-tr", "İstanbul"}, {"literal-percent", "100%_real"},
		{"literal-backslash", `path\needle`},
	} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO users (id,username,display_name,search_username,search_display_name) VALUES ($1,$2,$3,$4,$5)`, fmt.Sprintf("unicode-%d", i), value.username, value.display, normalizeSearchText(value.username), normalizeSearchText(value.display)); err != nil {
			t.Fatal("insert unicode fixture")
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO users (id,username,display_name,status,search_username,search_display_name) VALUES ('inactive','inactive','needle inactive','deleted','inactive','needle inactive')`); err != nil {
		t.Fatal("insert inactive fixture")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO users (id,username,display_name,search_username,search_display_name) VALUES ('viewer','viewer','needle viewer','viewer','needle viewer')`); err != nil {
		t.Fatal("insert viewer fixture")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO block_relations VALUES ('viewer','id-00001'),('id-00002','viewer')`); err != nil {
		t.Fatal("insert block fixture")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal("commit fixture")
	}
	queries := []string{"needle", "strasse", "café", "οσ", "i\u0307stanbul", "%_", `\`}
	blocked := map[string]bool{"id-00001": true, "id-00002": true}
	for _, rawQuery := range queries {
		for _, cursor := range []struct{ username, id string }{{"", ""}, {"user-00020", "id-00020"}} {
			query := normalizeSearchText(rawQuery)
			var got []User
			if err := db.SelectContext(ctx, &got, searchUsersSQL, "viewer", cursor.username, cursor.id, searchLikePattern(query)); err != nil {
				t.Fatal("run SQL search")
			}
			var all []struct {
				ID          string `db:"id"`
				Username    string `db:"username"`
				DisplayName string `db:"display_name"`
				Status      string `db:"status"`
			}
			if err := db.SelectContext(ctx, &all, `SELECT id,username,display_name,status FROM users WHERE id<>'viewer' AND status='active' AND ($1='' OR (username,id)>($1,$2)) ORDER BY username,id`, cursor.username, cursor.id); err != nil {
				t.Fatal("load Go oracle inputs")
			}
			want := make([]string, 0, 51)
			for _, row := range all {
				if !strings.Contains(normalizeSearchText(row.Username), query) && !strings.Contains(normalizeSearchText(row.DisplayName), query) {
					continue
				}
				if blocked[row.ID] {
					continue
				}
				want = append(want, row.ID)
				if len(want) == 51 {
					break
				}
			}
			gotIDs := make([]string, len(got))
			for i := range got {
				gotIDs[i] = got[i].ID
			}
			if !reflect.DeepEqual(gotIDs, want) {
				t.Errorf("query %q cursor %q IDs differ: got %v want %v", rawQuery, cursor.username, gotIDs, want)
			}
			hasMore := len(got) > 50
			if hasMore != (len(want) > 50) {
				t.Errorf("query %q cursor %q hasMore=%v want %v", rawQuery, cursor.username, hasMore, len(want) > 50)
			}
		}
	}
	// Display name writes and reset restore the same normalized value as the source.
	if _, err := db.ExecContext(ctx, `CREATE TEMP TABLE initial_users AS SELECT * FROM users`); err != nil {
		t.Fatal("snapshot fixture")
	}
	if _, err := db.ExecContext(ctx, `UPDATE users SET display_name='renamed needle',search_display_name=$1 WHERE id='id-00000'`, normalizeSearchText("renamed needle")); err != nil {
		t.Fatal("update fixture")
	}
	var updated int
	if err := db.GetContext(ctx, &updated, `SELECT count(*) FROM users WHERE id='id-00000' AND strpos(search_display_name,'renamed')>0`); err != nil || updated != 1 {
		t.Fatal("updated normalized value not searchable")
	}
	var renamed []User
	if err := db.SelectContext(ctx, &renamed, searchUsersSQL, "viewer", "", "", searchLikePattern(normalizeSearchText("renamed"))); err != nil || len(renamed) != 1 || renamed[0].ID != "id-00000" {
		t.Fatal("updated display name did not match search query")
	}
	if _, err := db.ExecContext(ctx, `UPDATE users u SET display_name=i.display_name,search_username=i.search_username,search_display_name=i.search_display_name FROM initial_users i WHERE u.id=i.id AND u.id='id-00000'`); err != nil {
		t.Fatal("reset fixture")
	}
	var restored int
	if err := db.GetContext(ctx, &restored, `SELECT count(*) FROM users WHERE id='id-00000' AND strpos(search_display_name,'renamed')=0 AND strpos(search_display_name,'needle')>0`); err != nil || restored != 1 {
		t.Fatal("reset did not restore normalized source")
	}
	var oldName []User
	if err := db.SelectContext(ctx, &oldName, searchUsersSQL, "viewer", "", "", searchLikePattern(normalizeSearchText("needle"))); err != nil || len(oldName) == 0 || oldName[0].ID != "id-00000" {
		t.Fatal("reset source was not searchable")
	}
}
