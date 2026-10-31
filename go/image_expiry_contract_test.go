package isureal

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

// This fixture authorizes an existing image. It does not test the visibility SQL.
type imageExpiryConnector struct{}
type imageExpiryDriver struct{}
type imageExpiryConn struct{}
type imageExpiryRows struct{ sent bool }

func (imageExpiryConnector) Connect(context.Context) (driver.Conn, error) {
	return imageExpiryConn{}, nil
}
func (imageExpiryConnector) Driver() driver.Driver { return imageExpiryDriver{} }
func (imageExpiryDriver) Open(string) (driver.Conn, error) {
	return imageExpiryConn{}, nil
}
func (imageExpiryConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (imageExpiryConn) Close() error                        { return nil }
func (imageExpiryConn) Begin() (driver.Tx, error) {
	return nil, fmt.Errorf("unexpected transaction in image expiry fixture")
}
func (imageExpiryConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &imageExpiryRows{}, nil
}
func (*imageExpiryRows) Columns() []string { return []string{"secret_key_hash"} }
func (*imageExpiryRows) Close() error      { return nil }
func (r *imageExpiryRows) Next(values []driver.Value) error {
	if r.sent {
		return io.EOF
	}
	r.sent = true
	values[0] = hex.EncodeToString(make([]byte, sha256.Size))
	return nil
}

func TestGetImageAcceptsValidFutureSignature(t *testing.T) {
	db := sqlx.NewDb(sql.OpenDB(imageExpiryConnector{}), "image-expiry-fixture")
	t.Cleanup(func() { _ = db.Close() })
	e := echo.New()
	e.HTTPErrorHandler = APIErrorHandler
	e.GET("/api/images/:imageId", getImageHandler(&HandlerContext{DB: db}))

	imageID := "expiry-contract-fixture"
	var imageBytes bytes.Buffer
	if err := jpeg.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 1, 1)), nil); err != nil {
		t.Fatal(err)
	}
	putCachedImage(imageID, &cachedImage{data: imageBytes.Bytes(), mediaType: "image/jpeg"})
	t.Cleanup(func() {
		deleteCachedImage(imageID)
		imageSignatures.Lock()
		defer imageSignatures.Unlock()
		for key := range imageSignatures.entries {
			if key.imageID == imageID {
				delete(imageSignatures.entries, key)
			}
		}
	})

	// Generated URL lifetime does not impose a new verification-time upper limit.
	// T50 rejected the second case before checking its otherwise valid signature.
	for _, hours := range []int{1, 48} {
		t.Run(fmt.Sprintf("%dh", hours), func(t *testing.T) {
			exp := time.Now().Add(time.Duration(hours) * time.Hour).Unix()
			value := []byte(strconv.FormatInt(exp, 10) + ":" + imageID)
			for i := 0; i < 1000; i++ {
				mac := hmac.New(sha256.New, make([]byte, sha256.Size))
				_, _ = mac.Write(value)
				value = mac.Sum(nil)
			}
			query := url.Values{"exp": {strconv.FormatInt(exp, 10)}, "sig": {base64.RawURLEncoding.EncodeToString(value)}}
			response := httptest.NewRecorder()
			e.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/images/"+imageID+"?"+query.Encode(), nil))
			if response.Code != http.StatusOK {
				t.Fatalf("correct signature and visible image: got status %d, want 200", response.Code)
			}
			if response.Header().Get("Content-Type") != "image/jpeg" || !bytes.Equal(response.Body.Bytes(), imageBytes.Bytes()) {
				t.Fatal("image response did not preserve MIME and bytes")
			}
		})
	}
}
