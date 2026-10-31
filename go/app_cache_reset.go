package isureal

import (
	"context"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
)

// The manual requires caches added outside the DB to be initialized too.
// /initialize reaches one process, so it records a reset epoch in the
// primary; every process polls it (through its replica, applied before the
// primary commit returns) and drops its in-memory caches when it changes.

const resetEpochPollInterval = 500 * time.Millisecond

func resetAppCaches() {
	imageCache.Lock()
	imageCache.entries = map[string]*imageCacheEntry{}
	imageCache.ring.Init()
	imageCache.bytes = 0
	imageCache.Unlock()

	imageSignatures.Lock()
	imageSignatures.entries = map[imageSignatureKey][]byte{}
	imageSignatures.Unlock()

	recentImageKeys.Lock()
	recentImageKeys.entries = map[string]recentImageKey{}
	recentImageKeys.Unlock()
}

func writeResetEpoch(ctx context.Context, db *sqlx.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS app_reset_epoch (id int PRIMARY KEY, at timestamptz NOT NULL)`); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, `INSERT INTO app_reset_epoch (id, at) VALUES (1, clock_timestamp())
		ON CONFLICT (id) DO UPDATE SET at = EXCLUDED.at`)
	return err
}

var resetEpochPollerOnce sync.Once

func startResetEpochPoller(db *sqlx.DB) {
	resetEpochPollerOnce.Do(func() {
		var last time.Time
		poll := func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			var at time.Time
			if err := db.GetContext(ctx, &at, `SELECT at FROM app_reset_epoch WHERE id = 1`); err != nil {
				return // table absent until the first /initialize
			}
			// Also fires on the first sighting (table created by the first
			// /initialize after this process started); at startup it is a no-op.
			if !at.Equal(last) {
				resetAppCaches()
			}
			last = at
		}
		poll()
		go func() {
			ticker := time.NewTicker(resetEpochPollInterval)
			defer ticker.Stop()
			for range ticker.C {
				poll()
			}
		}()
	})
}
