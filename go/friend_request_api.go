package isureal

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/labstack/echo/v5"
)

type createFriendRequestRequest struct {
	RecipientID string `json:"recipientId"`
}

type createFriendRequestResponse struct {
	RelationID string    `json:"relationId"`
	Status     string    `json:"status"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

func createFriendRequestHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input createFriendRequestRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		ctx := c.Request().Context()
		actor := GetSession(c).UserID
		if actor == input.RecipientID || input.RecipientID == "" {
			return NewAPIError(http.StatusBadRequest, "invalid_relationship")
		}
		var request FriendRequest
		// Row.Scan consumes and closes the result before returning, including
		// deferred autocommit errors. Never send 201 before that succeeds.
		err := h.DB.QueryRowxContext(ctx, `SELECT id, status, updated_at FROM create_friend_request_v1($1,$2,$3,$4)`, actor, input.RecipientID, uuid.NewString(), uuid.NewString()).Scan(&request.ID, &request.Status, &request.UpdatedAt)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) {
				switch pgErr.Code {
				case "PR400":
					return NewAPIError(http.StatusBadRequest, "invalid_relationship")
				case "PR404":
					return NewAPIError(http.StatusNotFound, "relationship_not_found")
				case "PR409":
					return NewAPIError(http.StatusConflict, "relationship_conflict")
				}
			}
			return err
		}
		return c.JSON(http.StatusCreated, createFriendRequestResponse{RelationID: request.ID, Status: request.Status, UpdatedAt: request.UpdatedAt})
	}
}

type transitionFriendRequestRequest struct {
	RequestID string `param:"requestId" json:"-"`
	Action    string `param:"action" json:"-"`
}

type transitionFriendRequestResponse createFriendRequestResponse

func transitionFriendRequestHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input transitionFriendRequestRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		var status string
		switch input.Action {
		case "accept":
			status = "accepted"
		case "reject":
			status = "rejected"
		case "cancel":
			status = "cancelled"
		default:
			return NewAPIError(http.StatusForbidden, "relationship_action_forbidden")
		}
		ctx := c.Request().Context()
		actor := GetSession(c).UserID
		request, err := getFriendRequest(ctx, h.DB, input.RequestID)
		if errors.Is(err, sql.ErrNoRows) {
			return NewAPIError(http.StatusNotFound, "relationship_not_found")
		}
		if err != nil {
			return err
		}
		if (status == "cancelled" && actor != request.RequesterID) || (status != "cancelled" && actor != request.RecipientID) {
			return NewAPIError(http.StatusForbidden, "relationship_action_forbidden")
		}
		tx, err := beginRelationshipTx(ctx, h.DB, request.RequesterID, request.RecipientID)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		request, err = getFriendRequest(ctx, tx, input.RequestID)
		if err != nil {
			return err
		}
		if request.Status != "pending" {
			return NewAPIError(http.StatusForbidden, "relationship_action_forbidden")
		}
		if status == "accepted" {
			blocked, err := isBlocked(ctx, tx, request.RequesterID, request.RecipientID)
			if err != nil {
				return err
			}
			if blocked {
				return NewAPIError(http.StatusConflict, "relationship_conflict")
			}
			if err := insertFriendship(ctx, tx, request); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE friend_requests SET status=$2, updated_at=clock_timestamp() WHERE id=$1`, request.ID, status); err != nil {
			return err
		}
		request, err = getFriendRequest(ctx, tx, request.ID)
		if err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		return c.JSON(http.StatusOK, transitionFriendRequestResponse{RelationID: request.ID, Status: request.Status, UpdatedAt: request.UpdatedAt})
	}
}
