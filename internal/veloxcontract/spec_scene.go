package veloxcontract

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// CanonicalJobSubmission is the descriptive name for the canonical
// velox.job.v1 envelope. The alias preserves JobSubmissionRequest as the
// established public name while making adapter intent explicit.
type CanonicalJobSubmission = JobSubmissionRequest

// SpecSceneSubmission is the migration input produced by editorial/pipeline
// code before it is converted to the canonical job envelope.
type SpecSceneSubmission struct {
	ContractVersion string
	IdempotencyKey  string
	JobType         string
	TemplateID      string
	TemplateVersion int
	VideoName       string
	Scenes          []SpecScene
	Output          *JobOutput
	DeliveryPlan    DeliveryPlan
}

// SpecScene is the typed legacy scene shape. It contains editorial fields and
// local bindings, but the adapter emits only canonical asset references.
type SpecScene struct {
	ID                string
	Text              string
	Title             string
	DurationMS        int
	LocalPath         string
	Assets            *SpecSceneAssets
	Bindings          SpecSceneBindings
	VisualAssignments []SpecSceneVisualAssignment
	VisualSlots       []SpecSceneVisualSlot
	Audio             *SpecSceneAudio
	Timeline          *SpecSceneTimeline
	Subtitles         []SpecSceneSubtitle
	Transition        *SpecSceneTransition
}

type SpecSceneAssets struct {
	PrimaryClip *SpecSceneAsset
	Stock       *SpecSceneAsset
	Image       *SpecSceneAsset
	Additional  []SpecSceneAsset
}

type SpecSceneBindings struct {
	Clip      *SpecSceneAsset
	Stock     *SpecSceneAsset
	Voiceover *SpecSceneAsset
}

type SpecSceneAsset struct {
	AssetID    string
	URI        string
	URL        string
	LocalPath  string
	Type       string
	DurationMS int
	StartMS    int
	EndMS      int
	Fallback   bool
}

type SpecSceneVisualAssignment struct {
	Slot  string
	Asset *SpecSceneAsset
}

type SpecSceneVisualSlot struct {
	ID     string
	Asset  *SpecSceneAsset
	Layer  string
	X      float64
	Y      float64
	Width  float64
	Height float64
}

type SpecSceneAudio struct {
	Voiceover *SpecSceneAsset
	Music     *SpecSceneAsset
	Ducking   float64
}

type SpecSceneTimeline struct {
	StartMS    int
	EndMS      int
	DurationMS int
}

type SpecSceneSubtitle struct {
	Text     string
	StartMS  int
	EndMS    int
	Style    string
	Language string
}

type SpecSceneTransition struct {
	Type       string
	DurationMS int
}

// AdaptSpecSceneSubmission converts the legacy scene model into the canonical
// envelope. It validates envelope requirements and rejects unresolved local
// filesystem paths so workers only receive portable asset references.
func AdaptSpecSceneSubmission(input SpecSceneSubmission) (CanonicalJobSubmission, error) {
	if len(input.Scenes) == 0 {
		return CanonicalJobSubmission{}, errors.New("spec scenes must be non-empty")
	}
	canonicalScenes := make([]canonicalSpecScene, 0, len(input.Scenes))
	for i, scene := range input.Scenes {
		converted, err := adaptSpecScene(scene)
		if err != nil {
			return CanonicalJobSubmission{}, fmt.Errorf("scene[%d]: %w", i, err)
		}
		canonicalScenes = append(canonicalScenes, converted)
	}
	spec, err := json.Marshal(struct {
		Scenes []canonicalSpecScene `json:"scenes"`
	}{Scenes: canonicalScenes})
	if err != nil {
		return CanonicalJobSubmission{}, fmt.Errorf("marshal scenes: %w", err)
	}
	result := CanonicalJobSubmission{
		ContractVersion: input.ContractVersion,
		IdempotencyKey:  input.IdempotencyKey,
		JobType:         input.JobType,
		TemplateID:      input.TemplateID,
		TemplateVersion: input.TemplateVersion,
		VideoName:       input.VideoName,
		Spec:            spec,
		Output:          input.Output,
		DeliveryPlan:    input.DeliveryPlan,
	}
	if err := result.ValidateCanonical(); err != nil {
		return CanonicalJobSubmission{}, err
	}
	return result, nil
}

type canonicalSpecScene struct {
	ID                string                      `json:"id,omitempty"`
	Text              string                      `json:"text,omitempty"`
	Title             string                      `json:"title,omitempty"`
	DurationMS        int                         `json:"duration_ms,omitempty"`
	Assets            *canonicalSpecSceneAssets   `json:"assets,omitempty"`
	Bindings          *canonicalSpecSceneBindings `json:"bindings,omitempty"`
	VisualAssignments []canonicalVisualAssignment `json:"visual_assignments,omitempty"`
	VisualSlots       []canonicalVisualSlot       `json:"visual_slots,omitempty"`
	Audio             *canonicalSceneAudio        `json:"audio,omitempty"`
	Timeline          *canonicalSceneTimeline     `json:"timeline,omitempty"`
	Subtitles         []canonicalSceneSubtitle    `json:"subtitles,omitempty"`
	Transition        *canonicalSceneTransition   `json:"transition,omitempty"`
}

type canonicalSpecSceneAssets struct {
	PrimaryClip *canonicalAssetRef  `json:"primary_clip,omitempty"`
	Stock       *canonicalAssetRef  `json:"stock,omitempty"`
	Image       *canonicalAssetRef  `json:"image,omitempty"`
	Additional  []canonicalAssetRef `json:"additional,omitempty"`
}

type canonicalSpecSceneBindings struct {
	Clip      *canonicalAssetRef `json:"clip,omitempty"`
	Stock     *canonicalAssetRef `json:"stock,omitempty"`
	Voiceover *canonicalAssetRef `json:"voiceover,omitempty"`
}

type canonicalAssetRef struct {
	AssetID    string `json:"asset_id,omitempty"`
	URI        string `json:"uri,omitempty"`
	URL        string `json:"url,omitempty"`
	Type       string `json:"type,omitempty"`
	DurationMS int    `json:"duration_ms,omitempty"`
	StartMS    int    `json:"start_ms,omitempty"`
	EndMS      int    `json:"end_ms,omitempty"`
	Fallback   bool   `json:"fallback,omitempty"`
}

type canonicalVisualAssignment struct {
	Slot  string             `json:"slot,omitempty"`
	Asset *canonicalAssetRef `json:"asset,omitempty"`
}

type canonicalVisualSlot struct {
	ID     string             `json:"id,omitempty"`
	Asset  *canonicalAssetRef `json:"asset,omitempty"`
	Layer  string             `json:"layer,omitempty"`
	X      float64            `json:"x,omitempty"`
	Y      float64            `json:"y,omitempty"`
	Width  float64            `json:"width,omitempty"`
	Height float64            `json:"height,omitempty"`
}

type canonicalSceneAudio struct {
	Voiceover *canonicalAssetRef `json:"voiceover,omitempty"`
	Music     *canonicalAssetRef `json:"music,omitempty"`
	Ducking   float64            `json:"ducking,omitempty"`
}

type canonicalSceneTimeline struct {
	StartMS    int `json:"start_ms,omitempty"`
	EndMS      int `json:"end_ms,omitempty"`
	DurationMS int `json:"duration_ms,omitempty"`
}

type canonicalSceneSubtitle struct {
	Text     string `json:"text,omitempty"`
	StartMS  int    `json:"start_ms,omitempty"`
	EndMS    int    `json:"end_ms,omitempty"`
	Style    string `json:"style,omitempty"`
	Language string `json:"language,omitempty"`
}

type canonicalSceneTransition struct {
	Type       string `json:"type,omitempty"`
	DurationMS int    `json:"duration_ms,omitempty"`
}

func adaptSpecScene(scene SpecScene) (canonicalSpecScene, error) {
	if strings.TrimSpace(scene.ID) == "" {
		return canonicalSpecScene{}, errors.New("id is required")
	}
	if scene.DurationMS < 0 {
		return canonicalSpecScene{}, errors.New("duration_ms must not be negative")
	}
	if scene.LocalPath != "" {
		return canonicalSpecScene{}, errors.New("scene local_path must be replaced by an asset reference")
	}
	result := canonicalSpecScene{
		ID:         scene.ID,
		Text:       scene.Text,
		Title:      scene.Title,
		DurationMS: scene.DurationMS,
	}
	var err error
	if scene.Assets != nil {
		result.Assets, err = adaptSceneAssets(*scene.Assets)
		if err != nil {
			return canonicalSpecScene{}, err
		}
	}
	result.Bindings, err = adaptBindings(scene.Bindings)
	if err != nil {
		return canonicalSpecScene{}, err
	}
	result.VisualAssignments, err = adaptVisualAssignments(scene.VisualAssignments)
	if err != nil {
		return canonicalSpecScene{}, err
	}
	result.VisualSlots, err = adaptVisualSlots(scene.VisualSlots)
	if err != nil {
		return canonicalSpecScene{}, err
	}
	result.Audio, err = adaptAudio(scene.Audio)
	if err != nil {
		return canonicalSpecScene{}, err
	}
	if scene.Timeline != nil {
		result.Timeline = &canonicalSceneTimeline{
			StartMS: scene.Timeline.StartMS, EndMS: scene.Timeline.EndMS, DurationMS: scene.Timeline.DurationMS,
		}
	}
	result.Subtitles = make([]canonicalSceneSubtitle, 0, len(scene.Subtitles))
	for _, subtitle := range scene.Subtitles {
		result.Subtitles = append(result.Subtitles, canonicalSceneSubtitle{
			Text: subtitle.Text, StartMS: subtitle.StartMS, EndMS: subtitle.EndMS,
			Style: subtitle.Style, Language: subtitle.Language,
		})
	}
	if scene.Transition != nil {
		result.Transition = &canonicalSceneTransition{Type: scene.Transition.Type, DurationMS: scene.Transition.DurationMS}
	}
	return result, nil
}

func adaptSceneAssets(input SpecSceneAssets) (*canonicalSpecSceneAssets, error) {
	result := &canonicalSpecSceneAssets{}
	var err error
	if result.PrimaryClip, err = adaptAsset(input.PrimaryClip); err != nil {
		return nil, err
	}
	if result.Stock, err = adaptAsset(input.Stock); err != nil {
		return nil, err
	}
	if result.Image, err = adaptAsset(input.Image); err != nil {
		return nil, err
	}
	for i := range input.Additional {
		asset, assetErr := adaptAsset(&input.Additional[i])
		if assetErr != nil {
			return nil, assetErr
		}
		if asset != nil {
			result.Additional = append(result.Additional, *asset)
		}
	}
	return result, nil
}

func adaptBindings(input SpecSceneBindings) (*canonicalSpecSceneBindings, error) {
	clip, err := adaptAsset(input.Clip)
	if err != nil {
		return nil, err
	}
	stock, err := adaptAsset(input.Stock)
	if err != nil {
		return nil, err
	}
	voiceover, err := adaptAsset(input.Voiceover)
	if err != nil {
		return nil, err
	}
	if clip == nil && stock == nil && voiceover == nil {
		return nil, nil
	}
	return &canonicalSpecSceneBindings{Clip: clip, Stock: stock, Voiceover: voiceover}, nil
}

func adaptVisualAssignments(input []SpecSceneVisualAssignment) ([]canonicalVisualAssignment, error) {
	result := make([]canonicalVisualAssignment, 0, len(input))
	for _, assignment := range input {
		asset, err := adaptAsset(assignment.Asset)
		if err != nil {
			return nil, err
		}
		result = append(result, canonicalVisualAssignment{Slot: assignment.Slot, Asset: asset})
	}
	return result, nil
}

func adaptVisualSlots(input []SpecSceneVisualSlot) ([]canonicalVisualSlot, error) {
	result := make([]canonicalVisualSlot, 0, len(input))
	for _, slot := range input {
		asset, err := adaptAsset(slot.Asset)
		if err != nil {
			return nil, err
		}
		result = append(result, canonicalVisualSlot{
			ID: slot.ID, Asset: asset, Layer: slot.Layer, X: slot.X, Y: slot.Y,
			Width: slot.Width, Height: slot.Height,
		})
	}
	return result, nil
}

func adaptAudio(input *SpecSceneAudio) (*canonicalSceneAudio, error) {
	if input == nil {
		return nil, nil
	}
	voiceover, err := adaptAsset(input.Voiceover)
	if err != nil {
		return nil, err
	}
	music, err := adaptAsset(input.Music)
	if err != nil {
		return nil, err
	}
	return &canonicalSceneAudio{Voiceover: voiceover, Music: music, Ducking: input.Ducking}, nil
}

func adaptAsset(input *SpecSceneAsset) (*canonicalAssetRef, error) {
	if input == nil {
		return nil, nil
	}
	if input.DurationMS < 0 || input.StartMS < 0 || input.EndMS < 0 {
		return nil, errors.New("asset timing values must not be negative")
	}
	if input.LocalPath != "" && !strings.HasPrefix(input.LocalPath, "velox-asset://") {
		return nil, errors.New("local_path must be a portable velox-asset:// URI or be replaced by asset_id")
	}
	ref := &canonicalAssetRef{
		AssetID: input.AssetID, URL: input.URL, Type: input.Type,
		DurationMS: input.DurationMS, StartMS: input.StartMS, EndMS: input.EndMS,
		Fallback: input.Fallback,
	}
	switch {
	case input.AssetID != "":
	case input.URI != "":
		ref.URI = input.URI
	case input.URL != "":
		ref.URL = input.URL
	case strings.HasPrefix(input.LocalPath, "velox-asset://"):
		ref.URI = input.LocalPath
	case input.LocalPath != "":
		return nil, errors.New("local_path must be a portable velox-asset:// URI or be replaced by asset_id")
	default:
		return nil, errors.New("asset requires asset_id, uri, url, or portable local_path")
	}
	return ref, nil
}
