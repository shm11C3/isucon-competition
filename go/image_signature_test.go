package isureal

import (
	"bytes"
	"encoding/base64"
	"net/url"
	"strconv"
	"testing"
	"time"
)

func TestSignedImageURLLifetimeMatchesReference(t *testing.T) {
	user := User{ImageKey: bytes.Repeat([]byte{1}, 32)}
	const imageID = "64237636-7c5c-4cbf-a9f0-03429cdd82b6"
	before := time.Now().Add(imageURLLifetime).Unix()
	signed, err := url.Parse(signedImageURL(user, imageID))
	if err != nil {
		t.Fatal(err)
	}
	after := time.Now().Add(imageURLLifetime).Unix()
	exp, err := strconv.ParseInt(signed.Query().Get("exp"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	if exp < before || exp > after {
		t.Fatalf("expiry=%d outside reference Now+24h range [%d,%d]", exp, before, after)
	}
	sig, err := base64.RawURLEncoding.DecodeString(signed.Query().Get("sig"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sig, imageSignature(user.ImageKey, imageID, exp)) {
		t.Fatal("signature does not match the emitted image ID and expiry")
	}
}
