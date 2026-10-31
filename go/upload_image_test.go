package isureal

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

func encodeJPEG(t *testing.T, w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), uint8(x ^ y), 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDecodeJPEGWithinMatchesReference(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		max  int
	}{
		{"valid within", encodeJPEG(t, 300, 200), 512},
		{"valid over", encodeJPEG(t, 600, 200), 512},
		{"truncated", encodeJPEG(t, 300, 200)[:900], 512},
		{"garbage", bytes.Repeat([]byte{0x41}, 4000), 512},
		{"empty", nil, 512},
	}
	for _, tc := range cases {
		refImg, refErr := jpeg.Decode(bytes.NewReader(tc.data))
		refFail := refErr != nil || refImg.Bounds().Dx() > tc.max || refImg.Bounds().Dy() > tc.max
		_, err := decodeJPEGWithin(tc.data, tc.max)
		if (err != nil) != refFail {
			t.Fatalf("%s: reference fail=%v ours err=%v", tc.name, refFail, err)
		}
	}
}

func TestReadUpload(t *testing.T) {
	data := bytes.Repeat([]byte{7}, 123456)
	for _, hint := range []int64{-1, 0, int64(len(data)), 10} {
		got, err := readUpload(bytes.NewReader(data), hint)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("hint %d: err=%v len=%d", hint, err, len(got))
		}
	}
}
