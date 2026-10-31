package isureal

import (
	"strconv"
	"sync"

	"golang.org/x/sync/singleflight"
)

const imageCacheMaxBytes = 128 << 20
const imageSignatureCacheMaxEntries = 200000

type cachedImage struct {
	data      []byte
	mediaType string
}

var imageCache = struct {
	sync.RWMutex
	entries map[string]*cachedImage
	bytes   int
}{entries: map[string]*cachedImage{}}

func getCachedImage(id string) *cachedImage {
	imageCache.RLock()
	defer imageCache.RUnlock()
	return imageCache.entries[id]
}

func putCachedImage(id string, image *cachedImage) {
	imageCache.Lock()
	defer imageCache.Unlock()
	if imageCache.bytes+len(image.data) > imageCacheMaxBytes {
		imageCache.entries = map[string]*cachedImage{}
		imageCache.bytes = 0
	}
	if _, ok := imageCache.entries[id]; !ok {
		imageCache.bytes += len(image.data)
	}
	imageCache.entries[id] = image
}

func deleteCachedImage(id string) {
	imageCache.Lock()
	defer imageCache.Unlock()
	if image, ok := imageCache.entries[id]; ok {
		imageCache.bytes -= len(image.data)
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
	entries map[imageSignatureKey][]byte
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
		if len(imageSignatures.entries) >= imageSignatureCacheMaxEntries {
			imageSignatures.entries = map[imageSignatureKey][]byte{}
		}
		imageSignatures.entries[cacheKey] = sig
		imageSignatures.Unlock()
		return sig, nil
	})
	return result.([]byte)
}
