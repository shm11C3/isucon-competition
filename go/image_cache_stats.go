package isureal

import (
	"encoding/json"
	"net/http"
	"os"
	"time"
)

// Uses only the existing optional debug listener, never the application router.
func init() {
	http.HandleFunc("/debug/image-cache-stats", func(w http.ResponseWriter, r *http.Request) {
		imageCache.RLock()
		snapshot := struct {
			PID            int       `json:"pid"`
			CapturedAt     time.Time `json:"captured_at"`
			CapacityBytes  int       `json:"capacity_bytes"`
			Entries        int       `json:"entries"`
			Bytes          int       `json:"bytes"`
			CapacityResets uint64    `json:"capacity_resets"`
			Evictions      uint64    `json:"evictions"`
			EvictedBytes   uint64    `json:"evicted_bytes"`
			Insertions     uint64    `json:"insertions"`
			InsertedBytes  uint64    `json:"inserted_bytes"`
		}{os.Getpid(), time.Now().UTC(), imageCacheMaxBytes, len(imageCache.entries), imageCache.bytes,
			imageCache.capacityResets, imageCache.evictions, imageCache.evictedBytes, imageCache.insertions, imageCache.insertedBytes}
		imageCache.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(snapshot)
	})
}
