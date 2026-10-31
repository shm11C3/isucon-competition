package isureal

import (
	"container/list"
	"strconv"
	"sync"
	"sync/atomic"

	"golang.org/x/sync/singleflight"
)

const imageCacheMaxBytes = 128 << 20
const imageSignatureCacheMaxEntries = 200000

type cachedImage struct {
	data      []byte
	mediaType string
}

// imageCacheEntry is one cached image in the CLOCK ring. referenced is set by
// readers under the read lock, so hits never take the write lock.
type imageCacheEntry struct {
	id         string
	image      *cachedImage
	elem       *list.Element
	referenced atomic.Bool
}

// imageCache is a byte-bounded CLOCK (second-chance) cache: when an insert
// would exceed imageCacheMaxBytes, the hand evicts unreferenced entries in
// insertion order and gives recently read entries one more pass, instead of
// dropping every cached image at once.
var imageCache = struct {
	sync.RWMutex
	entries        map[string]*imageCacheEntry
	ring           *list.List
	bytes          int
	capacityResets uint64 // kept for the stats shape; CLOCK never resets
	evictions      uint64
	evictedBytes   uint64
	insertions     uint64
	insertedBytes  uint64
}{entries: map[string]*imageCacheEntry{}, ring: list.New()}

func getCachedImage(id string) *cachedImage {
	imageCache.RLock()
	defer imageCache.RUnlock()
	entry := imageCache.entries[id]
	if entry == nil {
		return nil
	}
	if !entry.referenced.Load() {
		entry.referenced.Store(true)
	}
	return entry.image
}

func putCachedImage(id string, image *cachedImage) {
	size := len(image.data)
	if size > imageCacheMaxBytes {
		return
	}
	imageCache.Lock()
	defer imageCache.Unlock()
	imageCache.insertions++
	imageCache.insertedBytes += uint64(size)
	if entry, ok := imageCache.entries[id]; ok {
		imageCache.bytes += size - len(entry.image.data)
		entry.image = image
		entry.referenced.Store(true)
		evictImageCacheLocked(entry)
		return
	}
	entry := &imageCacheEntry{id: id, image: image}
	imageCache.bytes += size
	evictImageCacheLocked(entry)
	entry.elem = imageCache.ring.PushBack(entry)
	imageCache.entries[id] = entry
}

// evictImageCacheLocked runs the CLOCK hand from the front of the ring until
// the cache fits its byte budget. keep is never evicted. Every pass clears
// reference bits, so the loop ends after at most two sweeps.
func evictImageCacheLocked(keep *imageCacheEntry) {
	for imageCache.bytes > imageCacheMaxBytes {
		front := imageCache.ring.Front()
		if front == nil {
			return
		}
		entry := front.Value.(*imageCacheEntry)
		if entry == keep || entry.referenced.Load() {
			entry.referenced.Store(false)
			imageCache.ring.MoveToBack(front)
			if entry == keep && imageCache.ring.Len() == 1 {
				return
			}
			continue
		}
		imageCache.ring.Remove(front)
		delete(imageCache.entries, entry.id)
		imageCache.bytes -= len(entry.image.data)
		imageCache.evictions++
		imageCache.evictedBytes += uint64(len(entry.image.data))
	}
}

func deleteCachedImage(id string) {
	imageCache.Lock()
	defer imageCache.Unlock()
	if entry, ok := imageCache.entries[id]; ok {
		imageCache.bytes -= len(entry.image.data)
		imageCache.ring.Remove(entry.elem)
		delete(imageCache.entries, id)
	}
}

type imageSignatureKey struct {
	key     string
	imageID string
	exp     int64
}

var imageSignatures = struct {
	sync.RWMutex
	entries        map[imageSignatureKey][]byte
	calculations   uint64
	capacityResets uint64
	evictedEntries uint64
}{entries: map[imageSignatureKey][]byte{}}

var imageSignatureFlights singleflight.Group

func cachedImageSignature(key []byte, imageID string, exp int64) []byte {
	return cachedImageSignatureWith(key, imageID, exp, imageSignature)
}

func cachedImageSignatureWith(key []byte, imageID string, exp int64, calculate func([]byte, string, int64) []byte) []byte {
	cacheKey := imageSignatureKey{key: string(key), imageID: imageID, exp: exp}
	imageSignatures.RLock()
	sig, ok := imageSignatures.entries[cacheKey]
	imageSignatures.RUnlock()
	if ok {
		return sig
	}
	flightKey := strconv.Itoa(len(cacheKey.key)) + ":" + cacheKey.key + strconv.Itoa(len(cacheKey.imageID)) + ":" + cacheKey.imageID + ":" + strconv.FormatInt(cacheKey.exp, 10)
	result, _, _ := imageSignatureFlights.Do(flightKey, func() (any, error) {
		// Another caller may have filled the cache after our first check.
		imageSignatures.RLock()
		sig, ok := imageSignatures.entries[cacheKey]
		imageSignatures.RUnlock()
		if ok {
			return sig, nil
		}

		sig = calculate(key, imageID, exp)
		imageSignatures.Lock()
		imageSignatures.calculations++
		if len(imageSignatures.entries) >= imageSignatureCacheMaxEntries {
			imageSignatures.capacityResets++
			imageSignatures.evictedEntries += uint64(len(imageSignatures.entries))
			imageSignatures.entries = map[imageSignatureKey][]byte{}
		}
		imageSignatures.entries[cacheKey] = sig
		imageSignatures.Unlock()
		return sig, nil
	})
	return result.([]byte)
}
