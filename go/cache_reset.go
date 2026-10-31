package isureal

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
)

func resetLocalCaches(ctx context.Context, h *HandlerContext) error {
	resetImageCache()
	resetImageSignatures()
	resetRecentImageKeys()
	clearNormalizeSearchTextCache()
	resetActiveWindowGenerations()
	if h.IPBans != nil && h.DB != nil {
		if err := h.IPBans.Reload(ctx, h.DB); err != nil {
			return fmt.Errorf("reload IP bans: %w", err)
		}
	}
	// Refill the (now empty) image cache from the reset database in the
	// background; the initialize response does not wait for it.
	startImagePreload(h.DB)
	return nil
}

func registerCacheResetRoute(e *echo.Echo, token string, h *HandlerContext) {
	e.POST("/internal/cache/reset", func(c *echo.Context) error {
		if token == "" || c.Request().Header.Get(imageStoreTokenHeader) != token {
			return c.NoContent(http.StatusForbidden)
		}
		if err := resetLocalCaches(c.Request().Context(), h); err != nil {
			return err
		}
		c.Logger().Info("cache reset complete")
		return c.NoContent(http.StatusNoContent)
	})
}

func resetPeerCaches(ctx context.Context, urls []string, token string) error {
	return resetPeerCachesWithClient(ctx, urls, token, &http.Client{Timeout: 3 * time.Second})
}

func resetPeerCachesWithClient(ctx context.Context, urls []string, token string, client *http.Client) error {
	for _, baseURL := range urls {
		baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
		if baseURL == "" {
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/internal/cache/reset", nil)
		if err != nil {
			return fmt.Errorf("build cache reset request for %s: %w", baseURL, err)
		}
		req.Header.Set(imageStoreTokenHeader, token)
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("reset cache peer %s: %w", baseURL, err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			return fmt.Errorf("reset cache peer %s returned %d", baseURL, resp.StatusCode)
		}
	}
	return nil
}
