package jpegcheck

import (
	"bytes"
	gofmt "fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// same asserts Validate and image/jpeg.Decode agree on outcome, error text
// and dimensions for one input.
func same(t *testing.T, name string, data []byte) {
	t.Helper()
	img, decErr := jpeg.Decode(bytes.NewReader(data))
	w, h, valErr := Validate(bytes.NewReader(data))
	if (decErr == nil) != (valErr == nil) {
		t.Fatalf("%s: Decode err=%v Validate err=%v", name, decErr, valErr)
	}
	if decErr != nil {
		if decErr.Error() != valErr.Error() {
			t.Fatalf("%s: Decode err=%q Validate err=%q", name, decErr, valErr)
		}
		return
	}
	if img.Bounds().Dx() != w || img.Bounds().Dy() != h {
		t.Fatalf("%s: Decode %dx%d Validate %dx%d", name, img.Bounds().Dx(), img.Bounds().Dy(), w, h)
	}
}

func testFiles(t *testing.T) map[string][]byte {
	t.Helper()
	paths, err := filepath.Glob("testdata/*.jpg")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no testdata: %v", err)
	}
	files := map[string][]byte{}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		files[filepath.Base(p)] = data
	}
	return files
}

func synthetic(t *testing.T) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	sizes := [][2]int{{1, 1}, {7, 9}, {8, 8}, {17, 33}, {128, 128}, {513, 300}, {1086, 1448}}
	for _, sz := range sizes {
		rgb := image.NewRGBA(image.Rect(0, 0, sz[0], sz[1]))
		gray := image.NewGray(image.Rect(0, 0, sz[0], sz[1]))
		for y := 0; y < sz[1]; y++ {
			for x := 0; x < sz[0]; x++ {
				rgb.Set(x, y, color.RGBA{uint8(x * 7), uint8(y * 3), uint8(x ^ y), 255})
				gray.SetGray(x, y, color.Gray{uint8((x * y) & 0xff)})
			}
		}
		for _, q := range []int{10, 75, 100} {
			var b bytes.Buffer
			if err := jpeg.Encode(&b, rgb, &jpeg.Options{Quality: q}); err != nil {
				t.Fatal(err)
			}
			out[fmt("rgb-%dx%d-q%d", sz[0], sz[1], q)] = b.Bytes()
			b = bytes.Buffer{}
			if err := jpeg.Encode(&b, gray, &jpeg.Options{Quality: q}); err != nil {
				t.Fatal(err)
			}
			out[fmt("gray-%dx%d-q%d", sz[0], sz[1], q)] = b.Bytes()
		}
	}
	return out
}

func fmt(format string, a ...any) string { // keeps the test body short
	return gofmt.Sprintf(format, a...)
}

func TestValidateMatchesDecode(t *testing.T) {
	inputs := testFiles(t)
	for k, v := range synthetic(t) {
		inputs[k] = v
	}
	for name, data := range inputs {
		same(t, name, data)
	}
}

func TestValidateMatchesDecodeOnDamagedInputs(t *testing.T) {
	inputs := testFiles(t)
	for k, v := range synthetic(t) {
		inputs[k] = v
	}
	rng := rand.New(rand.NewSource(20261031))
	for name, data := range inputs {
		// Truncations: every prefix of the header region, then a sweep.
		for n := 0; n < len(data) && n < 700; n++ {
			same(t, fmt("%s/trunc%d", name, n), data[:n])
		}
		step := len(data)/61 + 1
		for n := 0; n < len(data); n += step {
			same(t, fmt("%s/trunc%d", name, n), data[:n])
		}
		same(t, name+"/trunc-last", data[:len(data)-1])
		same(t, name+"/trunc-last2", data[:len(data)-2])
		// Single byte flips, including inside entropy-coded data.
		for i := 0; i < 120; i++ {
			pos := rng.Intn(len(data))
			mut := append([]byte(nil), data...)
			mut[pos] ^= byte(1 + rng.Intn(255))
			same(t, fmt("%s/flip%d@%d", name, i, pos), mut)
		}
		// Byte insertions and deletions.
		for i := 0; i < 40; i++ {
			pos := rng.Intn(len(data))
			mut := append(append(append([]byte(nil), data[:pos]...), byte(rng.Intn(256))), data[pos:]...)
			same(t, fmt("%s/ins%d@%d", name, i, pos), mut)
			del := append(append([]byte(nil), data[:pos]...), data[pos+1:]...)
			same(t, fmt("%s/del%d@%d", name, i, pos), del)
		}
		// Extra trailing garbage and a second image appended.
		same(t, name+"/trailing", append(append([]byte(nil), data...), 0xff, 0xd8, 0x00))
		same(t, name+"/double", append(append([]byte(nil), data...), data...))
		// Entropy data removed: SOS header kept, scan bytes dropped.
		if i := bytes.Index(data, []byte{0xff, 0xda}); i >= 0 && i+14 < len(data) {
			same(t, name+"/entropy-empty", append(append([]byte(nil), data[:i+14]...), 0xff, 0xd9))
		}
	}
}

func TestValidateRejectsNonJPEG(t *testing.T) {
	for name, data := range map[string][]byte{
		"empty":   {},
		"soi":     {0xff, 0xd8},
		"soi-eoi": {0xff, 0xd8, 0xff, 0xd9},
		"png":     {0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0},
		"text":    []byte("not a jpeg at all"),
		"zeros":   make([]byte, 4096),
	} {
		same(t, name, data)
	}
}

func BenchmarkValidateUpload(b *testing.B) {
	data, err := os.ReadFile("testdata/7adfcb52-ce65-4f66-927f-9e6211a547df.jpg")
	if err != nil {
		b.Skip(err)
	}
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		if _, _, err := Validate(bytes.NewReader(data)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodeUpload(b *testing.B) {
	data, err := os.ReadFile("testdata/7adfcb52-ce65-4f66-927f-9e6211a547df.jpg")
	if err != nil {
		b.Skip(err)
	}
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
			b.Fatal(err)
		}
	}
}
