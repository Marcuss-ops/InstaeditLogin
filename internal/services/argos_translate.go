package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// DescriptionTranslator is the narrow capability used by publishing to
// localize long-form descriptions without coupling the worker to Argos.
type DescriptionTranslator interface {
	TranslateDescription(ctx context.Context, req TranslateRequest) (string, error)
}

// ArgosDescriptionTranslator calls the local Argos Translate service using
// the LibreTranslate-compatible /translate contract. It intentionally
// translates descriptions only: titles remain the responsibility of NVIDIA.
type ArgosDescriptionTranslator struct {
	baseURL    string
	httpClient *http.Client
}

func NewArgosDescriptionTranslator(baseURL string) *ArgosDescriptionTranslator {
	return &ArgosDescriptionTranslator{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		httpClient: NewHTTPClientWithTimeout(30 * time.Second),
	}
}

func (a *ArgosDescriptionTranslator) Configured() bool {
	return a != nil && a.baseURL != ""
}

func (a *ArgosDescriptionTranslator) TranslateDescription(ctx context.Context, req TranslateRequest) (string, error) {
	if !a.Configured() {
		return "", ErrArgosNotConfigured
	}
	if strings.TrimSpace(req.Description) == "" {
		return "", nil
	}
	source := strings.ToLower(strings.TrimSpace(req.SourceLanguage))
	if source == "" {
		return "", fmt.Errorf("argos translate: source language is required")
	}
	target := strings.ToLower(strings.TrimSpace(req.TargetLanguage))
	if target == "" {
		return "", fmt.Errorf("argos translate: target language is required")
	}
	payload, err := json.Marshal(struct {
		Text   string `json:"q"`
		Source string `json:"source"`
		Target string `json:"target"`
		Format string `json:"format,omitempty"`
	}{req.Description, source, target, "text"})
	if err != nil {
		return "", fmt.Errorf("argos translate: encode request: %w", err)
	}
	endpoint := a.baseURL
	if !strings.HasSuffix(endpoint, "/translate") {
		endpoint += "/translate"
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("argos translate: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("argos translate: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("argos translate: HTTP %d", resp.StatusCode)
	}
	var result struct {
		TranslatedText string `json:"translatedText"`
		Error          string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("argos translate: decode response: %w", err)
	}
	translated := strings.TrimSpace(result.TranslatedText)
	if translated == "" {
		if result.Error != "" {
			return "", fmt.Errorf("argos translate: %s", result.Error)
		}
		return "", fmt.Errorf("argos translate: empty response")
	}
	if err := ValidateYouTubeSnippet("", translated); err != nil {
		return "", fmt.Errorf("argos translate: invalid description: %w", err)
	}
	return translated, nil
}

var ErrArgosNotConfigured = fmt.Errorf("Argos description translation is not configured (set ARGOS_TRANSLATE_URL)")

var _ DescriptionTranslator = (*ArgosDescriptionTranslator)(nil)
