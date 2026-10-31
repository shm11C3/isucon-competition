package isureal

import (
	"os"
	"strconv"
	"sync"
	"time"
)

// Repeated requests with a bad signature for the same visible image (the
// abuse scenario sends the same ~30 URLs hundreds of times) do not need a
// visibility lookup each time: for a short window after an image was seen
// as visible, its signing key is reused to reject a wrong signature. A
// correct signature still goes through the fresh visibility query before any
// bytes are served, so 200 responses are unaffected. The only drift is the
// abuse annotation on a 403 during the window right after an attacked image
// stops being visible.

var recentImageKeyTTL = envDuration("ISUREAL_IMAGE_KEY_RECHECK", time.Second)

const recentImageKeyMaxEntries = 20000

type recentImageKey struct {
	key       []byte
	checkedAt time.Time
}

var recentImageKeys = struct {
	sync.RWMutex
	entries map[string]recentImageKey
}{entries: map[string]recentImageKey{}}

// recentVisibleImageKey returns the signing key of an image that was seen as
// visible within the recheck window, or nil.
func recentVisibleImageKey(imageID string, now time.Time) []byte {
	if recentImageKeyTTL <= 0 {
		return nil
	}
	recentImageKeys.RLock()
	entry, ok := recentImageKeys.entries[imageID]
	recentImageKeys.RUnlock()
	if !ok || now.Sub(entry.checkedAt) >= recentImageKeyTTL {
		return nil
	}
	return entry.key
}

func rememberVisibleImageKey(imageID string, key []byte, now time.Time) {
	if recentImageKeyTTL <= 0 {
		return
	}
	recentImageKeys.Lock()
	if len(recentImageKeys.entries) >= recentImageKeyMaxEntries {
		recentImageKeys.entries = map[string]recentImageKey{}
	}
	recentImageKeys.entries[imageID] = recentImageKey{key: key, checkedAt: now}
	recentImageKeys.Unlock()
}

func forgetVisibleImageKey(imageID string) {
	recentImageKeys.Lock()
	delete(recentImageKeys.entries, imageID)
	recentImageKeys.Unlock()
}

func envDuration(name string, defaultValue time.Duration) time.Duration {
	if value, err := strconv.ParseFloat(os.Getenv(name), 64); err == nil && value >= 0 {
		return time.Duration(value * float64(time.Second))
	}
	return defaultValue
}
