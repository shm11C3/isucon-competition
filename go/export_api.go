package isureal

import (
	"errors"
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v5"
)

type exportPostResponse struct {
	createPostResponse
	ImagePath string `json:"imagePath"`
}

func exportPostsHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()
		userID := GetSession(c).UserID
		author, err := getUser(ctx, h.DB, userID)
		if err != nil {
			return err
		}
		var posts []struct {
			Post
			ImageData []byte `db:"image_data"`
		}
		if err := h.DB.SelectContext(ctx, &posts, `
   SELECT `+postColumns+`, COALESCE(i.data, ''::bytea) AS image_data
   FROM posts p
   JOIN posting_windows w ON w.id = p.posting_window_id
   LEFT JOIN image_objects i ON i.id = p.image_id
    AND i.created_at >= p.created_at - interval '1 day'
    AND i.created_at < p.created_at + interval '1 day'
   WHERE p.author_id = $1 AND p.deleted_at IS NULL
   ORDER BY p.posted_at, p.id
  `, userID); err != nil {
			return err
		}

		var body bytes.Buffer
		writer := zip.NewWriter(&body)
		defer writer.Close()
		items := make([]exportPostResponse, 0, len(posts))
		for _, post := range posts {
			if len(post.ImageData) == 0 {
				// image_objects is range-partitioned by created_at; the join above
				// prunes to the post's own month. Bytes that live in the file store
				// (empty row) or in a partition outside that window are fetched here.
				var data []byte
				var err error
				if h.ImageStore != nil {
					data, err = h.ImageStore.Get(ctx, post.ImageID)
					if err != nil && !errors.Is(err, errImageStoreMissing) {
						return err
					}
				}
				if len(data) == 0 {
					if err := h.DB.GetContext(ctx, &data, `SELECT data FROM image_objects WHERE id = $1`, post.ImageID); err != nil {
						return err
					}
				}
				post.ImageData = data
			}
			imagePath := fmt.Sprintf("images/%06d.jpg", len(items)+1)
			image, err := writer.CreateHeader(&zip.FileHeader{Name: imagePath, Method: zip.Store})
			if err != nil {
				return err
			}
			if _, err := image.Write(post.ImageData); err != nil {
				return err
			}
			items = append(items, exportPostResponse{createPostResponse: newPostResponse(post.Post, author), ImagePath: imagePath})
		}
		manifest, err := writer.Create("posts.json")
		if err != nil {
			return err
		}
		if err := json.NewEncoder(manifest).Encode(items); err != nil {
			return err
		}
		if err := writer.Close(); err != nil {
			return err
		}
		c.Response().Header().Set(echo.HeaderContentDisposition, `attachment; filename="isureal-posts.zip"`)
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return c.Blob(http.StatusOK, "application/zip", body.Bytes())
	}
}
