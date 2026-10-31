package isureal

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/labstack/echo/v5"
)

type getImageRequest struct {
	ImageID string `param:"imageId" json:"-"`
	Exp     string `query:"exp" json:"-"`
	Sig     string `query:"sig" json:"-"`
}

func getImageHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		c.Response().Header().Set("Cache-Control", "no-store")
		var input getImageRequest
		if err := c.Bind(&input); err != nil {
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		}
		query, err := url.ParseQuery(c.Request().URL.RawQuery)
		if err != nil || len(query["exp"]) != 1 || len(query["sig"]) != 1 {
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		}
		exp, err := strconv.ParseInt(input.Exp, 10, 64)
		if err != nil || strconv.FormatInt(exp, 10) != input.Exp || exp <= time.Now().Unix() {
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		}
		sig, err := base64.RawURLEncoding.Strict().DecodeString(input.Sig)
		if err != nil || len(sig) != sha256.Size || base64.RawURLEncoding.EncodeToString(sig) != input.Sig {
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		}

		ctx := c.Request().Context()
		cached := getCachedImage(input.ImageID)
		var secretKeyHash string
		if cached != nil && cached.visibilityFresh() {
			secretKeyHash = cached.secretKeyHash
		} else {
			err = h.DB.GetContext(ctx, &secretKeyHash, `
   SELECT u.secret_key_hash
   FROM image_objects i JOIN users u ON u.id = i.owner_user_id
   WHERE i.id = $1 AND u.status = 'active'
  `, input.ImageID)
			if errors.Is(err, sql.ErrNoRows) {
				deleteCachedImage(input.ImageID)
				return NewAPIError(http.StatusForbidden, "image_forbidden")
			}
			if err != nil {
				return err
			}
		}
		key, err := imageSigningKey(secretKeyHash)
		if err != nil {
			return err
		}
		if !hmac.Equal(sig, cachedImageSignature(key, input.ImageID, exp)) {
			c.Set("abuse", "invalid_signature")
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		}

		// 削除状態はimageVisibilityTTLごとに確認する。5秒以内の許容範囲に収まる。
		if cached != nil && cached.visibilityFresh() {
			return c.Blob(http.StatusOK, cached.mediaType, cached.data)
		}
		var visible bool
		err = h.DB.GetContext(ctx, &visible, `SELECT EXISTS (`+imageVisibilityQuery+`)`, input.ImageID)
		if err != nil {
			return err
		}
		if !visible {
			deleteCachedImage(input.ImageID)
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		}
		if exp <= time.Now().Unix() {
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		}
		if cached != nil {
			cached.checkedAt.Store(time.Now().UnixNano())
			return c.Blob(http.StatusOK, cached.mediaType, cached.data)
		}
		var object struct {
			Data      []byte `db:"data"`
			MediaType string `db:"media_type"`
		}
		err = h.DB.GetContext(ctx, &object, `SELECT data, media_type FROM image_objects WHERE id = $1`, input.ImageID)
		if errors.Is(err, sql.ErrNoRows) {
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		}
		if err != nil {
			return err
		}
		putCachedImage(input.ImageID, &cachedImage{secretKeyHash: secretKeyHash, data: object.Data, mediaType: object.MediaType})
		return c.Blob(http.StatusOK, object.MediaType, object.Data)
	}
}

const imageVisibilityQuery = `
   SELECT 1
   FROM image_objects i JOIN users u ON u.id = i.owner_user_id AND u.status = 'active'
   WHERE i.id = $1 AND (
     u.profile_image_id = i.id
     OR EXISTS (
       SELECT 1 FROM posts p
       WHERE p.image_id = i.id AND p.author_id = u.id AND p.deleted_at IS NULL
     )
     OR EXISTS (
       SELECT 1 FROM realmojis r
       JOIN posts p ON p.id = r.post_id AND p.deleted_at IS NULL
       JOIN users author ON author.id = p.author_id AND author.status = 'active'
       WHERE r.image_id = i.id AND r.reactor_id = u.id AND r.replaced_at IS NULL AND r.deleted_at IS NULL
     )
   )
`
