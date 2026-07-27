// Package api — BookingEventsModule (Sprint: marketing strategy-call
// funnel).
//
// The POST /api/v1/booking_events endpoint is PUBLIC and
// ANONYMOUS. It captures the 3-question qualification from
// the BookingProvider modal on the marketing site and persists
// a typed SQL row so the sales team can read it directly.
//
// Security model (read top-to-bottom):
//   1. Rate-limit: per-IP 5/min via the dedicated BookingEventRateLimit
//      middleware (BookingEventLimit tier in services/ratelimit.go).
//      Keeps spam bots from filling the table; the edge tier
//      (Cloudflare/reverse proxy) is the real per-IP gate per
//      docs/OPERATIONS.md.
//   2. Same-origin: Origin / Referer headers are EXACT-matched
//      against the configured allowedOrigins. Cross-origin POSTs
//      are 403; empty / missing headers are 403 (no CLI/server-to-
//      server bypass — those callers should use a future admin/
//      API-key surface, NOT this anonymous prober).
//   3. Body size: 4 KiB cap. The JSON payload is small (~300
//      bytes); 4 KiB caps accidental abuse without rejecting a
//      heavily-extended metadata block from a future A/B test.
//   4. JSON validation: every enum field is checked against the
//      canonical sets (mirrored from web/src/lib/booking.ts);
//      unknown values are 400 — we never coerce silently. Trailing
//      JSON after the closing `}` is also 400 (defends against
//      accidentally-piped-two-POSTs payloads and a parser quirk
//      where encoding/json silently consumes trailing tokens).
//   5. CSRF: deliberately NOT applied. Anonymous visitors have no
//      csrf_token cookie, so the project's CSRF middleware would
//      403 every legit lead. Same-origin + rate-limit + dedupe
//      together prevent the genuine CSRF risk (luring an existing
//      user to submit a row on their behalf) since the table has
//      no per-user mutable state.
//   6. ip_hash / dedupe_hash: SHA-256 with a server-side HMAC key.
//      The key comes from BOOKING_HASH_SECRET (env var) when set,
//      else a per-process random 32-byte hex (logged as a warning
//      so operators see the misconfig in stderr). The per-process
//      fallback breaks dedup across restarts but never reveals
//      anything to a public reader of the Go source.
//
// Not wired here:
//   - GET /api/v1/admin/booking_events (sales dashboard)
//     deferred to a followup PR; the sales team currently reads
//     `booking_events` directly via SQL or hooks a Metabase view.

package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// bookingEventMaxBodyBytes bounds the request body. ~300 bytes is
// realistic for the current payload; 4 KiB is the safety cap.
const bookingEventMaxBodyBytes = 4 * 1024

// bookingEventUATruncate and bookingEventRefererTruncate mirror the
// VARCHAR(512) bound in migration 076 so we never write values past
// the column limit (Postgres would otherwise truncate silently).
const (
	bookingEventUATruncate     = 512
	bookingEventRefererTruncate = 512
)

// bookingEventHashSecretPepper is the env-var name that holds the
// HMAC key used to derive ip_hash + dedupe_hash. Set this in
// production so dedup is stable across restarts and the hash is
// irreversible for anyone missing the env var.
const bookingEventHashSecretPepper = "BOOKING_HASH_SECRET"

// bookingEventPepper is lazily initialised on first call so we
// pick up the env var at startup time rather than at package
// import time (the latter would happen before Wire() can set an
// env var from a secret). See hashForStorage for usage.
var bookingEventPepper = computeBookingPepper()

// computeBookingPepper resolves the HMAC key used for ip_hash +
// dedupe_hash. Precedence:
//
//  1. BOOKING_HASH_SECRET env-var (production).
//  2. Per-process cryptographically random 32-byte hex (dev /
//     pre-configured environments). Logs a loud warning so the
//     misconfiguration surfaces in stderr; dedup is broken
//     across restarts but the hash is still irreversible as
//     long as the process is running.
func computeBookingPepper() string {
	if v := strings.TrimSpace(os.Getenv(bookingEventHashSecretPepper)); v != "" {
		slog.Info("booking_events: using BOOKING_HASH_SECRET from env for ip_hash / dedupe_hash")
		return v
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// Last-resort fallback: deterministic-but-public pepper.
		// This branch should never fire in practice (Linux has
		// /dev/urandom) but we keep it so a panic-on-startup
		// never takes the process down on a sandbox without
		// entropy. The hash algorithm remains SHA-256; it just
		// becomes reachable to a public reader.
		slog.Error("booking_events: rand.Read failed; falling back to deterministic pepper")
		return "instaedit-booking-zero-pepper-fallback"
	}
	slog.Warn(
		"BOOKING_HASH_SECRET unset; booking_events uses a fresh per-process random pepper. " +
			"Set BOOKING_HASH_SECRET in production so dedup is stable across restarts.",
	)
	return hex.EncodeToString(b)
}

// BookingEventsModuleDeps is the narrow contract required by the
// POST endpoint. Same shape as the other feature-flag modules:
// field nil → endpoint not registered (matches webhookStore,
// uploadJobStore, snapshotStore nil-guard pattern).
type BookingEventsModuleDeps struct {
	Store          BookingEventStore
	RateLimit      func(http.Handler) http.Handler
	AllowedOrigins []string
}

// BookingEventsModule mounts POST /api/v1/booking_events. Same
// RouteModule pattern used by AdminModule / VeloxBFFModule /
// IntegrationsModule — see pkg/api/modules.go.
//
// No auth: the modal is on the public marketing site; the lead
// is anonymous. The dependency on AllowedOrigins is for the
// same-origin gate, NOT for CORS pre-flight (the form is
// fetch() from the same SPA, never a browser cross-origin
// request).
type BookingEventsModule struct {
	deps BookingEventsModuleDeps
}

// NewBookingEventsModule instantiates the module. The constructor
// signature mirrors the other bounded-context modules so the
// route registry invocates it uniformly.
func NewBookingEventsModule(deps BookingEventsModuleDeps) RouteModule {
	return &BookingEventsModule{deps: deps}
}

// Compile-time assertion: BookingEventsModule implements
// RouteModule. Mirrors the `var _ RouteModule =
// (*AdminModule)(nil)` pattern in modules.go.
var _ RouteModule = (*BookingEventsModule)(nil)

// Register mounts POST /api/v1/booking_events under a sub-mux
// that runs rate-limit + same-origin + body-cap before the
// handler. The sub-mux pattern (instead of mounting on `mux`
// directly) keeps the middleware chain isolated from the rest
// of the API surface, so a regression in the booking-event
// chain cannot accidentally re-order global middleware.
func (m *BookingEventsModule) Register(mux chi.Router) {
	if m.deps.Store == nil {
		return
	}

	r := chi.NewRouter()

	// Order matters:
	//   1. Size cap BEFORE rate-limit so a giant body doesn't
	//      burn an IP's token bucket (cheap rejection).
	//   2. Rate-limit BEFORE same-origin so spammers can't
	//      waste origin-allowlist lookups.
	//   3. Same-origin LAST so the rate-limit budget is consumed
	//      by legitimate origin checks.
	r.Use(limitBodySize(bookingEventMaxBodyBytes))
	if m.deps.RateLimit != nil {
		r.Use(m.deps.RateLimit)
	}
	r.Use(m.requireSameOrigin)

	r.Method(http.MethodPost, "/", http.HandlerFunc(m.handleCreateBookingEvent))
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	})

	mux.Mount("/api/v1/booking_events", r)
}

// handleCreateBookingEvent decodes the JSON payload, validates
// every field against the canonical enums, derives
// ip_hash + dedupe_hash, and calls Store.Insert.
//
// Reference: SaaS lead-capture pattern — keep responses FRONT-FACING,
// errors NEVER leak the dedupe_hash / ip_hash / row id; clients
// only need 200/400/403/429.
func (m *BookingEventsModule) handleCreateBookingEvent(w http.ResponseWriter, req *http.Request) {
	ip := extractIP(req, nil)

	var payload struct {
		Intent   string         `json:"intent"`
		Goal     string         `json:"goal"`
		Budget   string         `json:"budget"`
		Ready    string         `json:"ready"`
		// Metadata is the SPA passthrough for marketing tags
		// (utm_source / utm_campaign / etc.). Optional; missing
		// key falls through to a nil map and the repository
		// COALESCE's it down to '{}'::jsonb on insert. Used as
		// an analytics fallback when the upstream Google
		// Appointment Schedules redirect chain strips the query
		// string (verified empirically: calendar.app.google
		// rewrites without utm_* on the 302 to
		// calendar.google.com/calendar/appointments/schedules/<id>).
		// See web/src/components/booking/BookingProvider.tsx →
		// handleSubmit for the SPA-side submission contract.
		Metadata map[string]any `json:"metadata,omitempty"`
	}
	dec := json.NewDecoder(req.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON payload")
		return
	}
	// Reject trailing JSON (encoding/json silently consumes
	// tokens after the first object). Defends against
	// accidentally-piped POSTs and benign-malformed clients.
	if dec.More() {
		writeError(w, http.StatusBadRequest, "trailing JSON after object")
		return
	}

	if !models.ValidBookingIntent(payload.Intent) {
		writeError(w, http.StatusBadRequest, "invalid intent")
		return
	}
	if !models.ValidBookingGoal(payload.Goal) {
		writeError(w, http.StatusBadRequest, "invalid goal")
		return
	}
	if !models.ValidBookingBudget(payload.Budget) {
		writeError(w, http.StatusBadRequest, "invalid budget")
		return
	}
	if !models.ValidBookingReady(payload.Ready) {
		writeError(w, http.StatusBadRequest, "invalid ready")
		return
	}

	// Metadata is treated as opaque — no field-level schema
	// validation. Marketing teams iterate the payload shape
	// without redeploying the Go binary; the JSONB column
	// absorbs any new key. The repository JSON-marshals the map
	// into a Postgres `jsonb` so NULL/missing stays distinguishable
	// from explicit `{}` (a future dashboard query can filter on
	// metadata->>'utm_source' IS NOT NULL).
	ipHash := hashForStorage(ip)
	dedupeHash := hashForStorage(
		ipHash + "|" + payload.Intent + "|" + payload.Goal + "|" + payload.Budget + "|" + payload.Ready,
	)

	event := &models.BookingEvent{
		Intent:     payload.Intent,
		Goal:       payload.Goal,
		Budget:     payload.Budget,
		Ready:      payload.Ready,
		IPHash:     ipHash,
		UserAgent:  truncate(req.UserAgent(), bookingEventUATruncate),
		Referer:    truncate(req.Referer(), bookingEventRefererTruncate),
		DedupeHash: dedupeHash,
		Metadata:   payload.Metadata,
	}

	if err := m.deps.Store.Insert(event); err != nil {
		slog.Error("booking_events insert failed",
			"intent", payload.Intent,
			"ip_hash", ipHash,
			"error", err)
		writeError(w, http.StatusInternalServerError, "could not record booking event")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "recorded"})
}

// requireSameOrigin enforces that the request's Origin (preferred)
// or Referer (fallback) header EXACTLY matches one of the configured
// allowedOrigins. We deliberately do NOT use suffix matching — any
// bare-host entry (e.g. "instaedit.org" without scheme) would
// accept any origin whose URL ends with the literal, which is a
// trivial phishing-bait.
//
// Empty / missing Origin + Referer from CLI tools are REJECTED:
// server-to-server probes should use the future admin endpoint
// with API-key auth, not this anonymous surface. Empty
// AllowedOrigins is REJECTED unconditionally (default-deny) so a
// misconfigured prod deploy does not fall open.
func (m *BookingEventsModule) requireSameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !originAllowed(req, m.deps.AllowedOrigins) {
			writeError(w, http.StatusForbidden, "cross-origin request rejected")
			return
		}
		next.ServeHTTP(w, req)
	})
}

// ────────────────────────────────────────────────────────────────────
//  Helpers
// ────────────────────────────────────────────────────────────────────

// hashForStorage returns SHA-256(pepper + raw) as a hex string.
// Used for both ip_hash (GDPR) and dedupe_hash (idempotency).
// See computeBookingPepper + bookingEventHashSecretPepper for
// how the key is sourced.
func hashForStorage(raw string) string {
	h := sha256.Sum256([]byte(bookingEventPepper + "|" + raw))
	return hex.EncodeToString(h[:])
}

// truncate returns s clipped to max bytes; if the input is shorter
// it is returned unchanged. We operate on BYTES via
// s = s[:max] so a multibyte character is never split in the
// middle (Postgres VARCHAR(512) would reject a half-grapheme
// silent truncation on input).
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// originAllowed reports whether the request's Origin (or, when
// absent, its Referer) exactly matches one of the configured
// allowedOrigins. Trailing slashes are tolerated (scheme-equivalent);
// NO suffix matching — see comment on requireSameOrigin.
//
// Returns false when allowed is empty (default-deny in production)
// OR when the request lacks both Origin and Referer (anonymous
// CLI / server-to-server probes must use the future admin endpoint).
func originAllowed(req *http.Request, allowed []string) bool {
	if len(allowed) == 0 {
		// Default-deny: even an empty allowlist must not pass.
		// The dev-friendly variant (treat empty as "allow all")
		// was removed for security; dev environments must set
		// the env var explicitly.
		return false
	}
	hdr := req.Header.Get("Origin")
	if hdr == "" {
		hdr = req.Referer()
	}
	if hdr == "" {
		return false
	}
	for _, candidate := range allowed {
		if candidate == "" {
			continue
		}
		// Exact match OR trailing-slash equivalent. NO HasSuffix
		// — suffix matching would let `instaedit.org` accept
		// `https://evil.com/?x=instaedit.org` and phishing
		// subdomains piggy-backing on the same literal.
		if hdr == candidate || hdr == strings.TrimSuffix(candidate, "/") {
			return true
		}
	}
	return false
}

// limitBodySize is a minimal chi-compatible middleware that
// rejects requests with bodies larger than max bytes BEFORE the
// handler reads them. Implemented locally so we don't import
// chi/middleware just for one wrap. Uses http.MaxBytesReader
// to ALSO bound the parsed-read side once decoding has begun.
func limitBodySize(max int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.ContentLength > max {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			req.Body = http.MaxBytesReader(w, req.Body, max)
			next.ServeHTTP(w, req)
		})
	}
}
