package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeThumbnail_WideSourceProducesJPEG1920x1080(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 3200, 1200))
	fillGradient(source)

	output := normalizeTestImage(t, source)
	assertNormalizedThumbnail(t, output)
}

func TestNormalizeThumbnail_TallSourceProducesJPEG1920x1080(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 1200, 3200))
	fillGradient(source)

	output := normalizeTestImage(t, source)
	assertNormalizedThumbnail(t, output)
}

func normalizeTestImage(t *testing.T, source image.Image) []byte {
	t.Helper()

	sourcePath := filepath.Join(t.TempDir(), "source.png")
	file, err := os.Create(sourcePath)
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	if err := png.Encode(file, source); err != nil {
		_ = file.Close()
		t.Fatalf("encode source: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close source: %v", err)
	}

	output, err := normalizeThumbnail(sourcePath)
	if err != nil {
		t.Fatalf("normalizeThumbnail() error = %v", err)
	}
	return output
}

func assertNormalizedThumbnail(t *testing.T, output []byte) {
	t.Helper()

	if len(output) >= targetThumbBytes {
		t.Fatalf("thumbnail size = %d bytes, want < %d", len(output), targetThumbBytes)
	}

	decoded, format, err := image.Decode(bytes.NewReader(output))
	if err != nil {
		t.Fatalf("decode normalized thumbnail: %v", err)
	}
	if format != "jpeg" {
		t.Errorf("format = %q, want jpeg", format)
	}
	if got := decoded.Bounds().Size(); got != (image.Point{X: thumbWidth, Y: thumbHeight}) {
		t.Errorf("dimensions = %v, want %dx%d", got, thumbWidth, thumbHeight)
	}
}

func fillGradient(img *image.RGBA) {
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(x * 255 / bounds.Dx()),
				G: uint8(y * 255 / bounds.Dy()),
				B: uint8((x + y) * 255 / (bounds.Dx() + bounds.Dy())),
				A: 255,
			})
		}
	}
}
