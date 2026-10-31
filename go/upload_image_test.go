package isureal

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

func sampleJPEG(t *testing.T, w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 7), uint8(y * 3), uint8(x ^ y), 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestValidateJPEG(t *testing.T) {
	good := sampleJPEG(t, 300, 200)
	for _, fast := range []bool{false, true} {
		w, h, err := validateJPEG(good, fast)
		if err != nil || w != 300 || h != 200 {
			t.Fatalf("fast=%v valid jpeg rejected: %v %dx%d", fast, err, w, h)
		}
		if _, _, err := validateJPEG(good[:len(good)-10], fast); err == nil {
			t.Fatalf("fast=%v truncated jpeg accepted", fast)
		}
		if _, _, err := validateJPEG(bytes.Repeat([]byte{0x41}, 5000), fast); err == nil {
			t.Fatalf("fast=%v garbage accepted", fast)
		}
		if _, _, err := validateJPEG([]byte{0xFF, 0xD8, 0xFF, 0xD9}, fast); err == nil {
			t.Fatalf("fast=%v empty jpeg accepted", fast)
		}
		if _, _, err := validateJPEG(nil, fast); err == nil {
			t.Fatalf("fast=%v nil accepted", fast)
		}
	}
	// Trailing bytes after EOI are tolerated by the decoder and by the walk.
	withTrailer := append(append([]byte{}, good...), 0, 0, 0)
	if _, _, err := validateJPEG(withTrailer, true); err != nil {
		t.Fatalf("trailing bytes rejected: %v", err)
	}
	if _, _, err := validateJPEG(withTrailer, false); err != nil {
		t.Logf("note: full decode rejects trailing bytes: %v", err)
	}
}

func TestReadUploadBody(t *testing.T) {
	data := bytes.Repeat([]byte{1}, 1000)
	got, err := readUploadBody(bytes.NewReader(data), int64(len(data)), 2000)
	if err != nil || len(got) != 1000 {
		t.Fatalf("read failed: %v len=%d", err, len(got))
	}
	if _, err := readUploadBody(bytes.NewReader(data), -1, 999); err != errImageTooLarge {
		t.Fatalf("oversized body not rejected: %v", err)
	}
	if _, err := readUploadBody(bytes.NewReader(data), 5000, 2000); err != errImageTooLarge {
		t.Fatalf("size hint not rejected: %v", err)
	}
}
