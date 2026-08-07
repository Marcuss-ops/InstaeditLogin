package config

// VeloxConfig holds the Velox integration secrets.
type VeloxConfig struct {
	// VeloxAPIToken authenticates artifact HEAD/GET requests back to Velox.
	// This is the REVERSE direction token (Velox → InstaEdit via
	// /internal/v1/* routes). Loaded from VELOX_API_TOKEN.
	VeloxAPIToken string

	// VeloxControlURL is the base URL of the Velox master that the
	// BFF calls when proxying user-facing /api/v1/velox/* requests.
	// The browser never sees this URL — only InstaEdit calls it.
	// Loaded from VELOX_CONTROL_URL. Empty = BFF routes not mounted.
	VeloxControlURL string

	// VeloxControlJWTSecret is the shared HS256 secret for the
	// short-lived JWT InstaEdit signs when calling the Velox master.
	// This MUST be the same value as VeloxEditiingg's
	// INSTAEDIT_CONTROL_JWT_SECRET. It is DISTINCT from
	// VeloxAPIToken (the reverse-direction Bearer token) — the two
	// secrets MUST NOT be reused across directions. Loaded from
	// VELOX_CONTROL_JWT_SECRET. Empty = BFF routes not mounted.
	VeloxControlJWTSecret string

	// VeloxWebhookSecret is the shared HMAC-SHA256 secret used to
	// sign callbacks sent from InstaEdit to Velox. It is distinct
	// from VeloxAPIToken and VeloxControlJWTSecret. Loaded from
	// VELOX_WEBHOOK_SECRET.
	VeloxWebhookSecret string
}

// AIConfig holds AI/ML provider secrets for metadata generation,
// translations, and other AI-assisted features. These keys are
// server-side only — NEVER exposed to the frontend bundle, logs,
// or localStorage. The absence of these keys MUST NOT block manual
// metadata entry or the YouTube publish flow.
type AIConfig struct {
	// NVIDIAAPIKey authenticates calls to the NVIDIA AI API for
	// generating title, description, tags, and translations.
	// Loaded from NVIDIA_API_KEY. Empty = AI metadata generation
	// unavailable (fallback: manual entry in Dark Editor).
	NVIDIAAPIKey string
}
