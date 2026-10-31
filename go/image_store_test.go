package isureal

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/labstack/echo/v5"
)

func TestImageStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	server, err := newImageStoreServer(dir, "secret")
	if err != nil {
		t.Fatal(err)
	}
	e := echo.New()
	server.Register(e)
	ts := httptest.NewServer(e)
	defer ts.Close()

	client := newImageStoreClient(ts.URL, "secret")
	ctx := context.Background()
	data := bytes.Repeat([]byte{0xFF, 0xD8, 0x42}, 50000)
	id := "0f8fad5b-d9cb-469f-a165-70867728950e"
	if err := client.Put(ctx, id, data); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, id)); err != nil {
		t.Fatalf("file not persisted: %v", err)
	}
	got, err := client.Get(ctx, id)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("get mismatch: err=%v len=%d", err, len(got))
	}
	if _, err := client.Get(ctx, "missing-id"); err != errImageStoreMissing {
		t.Fatalf("expected missing, got %v", err)
	}
	if err := client.Put(ctx, "../etc/passwd", data); err == nil {
		t.Fatal("invalid id accepted")
	}
	wrong := newImageStoreClient(ts.URL, "wrong")
	if err := wrong.Put(ctx, id, data); err == nil {
		t.Fatal("wrong token accepted")
	}
	if err := client.Wipe(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get(ctx, id); err != errImageStoreMissing {
		t.Fatalf("expected wiped, got %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("store not empty after wipe: %d", len(entries))
	}
}
