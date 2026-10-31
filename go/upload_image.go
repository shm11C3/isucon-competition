package isureal

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"io"
)

var errImageTooLarge = errors.New("image dimensions exceed the limit")

// readUpload reads the whole body into a buffer sized from the hint so large
// uploads are not grown by repeated doubling.
func readUpload(r io.Reader, sizeHint int64) ([]byte, error) {
	var buf bytes.Buffer
	if sizeHint > 0 {
		buf.Grow(int(sizeHint) + 512)
	}
	if _, err := buf.ReadFrom(r); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// decodeJPEGWithin decodes a JPEG exactly like jpeg.Decode followed by a
// bounds check, except that images whose header already declares dimensions
// over the limit are rejected before the pixel decode. Both paths yield the
// same error for the caller, so responses are unchanged.
func decodeJPEGWithin(data []byte, maxDim int) (image.Image, error) {
	config, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if config.Width > maxDim || config.Height > maxDim {
		return nil, errImageTooLarge
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if img.Bounds().Dx() > maxDim || img.Bounds().Dy() > maxDim {
		return nil, errImageTooLarge
	}
	return img, nil
}
