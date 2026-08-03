package thumbnailrender

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"testing"
)

func mustParse(t *testing.T, snapshot string, w, h int) *Scene {
	t.Helper()
	scene, err := Parse([]byte(snapshot), w, h)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return scene
}

func renderSHA(t *testing.T, scene *Scene, contentType string) string {
	t.Helper()
	bytesOut, err := scene.Render(context.Background(), contentType, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	sum := sha256.Sum256(bytesOut)
	return hex.EncodeToString(sum[:])
}

func decodePNG(t *testing.T, data []byte) *image.RGBA {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("png decode: %v", err)
	}
	return toRGBA(img)
}

func decodeJPEG(data []byte) (*image.RGBA, error) {
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return toRGBA(img), nil
}

const simpleScene = `{"canvas":{"width":320,"height":180,"background":"#30305a"},"objects":[]}`

func TestRenderDeterministic(t *testing.T) {
	scene := mustParse(t, `{"canvas":{"width":320,"height":180,"background":"#30305a"},"objects":[
		{"id":"t1","type":"text","text":"HELLO","x":10,"y":10,"font_size":48,"fill":"#ffffff"},
		{"id":"r1","type":"rect","x":40,"y":60,"width":120,"height":60,"fill":"#ff0000","rotation":20}
	]}`, 320, 180)
	a := renderSHA(t, scene, "image/png")
	b := renderSHA(t, scene, "image/png")
	if a != b {
		t.Fatalf("render not deterministic: %s vs %s", a, b)
	}
	// A rotated variant must differ from the unrotated one.
	sceneFlat := mustParse(t, `{"canvas":{"width":320,"height":180,"background":"#30305a"},"objects":[
		{"id":"t1","type":"text","text":"HELLO","x":10,"y":10,"font_size":48,"fill":"#ffffff"},
		{"id":"r1","type":"rect","x":40,"y":60,"width":120,"height":60,"fill":"#ff0000","rotation":0}
	]}`, 320, 180)
	if renderSHA(t, scene, "image/png") == renderSHA(t, sceneFlat, "image/png") {
		t.Fatal("rotated and flat renders must differ")
	}
}

func TestRenderBackgroundPixel(t *testing.T) {
	scene := mustParse(t, simpleScene, 320, 180)
	data, err := scene.Render(context.Background(), "image/png", nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	img := decodePNG(t, data)
	got := img.RGBAAt(0, 0)
	want := color.RGBA{R: 0x30, G: 0x30, B: 0x5a, A: 255}
	if got != want {
		t.Fatalf("background pixel: got %+v, want %+v", got, want)
	}
	if img.Bounds().Dx() != 320 || img.Bounds().Dy() != 180 {
		t.Fatalf("canvas size: %dx%d", img.Bounds().Dx(), img.Bounds().Dy())
	}
}

func TestRenderRectPixel(t *testing.T) {
	scene := mustParse(t, `{"canvas":{"width":100,"height":100,"background":"#000000"},"objects":[
		{"id":"r1","type":"rect","x":10,"y":10,"width":40,"height":30,"fill":"#00ff00"}
	]}`, 100, 100)
	data, err := scene.Render(context.Background(), "image/png", nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	img := decodePNG(t, data)
	if got := img.RGBAAt(20, 20); got != (color.RGBA{G: 255, A: 255}) {
		t.Fatalf("rect interior pixel: got %+v", got)
	}
	if got := img.RGBAAt(5, 5); got != (color.RGBA{A: 255}) {
		t.Fatalf("outside rect pixel: got %+v", got)
	}
}

func TestRenderTextDiffersByContent(t *testing.T) {
	a := mustParse(t, `{"canvas":{"width":200,"height":100,"background":"#000"},"objects":[
		{"id":"t","type":"text","text":"AAAA","x":10,"y":10,"font_size":48,"fill":"#fff"}
	]}`, 200, 100)
	b := mustParse(t, `{"canvas":{"width":200,"height":100,"background":"#000"},"objects":[
		{"id":"t","type":"text","text":"BBBB","x":10,"y":10,"font_size":48,"fill":"#fff"}
	]}`, 200, 100)
	if renderSHA(t, a, "image/png") == renderSHA(t, b, "image/png") {
		t.Fatal("different text must produce different renders")
	}
	// Text must actually draw: the text area must contain non-background pixels.
	data, err := a.Render(context.Background(), "image/png", nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	img := decodePNG(t, data)
	found := false
	for y := 0; y < 40; y++ {
		for x := 0; x < 200; x++ {
			if img.RGBAAt(x, y) != (color.RGBA{A: 255}) {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("text layer produced no visible pixels")
	}
}

func TestRenderImageObject(t *testing.T) {
	// Build a 4x4 red PNG asset.
	asset := image.NewRGBA(image.Rect(0, 0, 4, 4))
	draw.Draw(asset, asset.Bounds(), image.NewUniform(color.RGBA{R: 255, A: 255}), image.Point{}, draw.Src)
	var buf bytes.Buffer
	if err := png.Encode(&buf, asset); err != nil {
		t.Fatal(err)
	}
	assetBytes := buf.Bytes()

	scene := mustParse(t, `{"canvas":{"width":64,"height":64,"background":"#000"},"objects":[
		{"id":"i1","type":"image","media_id":"media-1","x":8,"y":8,"width":32,"height":32}
	]}`, 64, 64)
	resolve := func(_ context.Context, mediaID string) ([]byte, error) {
		if mediaID != "media-1" {
			return nil, ErrMediaNotFound
		}
		return assetBytes, nil
	}
	data, err := scene.Render(context.Background(), "image/png", resolve)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	img := decodePNG(t, data)
	if got := img.RGBAAt(20, 20); got != (color.RGBA{R: 255, A: 255}) {
		t.Fatalf("image interior pixel: got %+v", got)
	}
	if got := img.RGBAAt(2, 2); got != (color.RGBA{A: 255}) {
		t.Fatalf("outside image pixel: got %+v", got)
	}
}

func TestRenderImageMissingResolverFails(t *testing.T) {
	scene := mustParse(t, `{"canvas":{"width":64,"height":64},"objects":[
		{"id":"i1","type":"image","media_id":"media-1","width":32,"height":32}
	]}`, 64, 64)
	if _, err := scene.Render(context.Background(), "image/png", nil); err == nil {
		t.Fatal("want error when image object has no resolver")
	}
}

func TestRenderImageUnresolvedMediaFails(t *testing.T) {
	scene := mustParse(t, `{"canvas":{"width":64,"height":64},"objects":[
		{"id":"i1","type":"image","media_id":"missing","width":32,"height":32}
	]}`, 64, 64)
	resolve := func(_ context.Context, mediaID string) ([]byte, error) { return nil, ErrMediaNotFound }
	if _, err := scene.Render(context.Background(), "image/png", resolve); err == nil {
		t.Fatal("want error when image media is not resolved")
	}
}

func TestRenderJPEGDeterministicAndDecodes(t *testing.T) {
	scene := mustParse(t, `{"canvas":{"width":64,"height":64,"background":"#123456"},"objects":[
		{"id":"r","type":"rect","x":0,"y":0,"width":20,"height":20,"fill":"#ff0000"}
	]}`, 64, 64)
	a := renderSHA(t, scene, "image/jpeg")
	b := renderSHA(t, scene, "image/jpeg")
	if a != b {
		t.Fatalf("jpeg not deterministic: %s vs %s", a, b)
	}
	data, err := scene.Render(context.Background(), "image/jpeg", nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	jpegImg, err := decodeJPEG(data)
	if err != nil {
		t.Fatalf("jpeg decode: %v", err)
	}
	if jpegImg.Bounds().Dx() != 64 || jpegImg.Bounds().Dy() != 64 {
		t.Fatalf("jpeg dims: %dx%d", jpegImg.Bounds().Dx(), jpegImg.Bounds().Dy())
	}
}

func TestRenderTransformsPersist(t *testing.T) {
	// DoD Test 5: x/y/rotation/scale must survive save → render. Two
	// renders of the same transformed object must be identical, and a
	// changed scale must change the output.
	base := `{"canvas":{"width":320,"height":180,"background":"#fff"},"objects":[
		{"id":"r","type":"rect","x":100,"y":80,"width":60,"height":40,"fill":"#ff0000","rotation":20,"scale_x":1.7,"scale_y":1.7}
	]}`
	a := renderSHA(t, mustParse(t, base, 320, 180), "image/png")
	b := renderSHA(t, mustParse(t, base, 320, 180), "image/png")
	if a != b {
		t.Fatal("transformed render not deterministic")
	}
	scaled := `{"canvas":{"width":320,"height":180,"background":"#fff"},"objects":[
		{"id":"r","type":"rect","x":100,"y":80,"width":60,"height":40,"fill":"#ff0000","rotation":20,"scale_x":1.0,"scale_y":1.0}
	]}`
	if a == renderSHA(t, mustParse(t, scaled, 320, 180), "image/png") {
		t.Fatal("scale change must alter the render")
	}
}

func TestRenderClampsHostileObjectSizes(t *testing.T) {
	// A crafted snapshot with an enormous rect, a huge text line and
	// an absurd scale factor must render within the canvas bound
	// instead of allocating unbounded layers (memory DoS guard).
	scene := mustParse(t, `{"canvas":{"width":64,"height":64},"objects":[
		{"id":"r","type":"rect","x":0,"y":0,"width":10000000,"height":10000000,"fill":"#fff"},
		{"id":"t","type":"text","text":"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789","x":0,"y":0,"font_size":4096,"fill":"#fff"},
		{"id":"s","type":"rect","x":0,"y":0,"width":4,"height":4,"fill":"#0f0","scale_x":1000000,"scale_y":1000000}
	]}`, 64, 64)
	scene.Width, scene.Height = 64, 64 // pin canvas; hostile fields are the target
	data, err := scene.Render(context.Background(), "image/png", nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	img := decodePNG(t, data)
	if img.Bounds().Dx() != 64 || img.Bounds().Dy() != 64 {
		t.Fatalf("canvas changed: %dx%d", img.Bounds().Dx(), img.Bounds().Dy())
	}
}

func TestRenderEmptySceneStillEncodes(t *testing.T) {
	scene := mustParse(t, simpleScene, 320, 180)
	data, err := scene.Render(context.Background(), "image/png", nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("empty scene must still produce PNG bytes")
	}
}
