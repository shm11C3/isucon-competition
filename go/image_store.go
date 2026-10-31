package isureal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
)

// Post image bytes are kept out of the replicated database: the process with
// ISUREAL_IMAGE_STORE_DIR (on the primary host) stores them as fsynced files
// behind an internal HTTP API, and every other process reaches it through
// ISUREAL_IMAGE_STORE_URL. The database row keeps the metadata with an empty
// payload, so a post commit replicates a few kilobytes instead of the JPEG.
// The uploading process also caches the bytes it just received, so same-region
// viewers rarely need to fetch them. Unset, everything behaves as before.

const imageStoreTokenHeader = "X-Image-Store-Token"

var errImageStoreMissing = errors.New("image store: object not found")

type imageStoreClient struct {
	baseURL string
	token   string
	http    *http.Client
}

func newImageStoreClient(baseURL, token string) *imageStoreClient {
	if baseURL == "" {
		return nil
	}
	transport := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:        64,
		MaxIdleConnsPerHost: 64,
		IdleConnTimeout:     90 * time.Second,
	}
	return &imageStoreClient{baseURL: strings.TrimRight(baseURL, "/"), token: token, http: &http.Client{Transport: transport, Timeout: 60 * time.Second}}
}

func (s *imageStoreClient) do(ctx context.Context, method, id string, body []byte) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.baseURL+"/internal/images/"+id, reader)
	if err != nil {
		return nil, err
	}
	if s.token != "" {
		req.Header.Set(imageStoreTokenHeader, s.token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "image/jpeg")
		req.ContentLength = int64(len(body))
	}
	return s.http.Do(req)
}

// Put stores the bytes durably before the caller commits the metadata.
func (s *imageStoreClient) Put(ctx context.Context, id string, data []byte) error {
	resp, err := s.do(ctx, http.MethodPut, id, data)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("image store: put %s returned %d", id, resp.StatusCode)
	}
	return nil
}

func (s *imageStoreClient) Get(ctx context.Context, id string) ([]byte, error) {
	resp, err := s.do(ctx, http.MethodGet, id, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		io.Copy(io.Discard, resp.Body)
		return nil, errImageStoreMissing
	}
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("image store: get %s returned %d", id, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// Wipe removes every stored image (initialize).
func (s *imageStoreClient) Wipe(ctx context.Context) error {
	resp, err := s.do(ctx, http.MethodDelete, "", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("image store: wipe returned %d", resp.StatusCode)
	}
	return nil
}

// warmPeerCache hands freshly uploaded bytes to the GET process on the same
// host (ISUREAL_IMAGE_CACHE_PEER_URL) so same-region views come from memory.
// Best effort: failures are ignored.
func (s *imageStoreClient) warmPeerCache(peerURL, id string, data []byte) {
	if peerURL == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, strings.TrimRight(peerURL, "/")+"/internal/cache/"+id, bytes.NewReader(data))
		if err != nil {
			return
		}
		if s.token != "" {
			req.Header.Set(imageStoreTokenHeader, s.token)
		}
		req.Header.Set("Content-Type", "image/jpeg")
		req.ContentLength = int64(len(data))
		resp, err := s.http.Do(req)
		if err != nil {
			return
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()
}

// registerCacheWarmRoute accepts bytes for the in-memory image cache from a
// sibling process on the same host.
func registerCacheWarmRoute(e *echo.Echo, token string) {
	e.PUT("/internal/cache/:id", func(c *echo.Context) error {
		if token == "" || c.Request().Header.Get(imageStoreTokenHeader) != token {
			return c.NoContent(http.StatusForbidden)
		}
		id := c.Param("id")
		if !validImageStoreID(id) {
			return c.NoContent(http.StatusBadRequest)
		}
		data, err := readUploadBody(c.Request().Body, c.Request().ContentLength)
		if err != nil {
			return err
		}
		putCachedImage(id, &cachedImage{data: data, mediaType: "image/jpeg"})
		return c.NoContent(http.StatusNoContent)
	})
}

// --- server side ---

type imageStoreServer struct {
	dir   string
	token string
}

func newImageStoreServer(dir, token string) (*imageStoreServer, error) {
	if dir == "" {
		return nil, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &imageStoreServer{dir: dir, token: token}, nil
}

func validImageStoreID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

func (s *imageStoreServer) authorized(c *echo.Context) bool {
	return s.token == "" || c.Request().Header.Get(imageStoreTokenHeader) == s.token
}

func (s *imageStoreServer) path(id string) string { return filepath.Join(s.dir, id) }

// writeDurable writes to a temp file, fsyncs it, renames it into place and fsyncs the directory.
func (s *imageStoreServer) writeDurable(id string, data []byte) error {
	tmp, err := os.CreateTemp(s.dir, ".tmp-"+id+"-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, s.path(id)); err != nil {
		os.Remove(tmpName)
		return err
	}
	return syncDir(s.dir)
}

// syncDir makes a rename durable; any failure is reported so the caller never
// acknowledges an upload that might not survive a crash.
func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (s *imageStoreServer) Register(e *echo.Echo) {
	e.PUT("/internal/images/:id", func(c *echo.Context) error {
		if !s.authorized(c) {
			return c.NoContent(http.StatusForbidden)
		}
		id := c.Param("id")
		if !validImageStoreID(id) {
			return c.NoContent(http.StatusBadRequest)
		}
		data, err := readUploadBody(c.Request().Body, c.Request().ContentLength)
		if err != nil {
			return err
		}
		if err := s.writeDurable(id, data); err != nil {
			return err
		}
		return c.NoContent(http.StatusCreated)
	})
	e.GET("/internal/images/:id", func(c *echo.Context) error {
		if !s.authorized(c) {
			return c.NoContent(http.StatusForbidden)
		}
		id := c.Param("id")
		if !validImageStoreID(id) {
			return c.NoContent(http.StatusBadRequest)
		}
		data, err := os.ReadFile(s.path(id))
		if errors.Is(err, os.ErrNotExist) {
			return c.NoContent(http.StatusNotFound)
		}
		if err != nil {
			return err
		}
		return c.Blob(http.StatusOK, "image/jpeg", data)
	})
	e.DELETE("/internal/images/", func(c *echo.Context) error {
		if !s.authorized(c) {
			return c.NoContent(http.StatusForbidden)
		}
		if err := s.wipe(); err != nil {
			return err
		}
		return c.NoContent(http.StatusNoContent)
	})
}

func (s *imageStoreServer) wipe() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(s.dir, entry.Name())); err != nil {
			return err
		}
	}
	return syncDir(s.dir)
}

func readUploadBody(r io.Reader, sizeHint int64) ([]byte, error) {
	var buf bytes.Buffer
	if sizeHint > 0 {
		buf.Grow(int(sizeHint) + 512)
	}
	if _, err := buf.ReadFrom(r); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
