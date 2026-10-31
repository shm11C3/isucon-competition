package isureal

import (
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/labstack/echo/v5"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

type searchUsersRequest struct {
	Q              string `query:"q"`
	BeforeUsername string `query:"beforeUsername"`
	BeforeID       string `query:"beforeId"`
}

type searchUsersResponse struct {
	Items   []relationshipUserResponse `json:"items"`
	HasMore bool                       `json:"hasMore"`
}

const normalizeSearchTextCacheMaxEntries = 20000

var normalizeSearchTextCache struct {
	sync.RWMutex
	values map[string]string
}

func normalizeSearchText(value string) string {
	normalizeSearchTextCache.RLock()
	normalized, ok := normalizeSearchTextCache.values[value]
	normalizeSearchTextCache.RUnlock()
	if ok {
		return normalized
	}

	normalized = normalizeSearchTextUncached(value)
	normalizeSearchTextCache.Lock()
	defer normalizeSearchTextCache.Unlock()
	if cached, ok := normalizeSearchTextCache.values[value]; ok {
		return cached
	}
	if normalizeSearchTextCache.values == nil {
		normalizeSearchTextCache.values = make(map[string]string)
	}
	if len(normalizeSearchTextCache.values) >= normalizeSearchTextCacheMaxEntries {
		clear(normalizeSearchTextCache.values)
	}
	normalizeSearchTextCache.values[value] = normalized
	return normalized
}

func normalizeSearchTextUncached(value string) string {
	return norm.NFC.String(cases.Fold().String(norm.NFC.String(value)))
}

// NormalizeSearchText is shared with the one-off database backfill command.
func NormalizeSearchText(value string) string { return normalizeSearchText(value) }

var searchUsersSQL = `SELECT ` + strings.Join(userColumns, ", ") + ` FROM users u
 WHERE u.status='active' AND u.id<>$1 AND ($2='' OR (u.username,u.id)>($2,$3))
   AND (strpos(u.search_username,$4)>0 OR strpos(u.search_display_name,$4)>0)
   AND NOT EXISTS (SELECT 1 FROM block_relations b WHERE (b.blocker_id=$1 AND b.blocked_id=u.id) OR (b.blocker_id=u.id AND b.blocked_id=$1))
 ORDER BY u.username,u.id LIMIT 51`

func searchUsersHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input searchUsersRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		if !validUserText(input.Q, 1, 100) {
			return NewAPIError(http.StatusBadRequest, "invalid_relationship")
		}
		if (input.BeforeUsername == "") != (input.BeforeID == "") {
			return NewAPIError(http.StatusBadRequest, "invalid_pagination")
		}
		ctx := c.Request().Context()
		query := normalizeSearchText(input.Q)
		if strings.ContainsRune(query, '\x00') {
			return c.JSON(http.StatusOK, searchUsersResponse{Items: make([]relationshipUserResponse, 0)})
		}
		viewer := GetSession(c).UserID
		var users []User
		if err := h.DB.SelectContext(ctx, &users, searchUsersSQL, viewer, input.BeforeUsername, input.BeforeID, query); err != nil {
			return err
		}
		items := make([]relationshipUserResponse, 0, 51)
		for _, user := range users {
			if err := user.setImageKey(); err != nil {
				return err
			}
			if user.AccountType != "certified_public" {
				user.Bio = ""
			}
			items = append(items, newRelationshipUserResponse(user))
		}
		hasMore := len(items) > 50
		if hasMore {
			items = items[:50]
		}
		return c.JSON(http.StatusOK, searchUsersResponse{Items: items, HasMore: hasMore})
	}
}

type getUserRequest struct {
	UserID string `param:"userId"`
}

type getUserResponse relationshipUserResponse

func getUserHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input getUserRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		ctx := c.Request().Context()
		viewer := GetSession(c).UserID
		user, err := getUser(ctx, h.DB, input.UserID)
		if errors.Is(err, sql.ErrNoRows) {
			return NewAPIError(http.StatusNotFound, "relationship_not_found")
		}
		if err != nil {
			return err
		}
		blocked, err := isBlocked(ctx, h.DB, viewer, user.ID)
		if err != nil {
			return err
		}
		if blocked {
			return NewAPIError(http.StatusNotFound, "relationship_not_found")
		}
		friends, err := getFriendIDs(ctx, h.DB, viewer)
		if err != nil {
			return err
		}
		if user.AccountType != "certified_public" && !slices.Contains(friends, user.ID) {
			user.Bio = ""
		}
		return c.JSON(http.StatusOK, getUserResponse(newRelationshipUserResponse(user)))
	}
}

func newRelationshipUserResponse(user User) relationshipUserResponse {
	return relationshipUserResponse{ID: user.ID, Username: user.Username, DisplayName: user.DisplayName, ProfileImageURL: newGetMeResponse(user).ProfileImageURL, Bio: user.Bio, AccountType: user.AccountType}
}
