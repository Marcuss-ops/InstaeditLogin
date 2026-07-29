package services

import "github.com/Marcuss-ops/InstaeditLogin/internal/models"

// NVIDIAMetadataRequest is the payload sent to the NVIDIA AI API to
// generate YouTube metadata. The Prompt field provides context
// (e.g. video topic, target audience, tone) so the model produces
// relevant title, description, tags and translations.
type NVIDIAMetadataRequest struct {
	Prompt string `json:"prompt"`
	// Model is the NVIDIA model identifier (optional; defaults to
	// the service-level default when empty).
	Model string `json:"model,omitempty"`
}

// NVIDIAMetadataResponse is the JSON shape the NVIDIA API MUST return
// AND the canonical output the Dark Editor consumes. Every field is
// validated server-side before being returned to the caller.
// The API must NOT return markdown, free-text, or explanations
// around the JSON — only the raw object.
type NVIDIAMetadataResponse struct {
	Title                string                               `json:"title"`
	Description          string                               `json:"description"`
	Tags                 []string                             `json:"tags"`
	DefaultLanguage      string                               `json:"default_language"`
	DefaultAudioLanguage string                               `json:"default_audio_language"`
	Translations         map[string]models.YouTubeTranslation `json:"translations"`
}
