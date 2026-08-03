// Package thumbnailrender implements the canonical, deterministic
// renderer for ThumbnailProject snapshots.
//
// Responsibilities (certified by the Dark Editor Definition of Done):
//   - read thumbnail_project_revisions.snapshot_json;
//   - produce a deterministic PNG/JPEG that is byte-identical for the
//     same snapshot (hash-verifiable preview == export);
//   - stamp a stable RendererVersion on every export so preview and
//     export always share the same renderer lineage.
//
// The renderer is intentionally dependency-light and pixel-exact: it
// uses nearest-neighbour scaling and deterministic integer rounding so
// the same snapshot always yields the same bytes on the same binary,
// regardless of when or where it runs. It renders solid backgrounds,
// rectangles, text (bundled bitmap face scaled to font_size) and image
// objects resolved through an injected resolver.
package thumbnailrender

import (
	"encoding/json"
	"fmt"
	"image/color"
	"strconv"
	"strings"
	"unicode/utf8"
)

// RendererVersion is stamped on every export produced by this renderer.
// Preview and export both flow through this same renderer, so they
// always share the same renderer_version and can be compared
// byte-for-byte (DoD: "renderer_version uguale").
const RendererVersion = "go-canvas-v1"

// maxCanvasDimension mirrors the database canvas/export bounds check
// (1..16384) enforced by migration 094. The renderer refuses to
// allocate beyond it to avoid unbounded memory use on hostile input.
const maxCanvasDimension = 16384

// Scene is a parsed snapshot ready for rendering.
type Scene struct {
	SchemaVersion int
	Width         int
	Height        int
	Background    color.RGBA
	Objects       []Object
}

// Object is one drawable element of a scene. Fields map 1:1 to the
// editor snapshot object schema (schema_version 1). Unknown extra
// fields are ignored so forward-compatible editor objects keep
// rendering.
type Object struct {
	ID       string
	Type     string
	X        float64
	Y        float64
	Width    float64
	Height   float64
	ScaleX   float64
	ScaleY   float64
	Rotation float64 // degrees, clockwise (screen space, y-down)
	Visible  bool
	Fill     color.RGBA
	Radius   float64 // rounded-corner radius for rect objects (0 = sharp)
	Text     string
	FontSize float64
	MediaID  string
}

// ErrMediaNotFound is returned by the Render resolver when an image
// object references a media_id that was not resolved up front.
var ErrMediaNotFound = fmt.Errorf("thumbnail render: media asset not resolved")

// canvasObject is the raw JSON shape of one object entry. It is decoded
// with pointers so missing fields keep their zero values and defaults
// are applied after decode.
type canvasObject struct {
	ID       string  `json:"id"`
	Type     string  `json:"type"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Width    float64 `json:"width"`
	Height   float64 `json:"height"`
	ScaleX   float64 `json:"scale_x"`
	ScaleY   float64 `json:"scale_y"`
	Rotation float64 `json:"rotation"`
	Visible  *bool   `json:"visible"`
	Fill     string  `json:"fill"`
	Radius   float64 `json:"radius"`
	Text     string  `json:"text"`
	FontSize float64 `json:"font_size"`
	MediaID  string  `json:"media_id"`
}

// Parse decodes a revision snapshot into a renderable Scene. Width and
// height fall back to the snapshot canvas values, then to the supplied
// fallback (the project's canvas dimensions) when absent.
func Parse(snapshotJSON []byte, fallbackWidth, fallbackHeight int) (*Scene, error) {
	var raw struct {
		SchemaVersion int `json:"schema_version"`
		Canvas        struct {
			Width      int    `json:"width"`
			Height     int    `json:"height"`
			Background string `json:"background"`
		} `json:"canvas"`
		Objects []json.RawMessage `json:"objects"`
	}
	if err := json.Unmarshal(snapshotJSON, &raw); err != nil {
		return nil, fmt.Errorf("thumbnail render: parse snapshot: %w", err)
	}

	s := &Scene{
		SchemaVersion: raw.SchemaVersion,
		Width:         fallbackWidth,
		Height:        fallbackHeight,
		Background:    color.RGBA{R: 0, G: 0, B: 0, A: 255},
	}
	if raw.Canvas.Width > 0 {
		s.Width = raw.Canvas.Width
	}
	if raw.Canvas.Height > 0 {
		s.Height = raw.Canvas.Height
	}
	if s.Width <= 0 || s.Width > maxCanvasDimension || s.Height <= 0 || s.Height > maxCanvasDimension {
		return nil, fmt.Errorf("thumbnail render: invalid canvas dimensions %dx%d", s.Width, s.Height)
	}
	if bg, err := ParseColor(raw.Canvas.Background); err == nil && bg != nil {
		s.Background = *bg
	}

	for _, msg := range raw.Objects {
		obj, err := parseObject(msg)
		if err != nil {
			return nil, err
		}
		if obj != nil {
			s.Objects = append(s.Objects, *obj)
		}
	}
	return s, nil
}

// parseObject decodes a single object entry. Unknown object types are
// skipped (nil, no error) so editor objects this renderer does not yet
// support cannot break the whole render.
func parseObject(raw json.RawMessage) (*Object, error) {
	var c canvasObject
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("thumbnail render: parse object: %w", err)
	}
	o := &Object{
		ID:       c.ID,
		Type:     strings.ToLower(strings.TrimSpace(c.Type)),
		X:        c.X,
		Y:        c.Y,
		Width:    c.Width,
		Height:   c.Height,
		ScaleX:   c.ScaleX,
		ScaleY:   c.ScaleY,
		Rotation: c.Rotation,
		Visible:  c.Visible == nil || *c.Visible,
		Fill:     color.RGBA{R: 0, G: 0, B: 0, A: 255},
		Radius:   c.Radius,
		Text:     c.Text,
		FontSize: c.FontSize,
		MediaID:  strings.TrimSpace(c.MediaID),
	}
	if o.ScaleX == 0 {
		o.ScaleX = 1
	}
	if o.ScaleY == 0 {
		o.ScaleY = 1
	}
	// Defensive bounds on untrusted snapshot input: object dimensions
	// are clamped to the canvas bound and scale factors are capped so
	// the rasterizer can never allocate beyond maxCanvasDimension.
	if o.Width > maxCanvasDimension {
		o.Width = maxCanvasDimension
	}
	if o.Height > maxCanvasDimension {
		o.Height = maxCanvasDimension
	}
	if o.ScaleX > 100 {
		o.ScaleX = 100
	}
	if o.ScaleY > 100 {
		o.ScaleY = 100
	}
	if o.FontSize > 4096 {
		o.FontSize = 4096
	}
	if c.Fill != "" {
		fill, err := ParseColor(c.Fill)
		if err != nil {
			return nil, fmt.Errorf("thumbnail render: object %q fill: %w", o.ID, err)
		}
		if fill != nil {
			o.Fill = *fill
		}
	}
	if o.FontSize <= 0 {
		o.FontSize = 48 // editor default when the object omits font_size
	}

	switch o.Type {
	case "rect", "text", "image":
		return o, nil
	case "":
		return nil, fmt.Errorf("thumbnail render: object has no type")
	default:
		// Unknown/future object types render as no-ops. This keeps the
		// canonical renderer forward-compatible with editor shapes it
		// does not yet rasterize (lines, arrows, shapes, ...).
		return nil, nil
	}
}

// ParseColor converts a CSS-ish color string to opaque/premultiplied
// color.RGBA. Accepted forms:
//
//	#RGB, #RGBA, #RRGGBB, #RRGGBBAA, rgb(r,g,b), rgba(r,g,b,a)
//
// Returns (nil, nil) for the empty string and an error for anything
// unparseable so callers can default.
func ParseColor(s string) (*color.RGBA, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if strings.HasPrefix(s, "#") {
		return parseHexColor(s[1:])
	}
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "rgba(") && strings.HasSuffix(lower, ")") {
		return parseFuncColor(s[len("rgba("):len(s)-1], 4)
	}
	if strings.HasPrefix(lower, "rgb(") && strings.HasSuffix(lower, ")") {
		return parseFuncColor(s[len("rgb("):len(s)-1], 3)
	}
	return nil, fmt.Errorf("unsupported color %q", s)
}

func parseHexColor(hex string) (*color.RGBA, error) {
	switch len(hex) {
	case 3, 4:
		expanded := make([]byte, 0, len(hex)*2)
		for i := 0; i < len(hex); i++ {
			expanded = append(expanded, hex[i], hex[i])
		}
		hex = string(expanded)
	case 6, 8:
	default:
		return nil, fmt.Errorf("unsupported hex color %q", "#"+hex)
	}
	v, err := strconv.ParseUint(hex, 16, 64)
	if err != nil {
		return nil, fmt.Errorf("unsupported hex color %q: %w", "#"+hex, err)
	}
	rgba := color.RGBA{
		R: uint8(v >> (8 * (len(hex)/2 - 1))),
		G: uint8(v >> (8 * (len(hex)/2 - 2))),
		B: uint8(v >> (8 * (len(hex)/2 - 3))),
		A: 255,
	}
	if len(hex) == 8 {
		rgba.A = uint8(v)
	}
	return &rgba, nil
}

func parseFuncColor(body string, parts int) (*color.RGBA, error) {
	fields := strings.Split(body, ",")
	if len(fields) != parts {
		return nil, fmt.Errorf("unsupported color function %q", body)
	}
	values := make([]int, 0, parts)
	for _, f := range fields {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil {
			return nil, fmt.Errorf("unsupported color function %q: %w", body, err)
		}
		values = append(values, n)
	}
	for i := 0; i < 3; i++ {
		if values[i] < 0 || values[i] > 255 {
			return nil, fmt.Errorf("unsupported color function %q: channel out of range", body)
		}
	}
	c := color.RGBA{R: uint8(values[0]), G: uint8(values[1]), B: uint8(values[2]), A: 255}
	if parts == 4 {
		if values[3] < 0 || values[3] > 255 {
			return nil, fmt.Errorf("unsupported color function %q: alpha out of range", body)
		}
		c.A = uint8(values[3])
	}
	return &c, nil
}

// textLayerWidth approximates the 1x bitmap width of a text block.
// Used only to size the offscreen layer before font scaling.
func textLayerWidth(s string) int {
	max := 0
	for _, line := range strings.Split(s, "\n") {
		w := utf8.RuneCountInString(line) * 7 // basicfont.Face7x13 advance
		if w > max {
			max = w
		}
	}
	return max
}
