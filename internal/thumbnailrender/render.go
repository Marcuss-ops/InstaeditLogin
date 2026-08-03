package thumbnailrender

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"

	// Register the "webp" decoder with image.Decode so image objects can
	// reference WebP assets from the Media Library.
	_ "golang.org/x/image/webp"
)

// basicFontHeight is the pixel height of basicfont.Face7x13, the
// canonical baseline for text scaling (font_size is expressed in this
// unit so font_size 13 renders at 1x).
const basicFontHeight = 13

// jpegQuality is fixed so JPEG exports are deterministic byte-for-byte
// for the same scene (Go's stdlib jpeg encoder is deterministic for a
// fixed quality).
const jpegQuality = 92

// ImageResolver fetches the raw bytes of a media asset referenced by an
// image object. It is called only for objects with type "image". The
// caller (API layer) resolves every referenced media up front and
// returns ErrMediaNotFound when an id was not resolved.
type ImageResolver func(ctx context.Context, mediaID string) ([]byte, error)

// Render rasterizes the scene to the requested content type and returns
// the encoded bytes. The output is deterministic: the same scene always
// produces identical bytes. resolve may be nil when the scene contains
// no image objects.
func (s *Scene) Render(ctx context.Context, contentType string, resolve ImageResolver) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, s.Width, s.Height))
	draw.Draw(img, img.Bounds(), image.NewUniform(s.Background), image.Point{}, draw.Src)

	for i := range s.Objects {
		o := &s.Objects[i]
		if !o.Visible {
			continue
		}
		layer, err := s.renderObject(ctx, o, resolve)
		if err != nil {
			return nil, err
		}
		if layer == nil {
			continue
		}
		composite(img, layer, o)
	}

	var buf bytes.Buffer
	switch contentType {
	case "image/png":
		if err := png.Encode(&buf, img); err != nil {
			return nil, fmt.Errorf("thumbnail render: encode png: %w", err)
		}
	case "image/jpeg":
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
			return nil, fmt.Errorf("thumbnail render: encode jpeg: %w", err)
		}
	default:
		return nil, fmt.Errorf("thumbnail render: unsupported content type %q", contentType)
	}
	return buf.Bytes(), nil
}

// renderObject rasterizes a single object into its own layer image
// (intrinsic size, before scale/rotate/translate).
func (s *Scene) renderObject(ctx context.Context, o *Object, resolve ImageResolver) (*image.RGBA, error) {
	switch o.Type {
	case "rect":
		return renderRect(o), nil
	case "text":
		return renderText(o), nil
	case "image":
		return renderImage(ctx, o, resolve)
	default:
		return nil, nil
	}
}

// renderRect draws a solid rectangle (optionally rounded) into a layer.
// Layer dimensions are clamped to the canvas bound so a crafted
// snapshot cannot force unbounded allocation.
func renderRect(o *Object) *image.RGBA {
	w := clampDim(o.Width)
	h := clampDim(o.Height)
	if w <= 0 || h <= 0 {
		return nil
	}
	layer := image.NewRGBA(image.Rect(0, 0, w, h))
	radius := int(math.Round(o.Radius))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if radius > 0 && !insideRoundedRect(x, y, w, h, radius) {
				continue
			}
			layer.SetRGBA(x, y, o.Fill)
		}
	}
	return layer
}

// insideRoundedRect reports whether (x,y) lies within the rect's
// rounded-corner interior. Corner exclusion uses the circle test against
// the four corner centers.
func insideRoundedRect(x, y, w, h, r int) bool {
	if r*2 > w {
		r = w / 2
	}
	if r*2 > h {
		r = h / 2
	}
	if r <= 0 {
		return true
	}
	// Corners: (r-1, r-1), (w-r, r-1), (r-1, h-r), (w-r, h-r).
	dx, dy := 0, 0
	switch {
	case x < r && y < r:
		dx, dy = x-(r-1), y-(r-1)
	case x >= w-r && y < r:
		dx, dy = x-(w-r), y-(r-1)
	case x < r && y >= h-r:
		dx, dy = x-(r-1), y-(h-r)
	case x >= w-r && y >= h-r:
		dx, dy = x-(w-r), y-(h-r)
	default:
		return true
	}
	return dx*dx+dy*dy <= r*r
}

// renderText rasterizes the text with the bundled 7x13 bitmap face
// scaled to font_size, left-aligned, multi-line aware.
func renderText(o *Object) *image.RGBA {
	lines := strings.Split(o.Text, "\n")
	layerW := textLayerWidth(o.Text)
	layerH := len(lines) * basicFontHeight
	if layerW <= 0 || layerH <= 0 {
		return nil
	}
	// Clamp the 1x bitmap layer too: a crafted multi-MB text line must
	// not allocate beyond the canvas bound.
	if layerW > maxCanvasDimension {
		layerW = maxCanvasDimension
	}
	if layerH > maxCanvasDimension {
		layerH = maxCanvasDimension
	}
	base := image.NewRGBA(image.Rect(0, 0, layerW, layerH))
	d := &font.Drawer{
		Dst:  base,
		Src:  image.NewUniform(o.Fill),
		Face: basicfont.Face7x13,
		Dot:  fixed.P(0, basicFontHeight),
	}
	for _, line := range lines {
		d.DrawString(line)
		d.Dot = fixed.P(0, d.Dot.Y.Floor()+basicFontHeight)
	}
	// Scale the 1x bitmap layer to the requested font size. Clamp to the
	// canvas bound so absurd font sizes cannot allocate unbounded layers.
	scale := o.FontSize / basicFontHeight
	sw := int(math.Round(float64(layerW) * scale))
	sh := int(math.Round(float64(layerH) * scale))
	if sw > maxCanvasDimension {
		sw = maxCanvasDimension
	}
	if sh > maxCanvasDimension {
		sh = maxCanvasDimension
	}
	return scaleImage(base, sw, sh)
}

// renderImage decodes the referenced media asset and scales it into a
// layer of the object's width x height.
func renderImage(ctx context.Context, o *Object, resolve ImageResolver) (*image.RGBA, error) {
	if o.MediaID == "" {
		return nil, fmt.Errorf("thumbnail render: image object %q has no media_id", o.ID)
	}
	if resolve == nil {
		return nil, fmt.Errorf("thumbnail render: image object %q cannot be resolved (no resolver)", o.ID)
	}
	data, err := resolve(ctx, o.MediaID)
	if err != nil {
		if err == ErrMediaNotFound {
			return nil, fmt.Errorf("thumbnail render: image object %q: %w", o.ID, ErrMediaNotFound)
		}
		return nil, fmt.Errorf("thumbnail render: image object %q: %w", o.ID, err)
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("thumbnail render: image object %q: decode asset: %w", o.ID, err)
	}
	bounds := src.Bounds()
	w := int(math.Round(o.Width))
	h := int(math.Round(o.Height))
	if w <= 0 {
		w = bounds.Dx()
	}
	if h <= 0 {
		h = bounds.Dy()
	}
	if w <= 0 || h <= 0 {
		return nil, nil
	}
	if w > maxCanvasDimension {
		w = maxCanvasDimension
	}
	if h > maxCanvasDimension {
		h = maxCanvasDimension
	}
	return scaleImage(toRGBA(src), w, h), nil
}

// toRGBA converts any image.Image into *image.RGBA so scaling and
// compositing have a single pixel representation.
func toRGBA(src image.Image) *image.RGBA {
	if rgba, ok := src.(*image.RGBA); ok {
		return rgba
	}
	dst := image.NewRGBA(src.Bounds())
	draw.Draw(dst, dst.Bounds(), src, src.Bounds().Min, draw.Src)
	return dst
}

// scaleImage resizes src to w x h with nearest-neighbour sampling. The
// integer division makes scaling deterministic across runs.
func scaleImage(src *image.RGBA, w, h int) *image.RGBA {
	if w <= 0 || h <= 0 {
		return nil
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	if sw <= 0 || sh <= 0 {
		return dst
	}
	if sw == w && sh == h {
		draw.Draw(dst, dst.Bounds(), src, image.Point{}, draw.Src)
		return dst
	}
	sx0, sy0 := src.Bounds().Min.X, src.Bounds().Min.Y
	for dy := 0; dy < h; dy++ {
		sy := sy0 + (dy*sh)/h
		for dx := 0; dx < w; dx++ {
			sx := sx0 + (dx*sw)/w
			dst.SetRGBA(dx, dy, src.RGBAAt(sx, sy))
		}
	}
	return dst
}

// composite places the object's layer onto the canvas applying
// scale_x/scale_y, rotation (degrees, clockwise) and translation to
// (x, y). Rotation uses inverse mapping with rounded source coords so
// output is deterministic.
func composite(dst *image.RGBA, layer *image.RGBA, o *Object) {
	// Clamp the scaled layer to the canvas bound: an absurd scale_x/
	// scale_y in a crafted snapshot must not trigger unbounded
	// allocation inside scaleImage.
	scaledW := clampDim(float64(layer.Bounds().Dx()) * o.ScaleX)
	scaledH := clampDim(float64(layer.Bounds().Dy()) * o.ScaleY)
	if scaledW <= 0 || scaledH <= 0 {
		return
	}
	src := scaleImage(layer, scaledW, scaledH)
	x := int(math.Round(o.X))
	y := int(math.Round(o.Y))

	if o.Rotation == 0 {
		draw.Draw(dst, image.Rect(x, y, x+scaledW, y+scaledH).Intersect(dst.Bounds()), src, image.Point{}, draw.Over)
		return
	}

	rad := o.Rotation * math.Pi / 180
	cos, sin := math.Cos(rad), math.Sin(rad)
	hw, hh := float64(scaledW)/2, float64(scaledH)/2
	bboxW := int(math.Ceil(math.Abs(float64(scaledW)*cos) + math.Abs(float64(scaledH)*sin)))
	bboxH := int(math.Ceil(math.Abs(float64(scaledW)*sin) + math.Abs(float64(scaledH)*cos)))
	cx, cy := float64(x)+hw, float64(y)+hh
	left := x - bboxW/2
	top := y - bboxH/2
	for dy := top; dy < top+bboxH; dy++ {
		for dx := left; dx < left+bboxW; dx++ {
			if dx < dst.Rect.Min.X || dx >= dst.Rect.Max.X || dy < dst.Rect.Min.Y || dy >= dst.Rect.Max.Y {
				continue
			}
			vx := float64(dx) - cx
			vy := float64(dy) - cy
			sx := int(math.Round(hw + vx*cos + vy*sin))
			sy := int(math.Round(hh - vx*sin + vy*cos))
			if sx < 0 || sx >= scaledW || sy < 0 || sy >= scaledH {
				continue
			}
			blendOver(dst, dx, dy, src.RGBAAt(sx, sy))
		}
	}
}

// clampDim rounds a float dimension to an int bounded by
// [1, maxCanvasDimension]; values <= 0 yield 0 (skip the object).
func clampDim(v float64) int {
	n := int(math.Round(v))
	if n <= 0 {
		return 0
	}
	if n > maxCanvasDimension {
		return maxCanvasDimension
	}
	return n
}

// blendOver composites c over the destination pixel with the Porter-
// Duff "over" operator. color.RGBA is alpha-premultiplied per Go
// conventions, so out = src + dst*(1-srcA) per channel.
func blendOver(dst *image.RGBA, x, y int, c color.RGBA) {
	if c.A == 0 {
		return
	}
	if c.A == 255 {
		dst.SetRGBA(x, y, c)
		return
	}
	d := dst.RGBAAt(x, y)
	fa := uint32(c.A)
	dst.SetRGBA(x, y, color.RGBA{
		R: uint8(uint32(c.R) + uint32(d.R)*(255-fa)/255),
		G: uint8(uint32(c.G) + uint32(d.G)*(255-fa)/255),
		B: uint8(uint32(c.B) + uint32(d.B)*(255-fa)/255),
		A: uint8(fa + uint32(d.A)*(255-fa)/255),
	})
}
