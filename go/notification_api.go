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
			var notification getNotificationsResponse
			err = h.DB.GetContext(ctx, &notification, `
    WITH batch_candidates AS MATERIALIZED (
      SELECT b.recipients ->> $1 AS id, b.posting_window_id, w.region, b.created_at
      FROM notification_batches b JOIN posting_windows w ON w.id = b.posting_window_id
      WHERE b.created_at > $2 AND w.ended_at IS NULL
    )
    SELECT id, posting_window_id, region, created_at
    FROM (
      SELECT n.id, n.posting_window_id, w.region, n.created_at
      FROM notifications n JOIN posting_windows w ON w.id = n.posting_window_id
      WHERE n.user_id = $1 AND n.created_at > $2 AND w.ended_at IS NULL
      UNION ALL
      SELECT id, posting_window_id, region, created_at
      FROM batch_candidates WHERE id IS NOT NULL
    ) AS pending
    ORDER BY created_at, id LIMIT 1
   `, userID, connectedAt)
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
