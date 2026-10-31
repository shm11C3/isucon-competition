package isureal

import (
	"container/list"
	"fmt"
	"sync"
	"testing"
)

func resetImageCacheForTest(t *testing.T) {
	t.Helper()
	imageCache.Lock()
	imageCache.entries = map[string]*imageCacheEntry{}
	imageCache.ring = list.New()
	imageCache.bytes = 0
	imageCache.Unlock()
	t.Cleanup(func() {
		imageCache.Lock()
		imageCache.entries = map[string]*imageCacheEntry{}
		imageCache.ring = list.New()
		imageCache.bytes = 0
		imageCache.Unlock()
	})
}

func imageOfSize(n int) *cachedImage {
	return &cachedImage{data: make([]byte, n), mediaType: "image/jpeg"}
}

func checkImageCacheInvariants(t *testing.T) {
	t.Helper()
	imageCache.RLock()
	defer imageCache.RUnlock()
	total := 0
	for e := imageCache.ring.Front(); e != nil; e = e.Next() {
		entry := e.Value.(*imageCacheEntry)
		if imageCache.entries[entry.id] != entry {
			t.Fatalf("ring entry %q not in map", entry.id)
		}
		total += len(entry.image.data)
	}
	if imageCache.ring.Len() != len(imageCache.entries) {
		t.Fatalf("ring %d entries, map %d", imageCache.ring.Len(), len(imageCache.entries))
	}
	if total != imageCache.bytes {
		t.Fatalf("bytes counter %d, actual %d", imageCache.bytes, total)
	}
	if imageCache.bytes > imageCacheMaxBytes {
		t.Fatalf("bytes %d over budget %d", imageCache.bytes, imageCacheMaxBytes)
	}
}

func TestImageCacheEvictsOnlyWhatIsNeeded(t *testing.T) {
	resetImageCacheForTest(t)
	const n = 8
	size := imageCacheMaxBytes / n
	for i := 0; i < n; i++ {
		putCachedImage(fmt.Sprint(i), imageOfSize(size))
	}
	// Read everything except "1" so it is the unreferenced victim.
	for i := 0; i < n; i++ {
		if i != 1 && getCachedImage(fmt.Sprint(i)) == nil {
			t.Fatalf("image %d missing before capacity", i)
		}
	}
	putCachedImage("new", imageOfSize(size))
	checkImageCacheInvariants(t)
	if getCachedImage("1") != nil {
		t.Fatal("unreferenced image 1 should be evicted")
	}
	for i := 0; i < n; i++ {
		if i != 1 && getCachedImage(fmt.Sprint(i)) == nil {
			t.Fatalf("referenced image %d evicted; clear-all behaviour", i)
		}
	}
	if getCachedImage("new") == nil {
		t.Fatal("new image not cached")
	}
}

func TestImageCacheAllReferencedStillFits(t *testing.T) {
	resetImageCacheForTest(t)
	size := imageCacheMaxBytes / 4
	for i := 0; i < 4; i++ {
		putCachedImage(fmt.Sprint(i), imageOfSize(size))
		getCachedImage(fmt.Sprint(i))
	}
	putCachedImage("big", imageOfSize(size*2))
	checkImageCacheInvariants(t)
	if getCachedImage("big") == nil {
		t.Fatal("big image not cached")
	}
}

func TestImageCacheReplaceAndDelete(t *testing.T) {
	resetImageCacheForTest(t)
	size := imageCacheMaxBytes / 4
	for i := 0; i < 4; i++ {
		putCachedImage(fmt.Sprint(i), imageOfSize(size))
	}
	// Growing an existing entry evicts others, never itself.
	putCachedImage("0", imageOfSize(size*3))
	checkImageCacheInvariants(t)
	if got := getCachedImage("0"); got == nil || len(got.data) != size*3 {
		t.Fatal("replaced image lost")
	}
	deleteCachedImage("0")
	checkImageCacheInvariants(t)
	if getCachedImage("0") != nil {
		t.Fatal("deleted image still cached")
	}
	putCachedImage("huge", imageOfSize(imageCacheMaxBytes+1))
	checkImageCacheInvariants(t)
	if getCachedImage("huge") != nil {
		t.Fatal("over-budget image must not be cached")
	}
}

func TestImageCacheConcurrent(t *testing.T) {
	resetImageCacheForTest(t)
	size := imageCacheMaxBytes / 64
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 400; i++ {
				id := fmt.Sprint((w*31 + i) % 200)
				switch i % 5 {
				case 0:
					deleteCachedImage(id)
				case 1, 2:
					putCachedImage(id, imageOfSize(size))
				default:
					getCachedImage(id)
				}
			}
		}(w)
	}
	wg.Wait()
	checkImageCacheInvariants(t)
}
