package isureal

import (
	"testing"
	"time"
)

func TestRecentVisibleImageKey(t *testing.T) {
	now := time.Now()
	key := []byte("0123456789abcdef0123456789abcdef")
	if recentVisibleImageKey("img", now) != nil {
		t.Fatal("unexpected hit before remember")
	}
	rememberVisibleImageKey("img", key, now)
	if got := recentVisibleImageKey("img", now.Add(recentImageKeyTTL/2)); string(got) != string(key) {
		t.Fatalf("expected cached key, got %v", got)
	}
	if recentVisibleImageKey("img", now.Add(recentImageKeyTTL)) != nil {
		t.Fatal("entry must expire after the recheck window")
	}
	rememberVisibleImageKey("img", key, now)
	forgetVisibleImageKey("img")
	if recentVisibleImageKey("img", now) != nil {
		t.Fatal("entry must be dropped when the image is no longer visible")
	}
}
