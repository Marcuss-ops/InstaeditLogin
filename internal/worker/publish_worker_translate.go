package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// ChannelTranslator is the narrow capability the publish worker needs
// to localize a post's title/caption into a target channel's language.
// *services.MetadataGenerator implements it (NVIDIA). Tests inject a
// fake. A nil translator on the worker = feature off (the original
// text is published unchanged).
type ChannelTranslator interface {
	Translate(ctx context.Context, req services.TranslateRequest) (*models.YouTubeTranslation, error)
}

// translationCacheKey identifies one localized (post version, language)
// pair. Sibling targets of the same post that share a channel language
// reuse the same translation, so N channels → 1 NVIDIA call per
// distinct language instead of one per channel. The post version is
// part of the key so a PATCHed post (title/caption edits are legal
// while queued) never reuses a stale translation.
type translationCacheKey struct {
	postID  int64
	version int64
	lang    string
}

// SetNvidiaMetadataTranslator wires the per-channel-language
// translator (the NVIDIA MetadataGenerator in production). Pass nil to
// disable the feature (original text is published). The setter pattern
// keeps NewPublishWorker's positional signature stable across wires.
func (w *PublishWorker) SetNvidiaMetadataTranslator(t ChannelTranslator) {
	w.nvidiaTranslator = t
}

// SetArgosDescriptionTranslator wires the local Argos description provider.
// When configured, NVIDIA supplies only the localized title and Argos
// supplies the localized description.
func (w *PublishWorker) SetArgosDescriptionTranslator(t services.DescriptionTranslator) {
	w.argosDescriptionTranslator = t
}

// localizeForChannel translates the post's title + caption into the
// target channel's language (account.Metadata["language"]) when they
// differ, returning the post to publish and whether a translation was
// applied.
//
// Semantics:
//   - No translator wired / NVIDIA not configured / channel has no
//     language / channel language equals the source language / both
//     title and caption empty → original post, no translation.
//   - Invalid channel language code → warn + original post (a
//     permanent configuration issue; retrying would never fix it).
//   - Translation failure (timeout, 5xx, invalid response, or the
//     model echoed the source back) → error; the caller marks the
//     target failed (production routes through the lease-aware
//     retrying state machine; legacy doubles see terminal 'failed').
//     We deliberately never publish the wrong language.
//
// The localized result is memoized per (post, language): a fan-out to
// several channels of the same language performs a single NVIDIA call
// (each call costs 30-180s+ on NVIDIA's hosted tier).
func (w *PublishWorker) localizeForChannel(ctx context.Context, target *models.PostTarget, account *models.PlatformAccount, post *models.Post) (*models.Post, bool, error) {
	if !postTranslationEnabled(post) {
		return post, false, nil
	}
	if account != nil {
		if snapshot, ok := snapshotForAccount(post.Metadata, account.ID); ok {
			localized := *post
			localized.Title = snapshot.Title
			localized.Caption = snapshot.Description
			return &localized, true, nil
		}
	}
	if w.nvidiaTranslator == nil && w.argosDescriptionTranslator == nil {
		return post, false, nil
	}

	channelLang := strings.ToLower(strings.TrimSpace(accountLanguage(account)))
	if channelLang == "" {
		return post, false, nil
	}
	if err := models.CheckBCP47Like("channel language", channelLang); err != nil {
		w.logger.Warn("publish worker: skipping channel-language translation (invalid channel language code)",
			"target_id", target.ID, "post_id", target.PostID, "platform_account_id", account.ID,
			"channel_language", channelLang, "error", err)
		return post, false, nil
	}
	if strings.TrimSpace(post.Title) == "" && strings.TrimSpace(post.Caption) == "" {
		return post, false, nil
	}

	sourceLang := strings.ToLower(strings.TrimSpace(postSourceLanguage(post)))
	if sourceLang != "" && sourceLang == channelLang {
		return post, false, nil
	}

	// In-memory cache: sibling targets of the same post (same version)
	// sharing a language reuse the same translation (one NVIDIA call
	// total). Keyed by post version so a PATCHed post invalidates.
	key := translationCacheKey{postID: post.ID, version: post.Version, lang: channelLang}
	if cached, ok := w.translationCache.Load(key); ok {
		if localized, isPost := cached.(*models.Post); isPost && localized != nil {
			w.logger.Debug("publish worker: channel-language translation cache hit",
				"target_id", target.ID, "post_id", post.ID, "channel_language", channelLang)
			return localized, true, nil
		}
	}

	start := time.Now()
	req := services.TranslateRequest{
		Title:          post.Title,
		Description:    post.Caption,
		SourceLanguage: sourceLang,
		TargetLanguage: channelLang,
	}
	localizedTitle := post.Title
	localizedDescription := post.Caption
	translated := false
	if w.nvidiaTranslator != nil {
		tr, err := w.nvidiaTranslator.Translate(ctx, req)
		if err != nil {
			if !errors.Is(err, services.ErrNVIDIANotConfigured) {
				return nil, false, fmt.Errorf("channel language translation title (post=%d lang=%s): %w", post.ID, channelLang, err)
			}
			w.logger.Warn("publish worker: NVIDIA not configured — keeping original title",
				"target_id", target.ID, "post_id", post.ID, "channel_language", channelLang)
		} else if tr != nil {
			localizedTitle = tr.Title
			// Compatibility path for deployments that have not wired Argos.
			if w.argosDescriptionTranslator == nil {
				localizedDescription = tr.Description
			}
			translated = true
		}
	}
	if w.argosDescriptionTranslator != nil {
		description, err := w.argosDescriptionTranslator.TranslateDescription(ctx, req)
		if err != nil {
			return nil, false, fmt.Errorf("channel language translation description (post=%d lang=%s): %w", post.ID, channelLang, err)
		}
		if description != "" {
			localizedDescription = description
			translated = true
		}
	}
	if !translated {
		return post, false, nil
	}

	// Shallow copy: the localized title/caption replace the originals;
	// everything else (media refs, metadata, cursors) is shared.
	localized := *post
	localized.Title = localizedTitle
	localized.Caption = localizedDescription
	w.translationCache.Store(key, &localized)
	w.logger.Info("publish worker: channel-language translation applied",
		"target_id", target.ID, "post_id", post.ID, "platform_account_id", account.ID,
		"channel_language", channelLang, "source_language", sourceLang,
		"description_provider", map[bool]string{true: "argos", false: "nvidia"}[w.argosDescriptionTranslator != nil],
		"duration_ms", time.Since(start).Milliseconds())
	return &localized, true, nil
}

// postTranslationEnabled reads the explicit per-post switch. Missing or
// malformed metadata keeps the historical behavior: translation enabled.
func postTranslationEnabled(post *models.Post) bool {
	if post == nil || len(post.Metadata) == 0 {
		return true
	}
	var meta struct {
		TranslationEnabled *bool `json:"translation_enabled"`
	}
	if json.Unmarshal(post.Metadata, &meta) != nil || meta.TranslationEnabled == nil {
		return true
	}
	return *meta.TranslationEnabled
}

// accountLanguage returns the channel language declared in the
// platform_account metadata ("" when absent). Channels imported via
// the CSV operator path (and the Groups UI) store it under
// metadata["language"].
func accountLanguage(account *models.PlatformAccount) string {
	if account == nil || account.Metadata == nil {
		return ""
	}
	v, _ := account.Metadata["language"].(string)
	return v
}

// postSourceLanguage returns the post's declared source language
// (post.Metadata["source_language"], stamped by POST /api/v1/posts
// content.language or by upload-job metadata). "" = unknown.
func postSourceLanguage(post *models.Post) string {
	if post == nil || len(post.Metadata) == 0 {
		return ""
	}
	var meta map[string]any
	if err := json.Unmarshal(post.Metadata, &meta); err != nil {
		return ""
	}
	v, _ := meta["source_language"].(string)
	return v
}
