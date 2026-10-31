package isureal

import (
	"context"
	"log"
	"time"

	"github.com/jmoiron/sqlx"
)

const imagePreloadBatch = 256

// warmProfileImages fills the image cache with every active user's profile
// image and precomputes their signatures for the current expiry bucket. It
// runs in the background right after the caches are reset (initialize and
// process start), so the first day's profile-image requests are served from
// memory instead of probing every image_objects partition while the caches
// are cold. The data comes from the freshly reset database, so nothing from
// a previous run survives.
func warmProfileImages(ctx context.Context, db *sqlx.DB) {
	if db == nil {
		return
	}
	started := time.Now()
	var users []struct {
		ImageID       string `db:"profile_image_id"`
		SecretKeyHash string `db:"secret_key_hash"`
	}
	if err := db.SelectContext(ctx, &users, `SELECT profile_image_id, secret_key_hash FROM users WHERE status = 'active' AND profile_image_id IS NOT NULL`); err != nil {
		log.Printf("image preload: list users: %v", err)
		return
	}
	loaded := 0
	for start := 0; start < len(users); start += imagePreloadBatch {
		end := min(start+imagePreloadBatch, len(users))
		ids := make([]string, 0, end-start)
		for _, u := range users[start:end] {
			ids = append(ids, u.ImageID)
		}
		var rows []struct {
			ID        string `db:"id"`
			Data      []byte `db:"data"`
			MediaType string `db:"media_type"`
		}
		if err := db.SelectContext(ctx, &rows, `SELECT id, data, media_type FROM image_objects WHERE id = ANY($1)`, ids); err != nil {
			log.Printf("image preload: load batch: %v", err)
			return
		}
		for _, row := range rows {
			if len(row.Data) == 0 {
				continue
			}
			putCachedImage(row.ID, &cachedImage{data: row.Data, mediaType: row.MediaType})
			loaded++
		}
	}
	exp := imageURLExpiry(time.Now())
	signed := 0
	for _, u := range users {
		if ctx.Err() != nil {
			break
		}
		key, err := imageSigningKey(u.SecretKeyHash)
		if err != nil {
			continue
		}
		cachedImageSignature(key, u.ImageID, exp)
		signed++
	}
	log.Printf("image preload: %d images, %d signatures in %s", loaded, signed, time.Since(started).Round(time.Millisecond))
}

func startImagePreload(db *sqlx.DB) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		warmProfileImages(ctx, db)
	}()
}
