package isureal

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
)

const imagePreloadBatch = 256

type imagePreloadWorker struct {
	sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

var profileImagePreloader imagePreloadWorker

// Caller holds the worker lock, including while clearing caches, so a new
// worker cannot start between cancellation and the reset.
func (w *imagePreloadWorker) stopLocked() {
	if w.cancel != nil {
		w.cancel()
		<-w.done
		w.cancel = nil
		w.done = nil
	}
}

func (w *imagePreloadWorker) startLocked(run func(context.Context)) {
	w.stopLocked()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	done := make(chan struct{})
	w.cancel, w.done = cancel, done
	go func() {
		defer close(done)
		defer cancel()
		run(ctx)
	}()
}

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
	profileImagePreloader.Lock()
	defer profileImagePreloader.Unlock()
	profileImagePreloader.startLocked(func(ctx context.Context) { warmProfileImages(ctx, db); warmRecentPostImages(ctx, db) })
}

// imagePreloadPostBytes bounds how much of the initial posts' image bytes the
// preload pulls into the cache; the cache itself stays capped at its limit.
const imagePreloadPostBytes = 96 << 20

// warmRecentPostImages fills the image cache with the newest initial posts'
// images and precomputes their signatures. Memories and the initial feeds read
// these during the first days, and they are initial data: the rows come from
// the freshly reset database, so nothing from a previous run survives.
func warmRecentPostImages(ctx context.Context, db *sqlx.DB) {
	if db == nil {
		return
	}
	started := time.Now()
	var posts []struct {
		ImageID       string    `db:"image_id"`
		SecretKeyHash string    `db:"secret_key_hash"`
		CreatedAt     time.Time `db:"created_at"`
	}
	if err := db.SelectContext(ctx, &posts, `
  SELECT p.image_id, u.secret_key_hash, p.created_at
  FROM posts p JOIN users u ON u.id = p.author_id AND u.status = 'active'
  WHERE p.deleted_at IS NULL
  ORDER BY p.posted_at DESC, p.id DESC
  LIMIT 400`); err != nil {
		log.Printf("post image preload: list posts: %v", err)
		return
	}
	loaded, bytes := 0, 0
	exp := imageURLExpiry(time.Now())
batchLoop:
	for start := 0; start < len(posts) && bytes < imagePreloadPostBytes && ctx.Err() == nil; start += 64 {
		end := min(start+64, len(posts))
		ids := make([]string, 0, end-start)
		keys := make(map[string]string, end-start)
		for _, p := range posts[start:end] {
			ids = append(ids, p.ImageID)
			keys[p.ImageID] = p.SecretKeyHash
		}
		var rows []struct {
			ID        string `db:"id"`
			Data      []byte `db:"data"`
			MediaType string `db:"media_type"`
		}
		if err := db.SelectContext(ctx, &rows, `SELECT id, data, media_type FROM image_objects WHERE id = ANY($1)`, ids); err != nil {
			log.Printf("post image preload: load batch: %v", err)
			return
		}
		for _, row := range rows {
			if len(row.Data) == 0 {
				continue
			}
			if bytes+len(row.Data) > imagePreloadPostBytes {
				break batchLoop
			}
			putCachedImage(row.ID, &cachedImage{data: row.Data, mediaType: row.MediaType})
			if key, err := imageSigningKey(keys[row.ID]); err == nil {
				cachedImageSignature(key, row.ID, exp)
			}
			loaded++
			bytes += len(row.Data)
		}
	}
	log.Printf("post image preload: %d images, %d bytes in %s", loaded, bytes, time.Since(started).Round(time.Millisecond))
}
