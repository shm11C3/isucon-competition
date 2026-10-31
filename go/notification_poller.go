package isureal

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jmoiron/sqlx"
)

const notificationPollInterval = 500 * time.Millisecond

// activeWindowGenerations maps a region to its open posting window. One poller
// per process lets idle SSE connections skip their per-user queries.
var activeWindowGenerations atomic.Pointer[map[string]string]
var notificationPollerOnce sync.Once

func startNotificationPoller(db *sqlx.DB) {
	notificationPollerOnce.Do(func() {
		pollActiveWindows(db)
		go func() {
			ticker := time.NewTicker(notificationPollInterval)
			defer ticker.Stop()
			for range ticker.C {
				pollActiveWindows(db)
			}
		}()
	})
}

func pollActiveWindows(db *sqlx.DB) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var windows []struct {
		ID     string `db:"id"`
		Region string `db:"region"`
	}
	if err := db.SelectContext(ctx, &windows, `SELECT id, region FROM posting_windows WHERE ended_at IS NULL`); err != nil {
		return
	}
	generations := make(map[string]string, len(windows))
	for _, window := range windows {
		generations[window.Region] = window.ID
	}
	activeWindowGenerations.Store(&generations)
}

func activeWindowGeneration(region string) string {
	generations := activeWindowGenerations.Load()
	if generations == nil {
		return ""
	}
	return (*generations)[region]
}
