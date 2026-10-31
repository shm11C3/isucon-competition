package isureal

import (
	"testing"
	"time"
)

func TestResetAppCachesClearsAllCaches(t *testing.T) {
	resetImageCacheForTest(t)
	putCachedImage("a", imageOfSize(16))
	_ = cachedImageSignature([]byte("k"), "a", 1)
	recentImageKeys.Lock()
	recentImageKeys.entries["a"] = recentImageKey{key: []byte("k"), checkedAt: time.Now()}
	recentImageKeys.Unlock()

	resetAppCaches()

	if getCachedImage("a") != nil {
		t.Fatal("image cache survived reset")
	}
	checkImageCacheInvariants(t)
	imageSignatures.RLock()
	n := len(imageSignatures.entries)
	imageSignatures.RUnlock()
	if n != 0 {
		t.Fatalf("signature cache has %d entries after reset", n)
	}
	recentImageKeys.RLock()
	m := len(recentImageKeys.entries)
	recentImageKeys.RUnlock()
	if m != 0 {
		t.Fatalf("recent image keys has %d entries after reset", m)
	}
	putCachedImage("b", imageOfSize(16))
	if getCachedImage("b") == nil {
		t.Fatal("cache unusable after reset")
	}
}
