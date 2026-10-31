package isureal

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

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
			CreatedAt time.Time `db:"post_created_at"`
			ImageData []byte    `db:"-"`
		}
		if err := h.DB.SelectContext(ctx, &posts, `
   SELECT `+postColumns+`, p.created_at AS post_created_at
   FROM posts p
   JOIN posting_windows w ON w.id = p.posting_window_id
   WHERE p.author_id = $1 AND p.deleted_at IS NULL
   ORDER BY p.posted_at, p.id
  `, userID); err != nil {
			return err
		}

		// image_objects is RANGE-partitioned by created_at (one partition per
		// month) and the image row is written in the post's transaction. Looking
		// the bytes up per calendar month with explicit bounds lets PostgreSQL
		// prune to one partition (plus a day of slack on each side) instead of
		// probing all 36 partitions per post.
		type monthGroup struct {
			from, to time.Time
			ids      []string
		}
		groups := map[string]*monthGroup{}
		order := []string{}
		for _, post := range posts {
			first := time.Date(post.CreatedAt.UTC().Year(), post.CreatedAt.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
			key := first.Format("2006-01")
			g, ok := groups[key]
			if !ok {
				g = &monthGroup{from: first.AddDate(0, 0, -1), to: first.AddDate(0, 1, 1)}
				groups[key] = g
				order = append(order, key)
			}
			g.ids = append(g.ids, post.ImageID)
		}
		imageData := make(map[string][]byte, len(posts))
		for _, key := range order {
			g := groups[key]
			var rows []struct {
				ID   string `db:"id"`
				Data []byte `db:"data"`
			}
			if err := h.DB.SelectContext(ctx, &rows, `SELECT id, data FROM image_objects WHERE id = ANY($1) AND created_at >= $2 AND created_at < $3`, g.ids, g.from, g.to); err != nil {
				return err
			}
			for _, row := range rows {
				imageData[row.ID] = row.Data
			}
		}

		var body bytes.Buffer
		writer := zip.NewWriter(&body)
		defer writer.Close()
		items := make([]exportPostResponse, 0, len(posts))
		for _, post := range posts {
			post.ImageData = imageData[post.ImageID]
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
				if len(data) == 0 {
					// Same outcome as before: a store-managed row whose bytes are
					// missing is an error, never a silent empty file in the archive.
					return errImageStoreMissing
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
