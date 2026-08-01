package worker

import (
	"encoding/json"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// buildPayload assembles the PublishPayload for the target. It applies
// the privacy-level precedence cascade and platform-specific defaults
// in the process phase. The idempotency key is injected into the payload
// before the publish call.
func (w *PublishWorker) buildPayload(account *models.PlatformAccount, post *models.Post, key string) models.PublishPayload {
	payload := models.PublishPayload{
		Text:         post.Caption,
		Title:        post.Title,
		PublishAt:    post.PublishAt,
		PrivacyLevel: post.PrivacyLevel,
	}
	if post.MediaURL != "" {
		payload.VideoURL = post.MediaURL
	}
	if len(post.Metadata) > 0 {
		var meta struct {
			Tags []string `json:"tags"`
		}
		if err := json.Unmarshal(post.Metadata, &meta); err == nil {
			payload.Tags = meta.Tags
		}
	}
	// Fallback to the inherited batch default (middle term of the cascade).
	if payload.PrivacyLevel == "" {
		payload.PrivacyLevel = post.DefaultPrivacyLevel
	}
	// YouTube-safe default.
	if account.Platform == models.PlatformYouTube && payload.PrivacyLevel == "" {
		payload.PrivacyLevel = "unlisted"
	}
	// Generic fallback.
	if payload.PrivacyLevel == "" {
		payload.PrivacyLevel = "PUBLIC_TO_EVERYONE"
	}
	// TikTok's PULL_FROM_URL mode requires the video URL's domain to be
	// ownership-verified, so route through PULL_FROM_FILE instead.
	if account.Platform == models.PlatformTikTok && payload.Source == "" {
		payload.Source = models.PublishSourcePULLFromFile
	}
	payload.IdempotencyKey = key
	return payload
}
