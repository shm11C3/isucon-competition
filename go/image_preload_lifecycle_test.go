package isureal

import (
	"context"
	"testing"
)

func TestImagePreloadReplacementWaitsForOldWorker(t *testing.T) {
	var worker imagePreloadWorker
	started, exited := make(chan struct{}), make(chan struct{})
	worker.Lock()
	worker.startLocked(func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(exited)
	})
	worker.Unlock()
	<-started

	worker.Lock()
	worker.stopLocked()
	select {
	case <-exited:
	default:
		t.Fatal("cache reset could race with an old preload")
	}
	if worker.cancel != nil || worker.done != nil {
		t.Fatal("stopped worker state was retained")
	}
	worker.stopLocked() // A duplicate reset is safe.
	worker.Unlock()
}

func TestCacheResetStopsPreloadBeforeClearing(t *testing.T) {
	started := make(chan struct{})
	profileImagePreloader.Lock()
	profileImagePreloader.startLocked(func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		// Simulate one last write from a canceled database batch.
		putCachedImage("old-preload", &cachedImage{data: []byte("old")})
	})
	profileImagePreloader.Unlock()
	<-started
	if err := resetLocalCaches(context.Background(), &HandlerContext{}); err != nil {
		t.Fatal(err)
	}
	if getCachedImage("old-preload") != nil {
		t.Fatal("pre-reset preload repopulated the cache")
	}
}
