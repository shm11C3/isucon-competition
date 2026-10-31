package isureal

import (
	"bytes"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCachedImageSignatureSingleflight(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	imageID := fmt.Sprintf("singleflight-%d", time.Now().UnixNano())
	const exp int64 = 1793424000
	cacheKey := imageSignatureKey{key: string(key), imageID: imageID, exp: exp}
	t.Cleanup(func() {
		imageSignatures.Lock()
		delete(imageSignatures.entries, cacheKey)
		imageSignatures.Unlock()
	})

	var calculations atomic.Int32
	calculationStarted := make(chan struct{})
	releaseCalculation := make(chan struct{})
	calculate := func(key []byte, imageID string, exp int64) []byte {
		if calculations.Add(1) == 1 {
			close(calculationStarted)
		}
		<-releaseCalculation
		return imageSignature(key, imageID, exp)
	}

	const callers = 32
	var ready, done sync.WaitGroup
	ready.Add(callers)
	done.Add(callers)
	start := make(chan struct{})
	results := make([][]byte, callers)
	for i := range results {
		go func(i int) {
			defer done.Done()
			ready.Done()
			<-start
			results[i] = cachedImageSignatureWith(key, imageID, exp, calculate)
		}(i)
	}
	ready.Wait()
	close(start)
	<-calculationStarted
	time.Sleep(10 * time.Millisecond)
	close(releaseCalculation)
	done.Wait()

	want := imageSignature(key, imageID, exp)
	if got := calculations.Load(); got != 1 {
		t.Fatalf("signature calculations = %d, want 1", got)
	}
	for i, result := range results {
		if !bytes.Equal(result, want) {
			t.Fatalf("caller %d returned a different signature", i)
		}
	}
}
