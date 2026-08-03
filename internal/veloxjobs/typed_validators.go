package veloxjobs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// typedSpecValidator selects one closed JSON schema per technical job type.
// Every nested object is decoded with DisallowUnknownFields, so adding a
// field requires an explicit schema change rather than silent acceptance.
type typedSpecValidator struct {
	jobType string
}

func (v typedSpecValidator) Validate(spec json.RawMessage) error {
	switch v.jobType {
	case "scene.composite.v1", "clip.stock.v1", "scene.image.v1":
		var value typedSceneSpec
		if err := decodeTypedSpec(spec, &value); err != nil {
			return fmt.Errorf("%s spec: %w", v.jobType, err)
		}
		if value.Scenes == nil {
			return errors.New("spec.scenes is required")
		}
		for i, scene := range *value.Scenes {
			if scene == nil {
				return fmt.Errorf("spec.scenes[%d] must be an object", i)
			}
		}
	case "slideshow.v1":
		var value typedSlideshowSpec
		if err := decodeTypedSpec(spec, &value); err != nil {
			return fmt.Errorf("slideshow.v1 spec: %w", err)
		}
		if value.Images == nil {
			return errors.New("spec.images is required")
		}
		for i, image := range *value.Images {
			if image == nil {
				return fmt.Errorf("spec.images[%d] must be an object", i)
			}
		}
	default:
		return fmt.Errorf("unsupported typed job_type %q", v.jobType)
	}
	return nil
}

func decodeTypedSpec(spec json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(spec))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return errors.New("unexpected trailing JSON value")
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("unexpected trailing JSON: %w", err)
	}
	return nil
}

// typedSceneSpec is shared by composite, stock-clip and image-scene jobs.
type typedSceneSpec struct {
	Scenes *[]*typedScene `json:"scenes"`
}

type typedScene struct {
	ID                string                  `json:"id,omitempty"`
	Text              string                  `json:"text,omitempty"`
	Title             string                  `json:"title,omitempty"`
	DurationMS        int                     `json:"duration_ms,omitempty"`
	StartMS           int                     `json:"start_ms,omitempty"`
	EndMS             int                     `json:"end_ms,omitempty"`
	Assets            *typedSceneAssets       `json:"assets,omitempty"`
	Audio             *typedSceneAudio        `json:"audio,omitempty"`
	Timeline          *typedTimeline          `json:"timeline,omitempty"`
	VisualSlots       []typedVisualSlot       `json:"visual_slots,omitempty"`
	VisualAssignments []typedVisualAssignment `json:"visual_assignments,omitempty"`
	Layers            []typedLayer            `json:"layers,omitempty"`
	Subtitles         []typedSubtitle         `json:"subtitles,omitempty"`
	Transition        *typedTransition        `json:"transition,omitempty"`
	Bindings          *typedBindings          `json:"bindings,omitempty"`
	PrimaryClip       *typedAssetRef          `json:"primary_clip,omitempty"`
	Stock             *typedAssetRef          `json:"stock,omitempty"`
	Image             *typedAssetRef          `json:"image,omitempty"`
}

type typedSceneAssets struct {
	PrimaryClip *typedAssetRef  `json:"primary_clip,omitempty"`
	Stock       *typedAssetRef  `json:"stock,omitempty"`
	Image       *typedAssetRef  `json:"image,omitempty"`
	Additional  []typedAssetRef `json:"additional,omitempty"`
}

type typedSceneAudio struct {
	Voiceover *typedAssetRef `json:"voiceover,omitempty"`
	Music     *typedAssetRef `json:"music,omitempty"`
	Ducking   float64        `json:"ducking,omitempty"`
}

type typedTimeline struct {
	StartMS    int `json:"start_ms,omitempty"`
	EndMS      int `json:"end_ms,omitempty"`
	DurationMS int `json:"duration_ms,omitempty"`
}

type typedVisualSlot struct {
	ID     string         `json:"id,omitempty"`
	Asset  *typedAssetRef `json:"asset,omitempty"`
	Layer  string         `json:"layer,omitempty"`
	X      float64        `json:"x,omitempty"`
	Y      float64        `json:"y,omitempty"`
	Width  float64        `json:"width,omitempty"`
	Height float64        `json:"height,omitempty"`
}

type typedVisualAssignment struct {
	Slot    string         `json:"slot,omitempty"`
	Binding string         `json:"binding,omitempty"`
	Asset   *typedAssetRef `json:"asset,omitempty"`
}

type typedLayer struct {
	ID      string         `json:"id,omitempty"`
	Type    string         `json:"type,omitempty"`
	Text    string         `json:"text,omitempty"`
	Asset   *typedAssetRef `json:"asset,omitempty"`
	Opacity float64        `json:"opacity,omitempty"`
	StartMS int            `json:"start_ms,omitempty"`
	EndMS   int            `json:"end_ms,omitempty"`
}

type typedSubtitle struct {
	Text     string `json:"text,omitempty"`
	StartMS  int    `json:"start_ms,omitempty"`
	EndMS    int    `json:"end_ms,omitempty"`
	Style    string `json:"style,omitempty"`
	Language string `json:"language,omitempty"`
}

type typedTransition struct {
	Type       string `json:"type,omitempty"`
	DurationMS int    `json:"duration_ms,omitempty"`
}

type typedBindings struct {
	Clip      *typedAssetRef `json:"clip,omitempty"`
	Stock     *typedAssetRef `json:"stock,omitempty"`
	Voiceover *typedAssetRef `json:"voiceover,omitempty"`
}

type typedAssetRef struct {
	ID         string `json:"id,omitempty"`
	AssetID    string `json:"asset_id,omitempty"`
	URI        string `json:"uri,omitempty"`
	URL        string `json:"url,omitempty"`
	LocalPath  string `json:"local_path,omitempty"`
	Type       string `json:"type,omitempty"`
	DurationMS int    `json:"duration_ms,omitempty"`
	StartMS    int    `json:"start_ms,omitempty"`
	EndMS      int    `json:"end_ms,omitempty"`
	Fallback   bool   `json:"fallback,omitempty"`
}

// typedSlideshowSpec is intentionally separate from typedSceneSpec: an image
// item has a smaller, image-oriented vocabulary and cannot silently accept a
// scene-only field such as visual_slots or bindings.
type typedSlideshowSpec struct {
	Images *[]*typedImage `json:"images"`
}

type typedImage struct {
	ID         string           `json:"id,omitempty"`
	AssetID    string           `json:"asset_id,omitempty"`
	URI        string           `json:"uri,omitempty"`
	URL        string           `json:"url,omitempty"`
	LocalPath  string           `json:"local_path,omitempty"`
	Text       string           `json:"text,omitempty"`
	DurationMS int              `json:"duration_ms,omitempty"`
	StartMS    int              `json:"start_ms,omitempty"`
	EndMS      int              `json:"end_ms,omitempty"`
	Zoom       float64          `json:"zoom,omitempty"`
	PanX       float64          `json:"pan_x,omitempty"`
	PanY       float64          `json:"pan_y,omitempty"`
	Transition *typedTransition `json:"transition,omitempty"`
}
