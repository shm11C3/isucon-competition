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
		now := time.Now()
		// A wrong signature for an image seen as visible moments ago is
		// rejected without another visibility lookup; a matching signature
		// always re-checks visibility below before any bytes are served.
		if key := recentVisibleImageKey(input.ImageID, now); key != nil {
			if !hmac.Equal(sig, cachedImageSignature(key, input.ImageID, exp)) {
				c.Set("abuse", "invalid_signature")
				return NewAPIError(http.StatusForbidden, "image_forbidden")
			}
		}
		// 公開範囲は毎回確認する。byteaと署名だけキャッシュする。
		var secretKeyHash string
		err = h.DB.GetContext(ctx, &secretKeyHash, imageVisibilityQuery, input.ImageID)
		if errors.Is(err, sql.ErrNoRows) {
			deleteCachedImage(input.ImageID)
			forgetVisibleImageKey(input.ImageID)
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		}
		if err != nil {
			return err
		}
		key, err := imageSigningKey(secretKeyHash)
		if err != nil {
			return err
		}
		rememberVisibleImageKey(input.ImageID, key, now)
		if !hmac.Equal(sig, cachedImageSignature(key, input.ImageID, exp)) {
			c.Set("abuse", "invalid_signature")
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		}

		if exp <= time.Now().Unix() {
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		}
		if cached := getCachedImage(input.ImageID); cached != nil {
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
		putCachedImage(input.ImageID, &cachedImage{data: object.Data, mediaType: object.MediaType})
		return c.Blob(http.StatusOK, object.MediaType, object.Data)
	}
}

// image_objectsの全パーティションを見ないよう、参照元から所有者を引く。
const imageVisibilityQuery = `
   (SELECT u.secret_key_hash FROM users u WHERE u.profile_image_id = $1 AND u.status = 'active')
   UNION ALL
   (SELECT u.secret_key_hash FROM posts p JOIN users u ON u.id = p.author_id AND u.status = 'active'
    WHERE p.image_id = $1 AND p.deleted_at IS NULL)
   UNION ALL
   (SELECT u.secret_key_hash FROM realmojis r
    JOIN users u ON u.id = r.reactor_id AND u.status = 'active'
    JOIN posts p ON p.id = r.post_id AND p.deleted_at IS NULL
    JOIN users author ON author.id = p.author_id AND author.status = 'active'
    WHERE r.image_id = $1 AND r.replaced_at IS NULL AND r.deleted_at IS NULL)
   LIMIT 1
`
