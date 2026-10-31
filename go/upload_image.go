package isureal

import (
	"bytes"
	"errors"
	"image/jpeg"
	"io"
	"net/http"
)

// Uploaded images are validated without decoding pixels: the JPEG header gives
// the dimensions and a marker walk checks the structure up to EOI. Bodies are
// read with a hard cap so oversized garbage never fills memory.

const postImageMaxBytes = 16 << 20
const smallImageMaxBytes = 2 << 20

var errImageTooLarge = errors.New("image body too large")
var errInvalidJPEG = errors.New("invalid jpeg")

// readUploadBody reads at most maxBytes; a larger body is an error.
func readUploadBody(r io.Reader, sizeHint int64, maxBytes int64) ([]byte, error) {
	if sizeHint > maxBytes {
		return nil, errImageTooLarge
	}
	var buf bytes.Buffer
	if sizeHint > 0 {
		buf.Grow(int(sizeHint) + 1)
	}
	n, err := buf.ReadFrom(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if n > maxBytes {
		return nil, errImageTooLarge
	}
	return buf.Bytes(), nil
}

// validateJPEG returns the image bounds after checking the header and the
// marker structure. With fastJPEG disabled it falls back to a full decode.
func validateJPEG(data []byte, fastJPEG bool) (width, height int, err error) {
	if !fastJPEG {
		img, err := jpeg.Decode(bytes.NewReader(data))
		if err != nil {
			return 0, 0, errInvalidJPEG
		}
		return img.Bounds().Dx(), img.Bounds().Dy(), nil
	}
	config, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0, errInvalidJPEG
	}
	if err := checkJPEGStructure(data); err != nil {
		return 0, 0, errInvalidJPEG
	}
	return config.Width, config.Height, nil
}

// checkJPEGStructure walks the marker segments from SOI to EOI, skipping
// entropy-coded data, so truncated or non-JPEG bodies are rejected.
func checkJPEGStructure(b []byte) error {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return errInvalidJPEG
	}
	i := 2
	sawSOS := false
	for i < len(b) {
		if b[i] != 0xFF {
			return errInvalidJPEG
		}
		if i+1 >= len(b) {
			return errInvalidJPEG
		}
		marker := b[i+1]
		switch {
		case marker == 0xFF: // fill byte
			i++
			continue
		case marker == 0xD9: // EOI
			if !sawSOS {
				return errInvalidJPEG
			}
			return nil
		case marker == 0xD8 || marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7): // standalone
			i += 2
			continue
		}
		if i+4 > len(b) {
			return errInvalidJPEG
		}
		segLen := int(b[i+2])<<8 | int(b[i+3])
		if segLen < 2 || i+2+segLen > len(b) {
			return errInvalidJPEG
		}
		i += 2 + segLen
		if marker == 0xDA { // SOS: skip entropy-coded data up to the next real marker
			sawSOS = true
			for i < len(b) {
				if b[i] == 0xFF && i+1 < len(b) && b[i+1] != 0x00 && b[i+1] != 0xFF && !(b[i+1] >= 0xD0 && b[i+1] <= 0xD7) {
					break
				}
				i++
			}
		}
	}
	return errInvalidJPEG
}

func uploadLimitError(err error) bool {
	var maxErr *http.MaxBytesError
	return errors.Is(err, errImageTooLarge) || errors.As(err, &maxErr)
}
