package isureal

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
)

type getNotificationsResponse struct {
	ID              string    `json:"id" db:"id"`
	PostingWindowID string    `json:"postingWindowId" db:"posting_window_id"`
	Region          string    `json:"region" db:"region"`
	CreatedAt       time.Time `json:"createdAt" db:"created_at"`
}

func getNotificationsHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()
		session := GetSession(c)
		userID := session.UserID
		var connectedAt time.Time
		if err := h.DB.GetContext(ctx, &connectedAt, `SELECT clock_timestamp()`); err != nil {
			return err
		}
		response := c.Response()
		response.Header().Set("Content-Type", "text/event-stream")
		response.WriteHeader(http.StatusOK)
		controller := http.NewResponseController(response)
		if err := controller.Flush(); err != nil {
			return nil
		}
		startNotificationPoller(h.DB)
		ticker := time.NewTicker(notificationPollInterval)
		defer ticker.Stop()
		var checkedGeneration string
		var checkedUserAt time.Time
		for checked := false; ; checked = true {
			generation := activeWindowGeneration(session.Region)
			if checked && generation == checkedGeneration && time.Since(checkedUserAt) < 5*time.Second {
				select {
				case <-ctx.Done():
					return nil
				case <-ticker.C:
				}
				continue
			}
			checkedGeneration, checkedUserAt = generation, time.Now()
			user, err := getUser(ctx, h.DB, userID)
			if err != nil {
				if !errors.Is(err, sql.ErrNoRows) && ctx.Err() == nil {
					c.Logger().Error("failed to get notification recipient", "error", err)
				}
				return nil
			}
			if user.Region != session.Region {
				return nil
			}
			// A notification exists for every user who was registered when the
			// open window of their region started; its id is derived from the
			// pair so reconnecting clients see the same notification.
			var notification getNotificationsResponse
			err = h.DB.GetContext(ctx, &notification, `
    SELECT uuid_in(md5(w.id || ':' || u.id)::cstring)::text AS id,
           w.id AS posting_window_id, w.region, w.starts_at AS created_at
    FROM posting_windows w JOIN users u ON u.id = $1
    WHERE w.region = $3 AND w.ended_at IS NULL AND w.starts_at > $2 AND u.created_at <= w.starts_at
    ORDER BY w.starts_at LIMIT 1
   `, userID, connectedAt, user.Region)
			if err == nil {
				data, err := json.Marshal(notification)
				if err != nil {
					c.Logger().Error("failed to encode notification", "error", err)
					return nil
				}
				if _, err := fmt.Fprintf(response, "data: %s\n\n", data); err != nil {
					return nil
				}
				_ = controller.Flush()
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				if ctx.Err() == nil {
					c.Logger().Error("failed to get notification", "error", err)
				}
				return nil
			}
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
			}
		}
	}
}
