package thumbnailrender

import (
	"image/color"
	"testing"
)

func TestParseHappyPath(t *testing.T) {
	snapshot := []byte(`{
		"schema_version": 1,
		"canvas": {"width": 1920, "height": 1080, "background": "#30305a"},
		"objects": [
			{"id": "text-1", "type": "text", "text": "THIS IS A TEST", "x": 420, "y": 520, "width": 900, "height": 160, "scale_x": 1, "scale_y": 1, "rotation": 0, "font_size": 96, "font_weight": 700, "fill": "#ffffff", "visible": true},
			{"id": "rect-1", "type": "rect", "x": 10, "y": 10, "width": 100, "height": 50, "fill": "#ff0000"},
			{"id": "img-1", "type": "image", "media_id": "5b2a3c98-7d4e-4f1a-9b3c-1a2b3c4d5e6f", "x": 0, "y": 0, "width": 320, "height": 180}
		]
	}`)
	scene, err := Parse(snapshot, 1280, 720)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if scene.Width != 1920 || scene.Height != 1080 {
		t.Fatalf("canvas dims: %dx%d, want 1920x1080", scene.Width, scene.Height)
	}
	if scene.Background != (color.RGBA{R: 0x30, G: 0x30, B: 0x5a, A: 255}) {
		t.Fatalf("background: %+v", scene.Background)
	}
	if len(scene.Objects) != 3 {
		t.Fatalf("objects: want 3, got %d", len(scene.Objects))
	}
	text := scene.Objects[0]
	if text.Type != "text" || text.Text != "THIS IS A TEST" || text.FontSize != 96 {
		t.Fatalf("text object: %+v", text)
	}
	if !text.Visible || text.ScaleX != 1 || text.Rotation != 0 {
		t.Fatalf("text defaults: %+v", text)
	}
	img := scene.Objects[2]
	if img.Type != "image" || img.MediaID != "5b2a3c98-7d4e-4f1a-9b3c-1a2b3c4d5e6f" {
		t.Fatalf("image object: %+v", img)
	}
}

func TestParseFallsBackToProjectDimensions(t *testing.T) {
	scene, err := Parse([]byte(`{"canvas":{"background":"#000"},"objects":[]}`), 640, 360)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if scene.Width != 640 || scene.Height != 360 {
		t.Fatalf("fallback dims: %dx%d, want 640x360", scene.Width, scene.Height)
	}
}

func TestParseRejectsInvalidCanvas(t *testing.T) {
	_, err := Parse([]byte(`{"canvas":{"width":0,"height":0},"objects":[]}`), 0, 0)
	if err == nil {
		t.Fatal("want error for zero canvas, got nil")
	}
	_, err = Parse([]byte(`{"canvas":{"width":20000,"height":100},"objects":[]}`), 1280, 720)
	if err == nil {
		t.Fatal("want error for oversized canvas, got nil")
	}
}

func TestParseSkipsUnknownObjectTypes(t *testing.T) {
	snapshot := []byte(`{"canvas":{"width":10,"height":10},"objects":[
		{"id":"a","type":"line","x1":0,"y1":0,"x2":10,"y2":10},
		{"id":"b","type":"rect","x":0,"y":0,"width":5,"height":5,"fill":"#fff"}
	]}`)
	scene, err := Parse(snapshot, 10, 10)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(scene.Objects) != 1 || scene.Objects[0].ID != "b" {
		t.Fatalf("unknown types must be skipped; got %+v", scene.Objects)
	}
}

func TestParseInvalidObjectFillFails(t *testing.T) {
	_, err := Parse([]byte(`{"canvas":{"width":10,"height":10},"objects":[{"id":"a","type":"rect","width":5,"height":5,"fill":"not-a-color"}]}`), 10, 10)
	if err == nil {
		t.Fatal("want error for invalid fill, got nil")
	}
}

func TestParseColorVariants(t *testing.T) {
	cases := []struct {
		in   string
		want color.RGBA
	}{
		{"#f00", color.RGBA{R: 255, A: 255}},
		{"#ff0000", color.RGBA{R: 255, A: 255}},
		{"#ff000080", color.RGBA{R: 255, A: 128}},
		{"rgb(0, 128, 255)", color.RGBA{B: 255, G: 128, A: 255}},
		{"rgba(0, 128, 255, 64)", color.RGBA{B: 255, G: 128, A: 64}},
	}
	for _, tc := range cases {
		got, err := ParseColor(tc.in)
		if err != nil {
			t.Errorf("ParseColor(%q): %v", tc.in, err)
			continue
		}
		if *got != tc.want {
			t.Errorf("ParseColor(%q) = %+v, want %+v", tc.in, *got, tc.want)
		}
	}
	if got, err := ParseColor(""); got != nil || err != nil {
		t.Errorf("empty color: got %v, %v; want nil, nil", got, err)
	}
	if _, err := ParseColor("bogus"); err == nil {
		t.Error("want error for bogus color")
	}
}
