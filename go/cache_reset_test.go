package isureal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
)

func TestCacheResetRouteAuthenticationAndClear(t *testing.T) {
	resetImageCache()
	resetImageSignatures()
	resetRecentImageKeys()
	clearNormalizeSearchTextCache()
	putCachedImage("reset-test", &cachedImage{data: []byte("image")})
	imageSignatures.Lock()
	imageSignatures.entries[imageSignatureKey{key: "k", imageID: "id", exp: 1}] = []byte("sig")
	imageSignatures.Unlock()
	recentImageKeys.Lock()
	recentImageKeys.entries["id"] = recentImageKey{key: []byte("k"), checkedAt: time.Now()}
	recentImageKeys.Unlock()
	normalizeSearchText("cache reset probe")
	active := map[string]string{"asia": "old"}
	activeWindowGenerations.Store(&active)

	e := echo.New()
	registerCacheResetRoute(e, "secret", &HandlerContext{})
	unauthorized := httptest.NewRequest(http.MethodPost, "/internal/cache/reset", nil)
	rr := httptest.NewRecorder()
	e.ServeHTTP(rr, unauthorized)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("unauthorized status = %d", rr.Code)
	}
	authorized := httptest.NewRequest(http.MethodPost, "/internal/cache/reset", nil)
	authorized.Header.Set(imageStoreTokenHeader, "secret")
	rr = httptest.NewRecorder()
	e.ServeHTTP(rr, authorized)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("authorized status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if getCachedImage("reset-test") != nil {
		t.Fatal("image cache not cleared")
	}
	imageSignatures.RLock()
	signatures := len(imageSignatures.entries)
	imageSignatures.RUnlock()
	if signatures != 0 {
		t.Fatalf("signature entries = %d", signatures)
	}
	recentImageKeys.RLock()
	recent := len(recentImageKeys.entries)
	recentImageKeys.RUnlock()
	if recent != 0 {
		t.Fatalf("recent image keys = %d", recent)
	}
	normalizeSearchTextCache.RLock()
	normalized := len(normalizeSearchTextCache.values)
	normalizeSearchTextCache.RUnlock()
	if normalized != 0 {
		t.Fatalf("normalized values = %d", normalized)
	}
	if got := activeWindowGeneration("asia"); got != "" {
		t.Fatalf("active generation = %q", got)
	}
}

func TestResetPeerCachesWaitsAndChecksStatus(t *testing.T) {
	if err := resetPeerCaches(context.Background(), nil, "secret"); err == nil {
		t.Fatal("configured image store must not silently skip all peers")
	}
	hit := false
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		hit = true
		if r.Method != http.MethodPost || r.URL.Path != "/internal/cache/reset" {
			t.Errorf("unexpected reset request: %s %s", r.Method, r.URL.String())
		}
		status := http.StatusNoContent
		if r.Header.Get(imageStoreTokenHeader) != "secret" {
			status = http.StatusForbidden
		}
		return &http.Response{StatusCode: status, Body: http.NoBody, Header: make(http.Header), Request: r}, nil
	})}
	if err := resetPeerCachesWithClient(context.Background(), []string{"http://peer"}, "secret", client); err != nil {
		t.Fatal(err)
	}
	if !hit {
		t.Fatal("peer reset was not called")
	}
	if err := resetPeerCachesWithClient(context.Background(), []string{"http://peer"}, "", client); err == nil {
		t.Fatal("expected peer error")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
