package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// MetadataGenerator calls the NVIDIA AI API to produce structured
// YouTube metadata (title, description, tags, languages, translations).
//
// Security contract:
//   - The API key is read from config (NVIDIA_API_KEY) and NEVER exposed
//     to the frontend or included in any API response.
//   - When the key is empty, Generate returns ErrNVIDIANotConfigured
//     and the caller returns HTTP 503 — the manual metadata flow
//     remains fully functional.
//   - The raw NVIDIA response is validated and sanitised before
//     returning; malformed responses produce ErrNVIDIAResponseInvalid.
//
// The service is intentionally decoupled from the publish flow.
// Callers (the HTTP handler) receive GeneratedMetadata and present
// it to the operator for review/edit before submitting to publish.
type MetadataGenerator struct {
	apiKey     string
	httpClient *http.Client
	// apiURL is the NVIDIA API endpoint. Overridable in tests.
	apiURL string
}

// NewMetadataGenerator constructs the generator. Pass an empty
// apiKey to disable NVIDIA (Generate returns ErrNVIDIANotConfigured).
func NewMetadataGenerator(apiKey string) *MetadataGenerator {
	return &MetadataGenerator{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
		apiURL: "https://integrate.api.nvidia.com/v1/chat/completions",
	}
}

// ErrNVIDIANotConfigured is returned when NVIDIA_API_KEY is empty.
var ErrNVIDIANotConfigured = fmt.Errorf("NVIDIA AI metadata generation is not configured (set NVIDIA_API_KEY)")

// ErrNVIDIAResponseInvalid is returned when the NVIDIA response fails
// server-side validation (malformed JSON, missing required fields,
// out-of-bounds values).
var ErrNVIDIAResponseInvalid = fmt.Errorf("NVIDIA response is invalid")

// Generate calls the NVIDIA API with the supplied prompt, validates
// every field against YouTube's bounds, deduplicates and normalises
// tags, and returns a sanitised GeneratedMetadata.
//
// The prompt should describe the video content, target audience,
// and desired tone so the model can produce relevant metadata.
func (g *MetadataGenerator) Generate(ctx context.Context, prompt string) (*NVIDIAMetadataResponse, error) {
	if g.apiKey == "" {
		return nil, ErrNVIDIANotConfigured
	}

	raw, err := g.callNVIDIA(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("nvidia api call: %w", err)
	}

	if err := g.validateAndNormalise(raw); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNVIDIAResponseInvalid, err)
	}

	return raw, nil
}

// nvidiaChatRequest is the OpenAI-compatible chat completion request
// body sent to NVIDIA's API endpoint.
type nvidiaChatRequest struct {
	Model       string             `json:"model"`
	Messages    []nvidiaChatMsg    `json:"messages"`
	Temperature float64            `json:"temperature"`
	MaxTokens   int                `json:"max_tokens"`
	ResponseFormat *nvidiaResponseFormat `json:"response_format,omitempty"`
}

type nvidiaChatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type nvidiaResponseFormat struct {
	Type string `json:"type"`
}

// nvidiaChatResponse is the OpenAI-compatible chat completion response.
type nvidiaChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

// callNVIDIA sends the prompt to NVIDIA and returns the raw JSON
// response content. It strips any markdown code fences the model
// might wrap around the JSON.
func (g *MetadataGenerator) callNVIDIA(ctx context.Context, prompt string) (*NVIDIAMetadataResponse, error) {
	systemPrompt := `You are a YouTube metadata generator. Given a video description, produce a JSON object with:
- "title": string (max 100 chars), compelling YouTube title
- "description": string (max 5000 chars), SEO-friendly description
- "tags": string array (max 30 items, total chars with commas ≤ 500)
- "default_language": BCP-47 code (e.g. "it", "en", "pt-BR")
- "default_audio_language": BCP-47 code (same as default_language unless different)
- "translations": object keyed by language code with { "title", "description" }

Return ONLY the JSON object. No markdown, no explanation, no code fences.`

	reqBody := nvidiaChatRequest{
		Model: "nvidia/llama-3.1-nemotron-70b-instruct",
		Messages: []nvidiaChatMsg{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: prompt},
		},
		Temperature: 0.3,
		MaxTokens:   2048,
		ResponseFormat: &nvidiaResponseFormat{Type: "json_object"},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, g.apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+g.apiKey)

	resp, err := g.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB cap
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nvidia returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp nvidiaChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("parse chat response: %w (body=%s)", err, string(respBody))
	}

	if chatResp.Error != nil {
		return nil, fmt.Errorf("nvidia API error: %s (code=%s)", chatResp.Error.Message, chatResp.Error.Code)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("nvidia returned no choices")
	}

	content := chatResp.Choices[0].Message.Content
	content = stripMarkdownCodeFences(content)

	var metadata NVIDIAMetadataResponse
	if err := json.Unmarshal([]byte(content), &metadata); err != nil {
		return nil, fmt.Errorf("parse metadata JSON: %w (content=%s)", err, content)
	}

	return &metadata, nil
}

// stripMarkdownCodeFences removes ```json and ``` wrappers the model
// might output despite the system prompt instructions.
func stripMarkdownCodeFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		// Find the first newline after the opening fence.
		if idx := strings.IndexByte(s, '\n'); idx >= 0 {
			s = s[idx+1:]
		} else {
			// Single-line fence with no body — return empty.
			return ""
		}
	}
	if strings.HasSuffix(s, "```") {
		s = strings.TrimSuffix(s, "```")
	}
	return strings.TrimSpace(s)
}

// validateAndNormalise enforces every YouTube-side bound on the
// NVIDIA-generated metadata. It reuses the existing
// ValidateYouTubeSnippet and YouTubePublishOptions.Validate()
// logic so the bounds are defined in exactly one place.
//
// Normalisation applied:
//   - Trim whitespace from title, description, all tags.
//   - Deduplicate tags (case-insensitive).
//   - Lowercase language codes.
//   - Sort tags alphabetically for deterministic output.
func (g *MetadataGenerator) validateAndNormalise(raw *NVIDIAMetadataResponse) error {
	if raw == nil {
		return fmt.Errorf("metadata is nil")
	}

	// --- Title ---
	raw.Title = strings.TrimSpace(raw.Title)
	if raw.Title == "" {
		return fmt.Errorf("title is required")
	}
	if err := ValidateYouTubeSnippet(raw.Title, ""); err != nil {
		return fmt.Errorf("title: %w", err)
	}

	// --- Description ---
	raw.Description = strings.TrimSpace(raw.Description)
	if err := ValidateYouTubeSnippet("", raw.Description); err != nil {
		return fmt.Errorf("description: %w", err)
	}

	// --- Default language ---
	raw.DefaultLanguage = strings.TrimSpace(raw.DefaultLanguage)
	if raw.DefaultLanguage != "" {
		if err := models.CheckBCP47Like("default_language", raw.DefaultLanguage); err != nil {
			return fmt.Errorf("default_language: %w", err)
		}
	}

	// --- Default audio language ---
	raw.DefaultAudioLanguage = strings.TrimSpace(raw.DefaultAudioLanguage)
	if raw.DefaultAudioLanguage != "" {
		if err := models.CheckBCP47Like("default_audio_language", raw.DefaultAudioLanguage); err != nil {
			return fmt.Errorf("default_audio_language: %w", err)
		}
	}

	// --- Tags: trim, deduplicate, validate ---
	seen := make(map[string]bool)
	normalised := make([]string, 0, len(raw.Tags))
	for _, t := range raw.Tags {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		lower := strings.ToLower(t)
		if seen[lower] {
			continue
		}
		seen[lower] = true
		normalised = append(normalised, t)
	}
	// Sort for deterministic output.
	sort.Strings(normalised)
	raw.Tags = normalised

	// Validate tag count and total chars using the existing
	// YouTubePublishOptions.Validate() path.
	opts := models.YouTubePublishOptions{
		Tags:                 raw.Tags,
		DefaultLanguage:      raw.DefaultLanguage,
		DefaultAudioLanguage: raw.DefaultAudioLanguage,
	}
	if err := opts.Validate(); err != nil {
		return err
	}

	// --- Translations ---
	if raw.Translations != nil {
		for lang, tr := range raw.Translations {
			// Normalise language code.
			delete(raw.Translations, lang)
			normalisedLang := strings.ToLower(strings.TrimSpace(lang))
			if err := models.CheckBCP47Like("translation key", normalisedLang); err != nil {
				return fmt.Errorf("translations[%s]: %w", lang, err)
			}
			tr.Title = strings.TrimSpace(tr.Title)
			tr.Description = strings.TrimSpace(tr.Description)
			if tr.Title == "" && tr.Description == "" {
				// Skip empty translations — don't reject, just drop.
				continue
			}
			if err := ValidateYouTubeSnippet(tr.Title, tr.Description); err != nil {
				return fmt.Errorf("translations[%s]: %w", lang, err)
			}
			raw.Translations[normalisedLang] = tr
		}
	}

	// Validate full translations block with the existing Validate() path.
	fullOpts := models.YouTubePublishOptions{
		Tags:                 raw.Tags,
		DefaultLanguage:      raw.DefaultLanguage,
		DefaultAudioLanguage: raw.DefaultAudioLanguage,
		Translations:         raw.Translations,
	}
	if err := fullOpts.Validate(); err != nil {
		return err
	}

	return nil
}
