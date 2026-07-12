package models

import "time"

// CapabilitySet is the per-platform feature set used by ContentValidator and
// the per-account capability endpoint.
//
// Theoretical CapabilitySet = the platform's documented limits loaded from
// config/capabilities.json. This is the SAFE default: what the platform's
// API docs say is supported.
//
// Real CapabilitySet = the runtime-derived feature set returned by the
// provider's AccountCapabilitiesDiscoverer (GetAccountCapabilities). This
// reflects the *actual* account — a Facebook Page may or may not support
// features beyond the baseline depending on the account type, age,
// permissions, and platform-side flags.
//
// Effective CapabilitySet = the per-publish intersection:
//   effective.field = theory.field AND (real is nil OR real.field)
// Reasoning: we never relax what theory says the platform supports.
// If real says the account DOES support a feature theory permits, the
// feature is usable. If real says the account does NOT support a feature
// even though theory says the platform supports it, the feature is NOT
// usable on this account. If real is unavailable (cache miss / discoverer
// error / discoverer not implemented), the theoretical set is the safe
// fallback — we treat the payload as valid iff theory permits.
//
// PrivacyLevels is intentionally a []string slice (not an enum type) so
// providers can declare their canonical values declaratively (TikTok:
// ["PUBLIC_TO_EVERYONE","MUTUAL_FOLLOW_FRIENDS","SELF_ONLY"]; YouTube:
// ["public","unlisted","private"]; LinkedIn: ["PUBLIC","CONNECTIONS"]).
// The intersection over real ∩ theory is well-defined on string equality.
type CapabilitySet struct {
	// TextOnly forces text-only payloads. When true, the validator rejects
	// payload.ImageURL / payload.VideoURL if either is set. (Historical
	// "Twitter as text-only" behaviour is now driven by this flag.)
	TextOnly bool `json:"text_only"`
	// SupportsImages / SupportsVideo / SupportsCarousel / SupportsStories
	// are the platform's allow-list for media types. The validator
	// rejects payload fields that conflict with these flags.
	SupportsImages   bool `json:"supports_images"`
	SupportsVideo    bool `json:"supports_video"`
	SupportsCarousel bool `json:"supports_carousel"`
	SupportsStories  bool `json:"supports_stories"`

	// MaxMediaCount caps the number of distinct media assets a single post
	// can carry. The publish payload currently carries a single asset_id,
	// so only "0" (forbidden) and "1+" (allowed) are physically meaningful
	// today; the field is here so multi-asset carousel posts (post-MVP)
	// can declare their limit declaratively and the validator can enforce
	// it without code changes.
	MaxMediaCount int `json:"max_media_count"`

	// MaxCaptionRunes caps the caption length in Unicode code points
	// (rune = one displayed "letter"; emojis are typically 1-2 runes).
	// The validator rejects payloads whose payload.Text exceeds this.
	MaxCaptionRunes int `json:"max_caption_runes"`

	// MaxVideoSeconds is optional (writeable as null/0 in JSON). The
	// validator only consults it when Supported=true on the validation
	// path's media probe — it is NOT enforced server-side today because
	// the publish payload does not yet carry a duration field. Future
	// work wires a media-probe call when this matters (e.g. YouTube
	// uploads often reject over-12-hour videos even though the API
	// theoretically permits them).
	MaxVideoSeconds int `json:"max_video_seconds,omitempty"`

	// PrivacyLevels declares the platform's canonically-recognised
	// privacy/visibility values. The validator accepts only these (after
	// canonicalisation: trim + uppercase or lowercase per platform
	// convention). An empty slice means "no privacy/visibility field
	// is required" (e.g. Twitter: any string is accepted).
	PrivacyLevels []string `json:"privacy_levels,omitempty"`
}

// AccountCapabilities is the JSON shape returned by
// GET /api/v1/accounts/{id}/capabilities. The response is the canonical
// "what can I publish on this account right now" answer: three layers
// (theoretical from matrix.json, real from the provider's runtime
// query, effective = real ∩ theoretical) plus an error string when
// real-flavour discovery failed but theoretical is still useful.
//
// The endpoint contract is "always 200 with a populated struct" — if
// the account doesn't exist we 404 BEFORE serialisation; once the
// account exists, every account gets a response (theoretical is
// always populated from the matrix; real may be partial / missing /
// error'd and that is part of the call's success surface, not a
// reason to fail the HTTP call).
type AccountCapabilities struct {
	// Platform is the platform name (e.g. "instagram", "tiktok").
	Platform string `json:"platform"`
	// PlatformAccountID is the platform_accounts.id of the row this
	// capability snapshot was computed for. Surfaced so the client can
	// correlate the snapshot with the displayed PlatformAccount row.
	PlatformAccountID int64 `json:"platform_account_id"`

	// Theoretical is the platform-level fallback (from config/capabilities.json
	// merged with built-in defaults). Always populated.
	Theoretical CapabilitySet `json:"theoretical"`
	// Real is the runtime-fetched account-level feature set. Nil if the
	// platform doesn't implement AccountCapabilitiesDiscoverer, or if
	// the discovery call errored, or if the cache is empty and the live
	// fetch failed. Clients SHOULD test for nil before intersecting.
	Real *CapabilitySet `json:"real,omitempty"`
	// Effective is the per-publish intersection (real ∩ theoretical).
	// The ContentValidator uses this set to decide accept/reject.
	// Always populated.
	Effective CapabilitySet `json:"effective"`
	// RealCapsError is the human prose of the discovery error, if any.
	// Example: "twitter api /2/users/me?user.fields=public_metrics failed
	// (status 503)". Empty when discovery succeeded or isn't supported.
	RealCapsError string `json:"real_caps_error,omitempty"`
	// LastFetchedAt is the timestamp of the most recent successful
	// discovery call (i.e. the cache was populated from this instant).
	// Nil when the cache is empty.
	LastFetchedAt *time.Time `json:"last_fetched_at,omitempty"`
}

// Intersect returns the per-field conjunction of a (theoretical, real)
// pair. The operation is the SAFE side: a feature is enabled only if
// BOTH theory AND real permit it. A nil real set is treated as "no real
// data"; in that case the result equals theory (acceptance constraint:
// callers must use a NIL real differently from a populated real; the
// effective-field semantics above document this).
//
// Used by the ContentValidator to compute the effective capability set
// before every publish. The shape is bit-AND on bools and min() on
// numeric bounds. PrivacyLevels is intersection of the two slices —
// a value is in the result iff it appears in both.
func Intersect(theory, real *CapabilitySet) CapabilitySet {
	if theory == nil {
		return CapabilitySet{}
	}
	if real == nil {
		// No real data → fall back to theoretical (safe upper bound;
		// callers may want a tighter set, but this matches the
		// documented "real is nil → fallback to theory" rule).
		return *theory
	}
	out := CapabilitySet{
		TextOnly:         theory.TextOnly && real.TextOnly,
		SupportsImages:   theory.SupportsImages && real.SupportsImages,
		SupportsVideo:    theory.SupportsVideo && real.SupportsVideo,
		SupportsCarousel: theory.SupportsCarousel && real.SupportsCarousel,
		SupportsStories:  theory.SupportsStories && real.SupportsStories,
		MaxMediaCount:    minInt(theory.MaxMediaCount, real.MaxMediaCount),
		MaxCaptionRunes:  minInt(theory.MaxCaptionRunes, real.MaxCaptionRunes),
		MaxVideoSeconds:  minInt(theory.MaxVideoSeconds, real.MaxVideoSeconds),
	}
	if len(theory.PrivacyLevels) > 0 || len(real.PrivacyLevels) > 0 {
		out.PrivacyLevels = intersectStringSlice(theory.PrivacyLevels, real.PrivacyLevels)
	}
	return out
}

// minInt returns the smaller of two non-negative ints. When at least
// one is 0 (meaning "no constraint documented"), protect against
// confusing "0 means no constraint" with "0 is the limit". We treat 0
// as "no documented upper bound" — so minInt(0, x) = x and minInt(x, 0)
// = x accordingly. This keeps the JSON-config ergonomics intact (a
// field omitted from JSON becomes 0 — a free signal, not a confusing
// zero-length limit).
func minInt(a, b int) int {
	if a == 0 {
		return b
	}
	if b == 0 {
		return a
	}
	if a < b {
		return a
	}
	return b
}

// intersectStringSlice returns the set of strings present in both
// slices. Order is preserved (a-first, then b-only if a misses). An
// empty input slice returns nil — callers must check `len(result) == 0`
// to distinguish "no values" from "all values deliberately omitted".
func intersectStringSlice(a, b []string) []string {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(b))
	for _, v := range b {
		set[v] = struct{}{}
	}
	out := make([]string, 0, min(len(a), len(b)))
	for _, v := range a {
		if _, ok := set[v]; ok {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
