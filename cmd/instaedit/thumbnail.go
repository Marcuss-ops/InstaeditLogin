package main

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"os"

	_ "golang.org/x/image/webp"
	_ "image/png"

	xdraw "golang.org/x/image/draw"
)

// YouTube thumbnails are 16:9. The cover is normalized to 1920x1080 and
// re-encoded as JPEG under ~1.9 MB (YouTube enforces a 2 MB limit when
// the publish path downloads the thumbnail).
const (
	thumbWidth       = 1920
	thumbHeight      = 1080
	maxThumbBytes    = 2 * 1024 * 1024
	targetThumbBytes = 1_900_000
)

// normalizeThumbnail decodes src, crops to 16:9, scales to 1920x1080
// and encodes JPEG, lowering quality until it clears ~1.9 MB. Returns
// the encoded bytes.
func normalizeThumbnail(srcPath string) ([]byte, error) {
	f, err := os.Open(srcPath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", srcPath, err)
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", srcPath, err)
	}

	cropped := cropToAspect(img, thumbWidth, thumbHeight)

	dst := image.NewRGBA(image.Rect(0, 0, thumbWidth, thumbHeight))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), cropped, cropped.Bounds(), draw.Src, nil)

	for quality := 92; quality >= 60; quality -= 5 {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: quality}); err != nil {
			return nil, fmt.Errorf("encode jpeg: %w", err)
		}
		if buf.Len() < targetThumbBytes {
			return buf.Bytes(), nil
		}
	}

	return nil, fmt.Errorf("could not compress thumbnail under %d bytes", targetThumbBytes)
}

// cropToAspect returns the largest centered crop of img that matches
// the tw:th aspect ratio.
func cropToAspect(img image.Image, tw, th int) image.Image {
	src := img.Bounds()
	sw, sh := src.Dx(), src.Dy()
	if sw <= 0 || sh <= 0 {
		return img
	}

	targetRatio := float64(tw) / float64(th)
	srcRatio := float64(sw) / float64(sh)

	var rect image.Rectangle
	if srcRatio > targetRatio {
		// Source is wider than target: crop the sides.
		cw := int(float64(sh) * targetRatio)
		x0 := (sw - cw) / 2
		rect = image.Rect(src.Min.X+x0, src.Min.Y, src.Min.X+x0+cw, src.Max.Y)
	} else {
		// Source is taller than target: crop the top/bottom.
		ch := int(float64(sw) / targetRatio)
		y0 := (sh - ch) / 2
		rect = image.Rect(src.Min.X, src.Min.Y+y0, src.Max.X, src.Min.Y+y0+ch)
	}

	if sub, ok := img.(interface {
		SubImage(r image.Rectangle) image.Image
	}); ok {
		return sub.SubImage(rect)
	}

	out := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	draw.Draw(out, out.Bounds(), img, rect.Min, draw.Src)
	return out
}
