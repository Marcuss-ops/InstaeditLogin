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
	model      string
	httpClient *http.Client
	// apiURL is the NVIDIA API endpoint. Overridable in tests.
	apiURL string
}

// defaultNVIDIAModel is the NVIDIA API catalog model used when
// NVIDIA_MODEL is not configured. Model availability is PER NVIDIA
// ACCOUNT: a key without access to this function gets a 404
// ("Function …: Not found for account …") from the API. Deployments
// whose key cannot access the default must pin an accessible model
// via NVIDIA_MODEL (see WithModel).
const defaultNVIDIAModel = "nvidia/llama-3.1-nemotron-70b-instruct"

// MetadataGeneratorOption configures a MetadataGenerator.
type MetadataGeneratorOption func(*MetadataGenerator)

// WithModel overrides the NVIDIA API catalog model used for metadata
// generation. An empty value keeps the default.
func WithModel(model string) MetadataGeneratorOption {
	return func(g *MetadataGenerator) {
		if model != "" {
			g.model = model
		}
	}
}

// NewMetadataGenerator constructs the generator. Pass an empty
// apiKey to disable NVIDIA (Generate returns ErrNVIDIANotConfigured).
func NewMetadataGenerator(apiKey string, opts ...MetadataGeneratorOption) *MetadataGenerator {
	g := &MetadataGenerator{
		apiKey: apiKey,
		model:  defaultNVIDIAModel,
		// The generation (title + long description + tags + up to
		// several per-language translations) is a single large chat
		// completion; on NVIDIA's hosted tier observed latencies are
		// 60-180s+ and highly variable (benchmark 2026-08-07 measured
		// >180s for a 3-language single call). 300s avoids spurious
		// context-deadline failures for heavy multi-language prompts.
		httpClient: NewHTTPClientWithTimeout(300 * time.Second),
		apiURL:     "https://integrate.api.nvidia.com/v1/chat/completions",
	}
	for _, opt := range opts {
		opt(g)
	}
	return g
}

// Configured reports whether the generator has credentials for NVIDIA.
// Keeping this check on the service prevents callers from mistaking a
// non-nil, intentionally disabled generator for an available provider.
func (g *MetadataGenerator) Configured() bool {
	return g != nil && strings.TrimSpace(g.apiKey) != ""
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

	content, err := g.chatCompletion(ctx, metadataGenerationSystemPrompt, prompt)
	if err != nil {
		return nil, fmt.Errorf("nvidia api call: %w", err)
	}

	var raw NVIDIAMetadataResponse
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return nil, fmt.Errorf("parse metadata JSON: %w (content=%s)", err, content)
	}

	if err := g.validateAndNormalise(&raw); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNVIDIAResponseInvalid, err)
	}

	return &raw, nil
}

// metadataGenerationSystemPrompt is the system prompt for the full
// metadata generation flow (title + description + tags + all
// per-language translations in one completion).
const metadataGenerationSystemPrompt = `You are a YouTube metadata generator. Given a video description, produce a JSON object with:
- "title": string (max 100 chars), compelling YouTube title
- "description": string (max 5000 chars), SEO-friendly description
- "tags": string array (max 30 items, total chars with commas ≤ 500)
- "default_language": BCP-47 code (e.g. "it", "en", "pt-BR")
- "default_audio_language": BCP-47 code (same as default_language unless different)
- "translations": object keyed by language code with { "title", "description" }

Return ONLY the JSON object. No markdown, no explanation, no code fences.`

// nvidiaChatRequest is the OpenAI-compatible chat completion request
// body sent to NVIDIA's API endpoint.
type nvidiaChatRequest struct {
	Model          string                `json:"model"`
	Messages       []nvidiaChatMsg       `json:"messages"`
	Temperature    float64               `json:"temperature"`
	MaxTokens      int                   `json:"max_tokens"`
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

// chatCompletion performs the single chat-completions HTTP round trip
// against the NVIDIA API with the given system + user prompts and
// returns the raw assistant content (markdown code fences stripped).
// Callers parse + validate their own JSON shape; Generate and Translate
// are the two consumers. It strips any markdown code fences the model
// might wrap around the JSON.
func (g *MetadataGenerator) chatCompletion(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	reqBody := nvidiaChatRequest{
		Model: g.model,
		Messages: []nvidiaChatMsg{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: 0.3,
		// 4096 output tokens leaves room for title + long description
		// + tags + all per-language translations in one completion;
		// 2048 truncated heavy multi-language prompts mid-JSON.
		MaxTokens:      4096,
		ResponseFormat: &nvidiaResponseFormat{Type: "json_object"},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, g.apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+g.apiKey)

	resp, err := g.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB cap
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("nvidia returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp nvidiaChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", fmt.Errorf("parse chat response: %w (body=%s)", err, string(respBody))
	}

	if chatResp.Error != nil {
		return "", fmt.Errorf("nvidia API error: %s (code=%s)", chatResp.Error.Message, chatResp.Error.Code)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("nvidia returned no choices")
	}

	content := chatResp.Choices[0].Message.Content
	return stripMarkdownCodeFences(content), nil
}

// Translate translates the post's title + description into a single
// target language — the per-channel-language posting step. One HTTP
// call per target language: unlike Generate (whose response is the
// full metadata object with ALL translations inside), Translate asks
// for exactly two fields, so the prompt shape is simpler and the
// per-language result is more reliable than fishing a key out of the
// all-languages output.
//
// Validation applied:
//   - target_language must be a BCP-47-like code;
//   - input and output are bounded by YouTube snippet limits
//     (title ≤ 100, description ≤ 5000) via ValidateYouTubeSnippet;
//   - a response with BOTH fields empty is rejected.
//
// Errors: ErrNVIDIANotConfigured when the API key is empty;
// ErrNVIDIAResponseInvalid (wrapped) for malformed/out-of-bounds
// responses; plain wrapped errors for HTTP/transport failures.
func (g *MetadataGenerator) Translate(ctx context.Context, req TranslateRequest) (*models.YouTubeTranslation, error) {
	if g.apiKey == "" {
		return nil, ErrNVIDIANotConfigured
	}

	target := strings.ToLower(strings.TrimSpace(req.TargetLanguage))
	if target == "" {
		return nil, fmt.Errorf("translate: target_language is required")
	}
	if err := models.CheckBCP47Like("target_language", target); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Title) == "" && strings.TrimSpace(req.Description) == "" {
		return nil, fmt.Errorf("translate: nothing to translate (title and description are both empty)")
	}
	if err := ValidateYouTubeSnippet(req.Title, req.Description); err != nil {
		return nil, fmt.Errorf("translate input: %w", err)
	}

	source := strings.TrimSpace(req.SourceLanguage)
	if source == "" {
		source = "unknown — infer it from the text"
	}

	systemPrompt := `You are a professional translator of YouTube video metadata. Translate the given video title and description into the requested target language.
Return ONLY a JSON object with exactly these fields:
- "title": string (max 100 characters)
- "description": string (max 5000 characters)
Keep the meaning, tone, hashtags and emojis of the original. No markdown, no code fences, no explanations.`

	userPrompt := fmt.Sprintf(`Source language: %s
Target language: %s

Title:
%s

Description:
%s

Translate the title and description above into "%s". Keep the title within 100 characters and the description within 5000 characters.`,
		source, target, req.Title, req.Description, target)

	content, err := g.chatCompletion(ctx, systemPrompt, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("nvidia translate api call: %w", err)
	}

	var raw nvidiaTranslationResponse
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return nil, fmt.Errorf("%w: translate response is not valid JSON: %v (content=%s)", ErrNVIDIAResponseInvalid, err, content)
	}
	raw.Title = strings.TrimSpace(raw.Title)
	raw.Description = strings.TrimSpace(raw.Description)
	if raw.Title == "" && raw.Description == "" {
		return nil, fmt.Errorf("%w: translation response is empty", ErrNVIDIAResponseInvalid)
	}
	// Safety net against "the model echoed the source back": reject when
	// BOTH fields are identical to the input (a single field may stay
	// identical legitimately — brand names, proper nouns — but both
	// identical means the language was never translated). The caller
	// treats this as a failure → target failed → retry, never publishing
	// the wrong language.
	if strings.EqualFold(raw.Title, strings.TrimSpace(req.Title)) &&
		strings.EqualFold(raw.Description, strings.TrimSpace(req.Description)) {
		return nil, fmt.Errorf("%w: translation is identical to the source text (language not translated)", ErrNVIDIAResponseInvalid)
	}
	if err := ValidateYouTubeSnippet(raw.Title, raw.Description); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNVIDIAResponseInvalid, err)
	}
	return &models.YouTubeTranslation{Title: raw.Title, Description: raw.Description}, nil
}

// nvidiaTranslationResponse is the JSON shape the NVIDIA API MUST
// return for Translate: the title + description in the target language.
type nvidiaTranslationResponse struct {
	Title       string `json:"title"`
	Description string `json:"description"`
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
