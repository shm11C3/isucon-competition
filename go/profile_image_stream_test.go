package isureal

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"testing"
)

func TestReadAndDecodeProfileImagePreservesValidBody(t *testing.T) {
	encoded := profileSampleJPEG(t, 32, 24)
	body := append(append([]byte(nil), encoded...), 0, 1, 2, 3)
	data, img, err := readAndDecodeProfileImage(&profileShortReader{reader: bytes.NewReader(body), maxChunk: 3})
	if err != nil {
		t.Fatal(err)
	}
	if got := img.Bounds(); got != image.Rect(0, 0, 32, 24) {
		t.Fatalf("bounds = %v", got)
	}
	if !bytes.Equal(data, body) {
		t.Fatalf("stored body changed: got %d bytes, want %d", len(data), len(body))
	}
}

func TestReadAndDecodeProfileImageRejectsInvalidJPEGWithoutDraining(t *testing.T) {
	valid := profileSampleJPEG(t, 8, 8)
	withoutEntropy := profileJPEGWithoutEntropy(t, valid)
	for _, input := range [][]byte{nil, []byte("garbage"), withoutEntropy} {
		if _, _, err := readAndDecodeProfileImage(bytes.NewReader(input)); !errors.Is(err, errInvalidProfileJPEG) {
			t.Errorf("invalid JPEG returned %v", err)
		}
	}
	reader := &profileLargeInvalidReader{remaining: 32 << 20}
	if _, _, err := readAndDecodeProfileImage(reader); !errors.Is(err, errInvalidProfileJPEG) {
		t.Fatalf("large invalid body returned %v", err)
	}
	if reader.reads >= 32<<20 {
		t.Fatalf("invalid body was drained: read %d of %d bytes", reader.reads, 32<<20)
	}
}

func TestReadAndDecodeProfileImageAcceptsLargeJPEGMetadata(t *testing.T) {
	encoded := profileSampleJPEG(t, 8, 8)
	var body bytes.Buffer
	body.Write(encoded[:2])
	segment := make([]byte, 65537)
	segment[0], segment[1], segment[2], segment[3] = 0xff, 0xef, 0xff, 0xff
	for i := 0; i < 32; i++ {
		body.Write(segment)
	}
	body.Write(encoded[2:])
	if body.Len() <= 2<<20 {
		t.Fatalf("fixture size %d does not exceed 2MiB", body.Len())
	}
	data, img, err := readAndDecodeProfileImage(bytes.NewReader(body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds() != image.Rect(0, 0, 8, 8) || !bytes.Equal(data, body.Bytes()) {
		t.Fatalf("large metadata image changed or has unexpected bounds: %v, %d/%d bytes", img.Bounds(), len(data), body.Len())
	}
}

func TestReadAndDecodeProfileImagePreservesReaderError(t *testing.T) {
	readErr := errors.New("body read failed")
	body := &profileErrorReader{data: profileSampleJPEG(t, 8, 8), err: readErr}
	if _, _, err := readAndDecodeProfileImage(body); !errors.Is(err, readErr) {
		t.Fatalf("reader error = %v, want %v", err, readErr)
	}
}

func profileSampleJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 17), uint8(y * 23), uint8(x ^ y), 255})
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func profileJPEGWithoutEntropy(t *testing.T, encoded []byte) []byte {
	t.Helper()
	for i := 2; i+4 <= len(encoded); {
		if encoded[i] != 0xff {
			t.Fatalf("unexpected byte before SOS at %d", i)
		}
		marker := encoded[i+1]
		if marker == 0xda {
			segLen := int(encoded[i+2])<<8 | int(encoded[i+3])
			entropyStart := i + 2 + segLen
			return append(append([]byte(nil), encoded[:entropyStart]...), 0xff, 0xd9)
		}
		if marker == 0xd8 || marker == 0x01 || marker >= 0xd0 && marker <= 0xd7 {
			i += 2
			continue
		}
		segLen := int(encoded[i+2])<<8 | int(encoded[i+3])
		i += 2 + segLen
	}
	t.Fatal("JPEG has no SOS marker")
	return nil
}

type profileShortReader struct {
	reader   io.Reader
	maxChunk int
}

func (reader *profileShortReader) Read(p []byte) (int, error) {
	if len(p) > reader.maxChunk {
		p = p[:reader.maxChunk]
	}
	return reader.reader.Read(p)
}

type profileLargeInvalidReader struct {
	reads     int64
	remaining int64
}

func (reader *profileLargeInvalidReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if reader.remaining == 0 {
		return 0, io.EOF
	}
	reader.reads++
	reader.remaining--
	p[0] = 0
	return 1, nil
}

type profileErrorReader struct {
	data []byte
	err  error
}

func (reader *profileErrorReader) Read(p []byte) (int, error) {
	if len(reader.data) == 0 {
		return 0, reader.err
	}
	n := copy(p, reader.data)
	reader.data = reader.data[n:]
	return n, nil
}

func BenchmarkProfileImageInvalidBody(b *testing.B) {
	const bodySize = 32 << 20
	body := make([]byte, bodySize)
	b.Run("read-all-first", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			data, err := io.ReadAll(bytes.NewReader(body))
			if err != nil || len(data) != bodySize {
				b.Fatalf("read all: len=%d err=%v", len(data), err)
			}
			_, _ = jpeg.Decode(bytes.NewReader(data))
		}
	})
	b.Run("decode-first", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _, err := readAndDecodeProfileImage(bytes.NewReader(body))
			if !errors.Is(err, errInvalidProfileJPEG) {
				b.Fatalf("decode first: %v", err)
			}
		}
	})
}
