// Package capabilities loads + serves the theoretical capability
// matrix (Taglio 5.0 LEVEL 1) -- the per-platform rules that decide
// whether a publish payload is acceptable before the per-platform
// Publish call round-trip.
//
// The matrix is loaded once at startup from config/capabilities.json
// (a small, hand-edited file). Missing file -> falls back to
// WithDefaults(platform) on every lookup so the server boots in dev
// mode without an explicit matrix file.
//
// Level 2 (real per-account caps) reuses this same Matrix type:
// the worker computes effective = Intersect(theoretical, real) and
// pipes it into PrePublishCheckWithEffective. The matrix itself
// does not grow -- Level 2 is a discoverer + repository-cache +
// worker-side intersect.
package capabilities

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// Matrix holds default and per-platform theoretical capabilities.
// Loaded once at startup from config/capabilities.json.
type Matrix struct {
	Defaults  models.CapabilitySet            `json:"_defaults"`
	Platforms map[string]models.CapabilitySet `json:"-"`
}

// rawMatrix is the on-disk JSON shape: a flat map keyed by platform
// name (or "_defaults").
type rawMatrix map[string]models.CapabilitySet

// LoadFromFile reads the capability matrix JSON from disk.
func LoadFromFile(path string) (*Matrix, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("capabilities: read %s: %w", path, err)
	}
	var raw rawMatrix
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("capabilities: parse %s: %w", path, err)
	}
	m := &Matrix{Platforms: make(map[string]models.CapabilitySet, len(raw))}
	for k, v := range raw {
		if k == "_defaults" {
			m.Defaults = v
			continue
		}
		m.Platforms[k] = v
	}
	return m, nil
}

// For returns capabilities for a platform name. KNOWN platforms with
// an entry in the JSON resolve to that row verbatim. UNKNOWN
// platforms log a slog.Warn (so config typos / new-platform-not-
// yet-added-in-JSON are visible) and fall back to WithDefaults(platform)
// which is fail-closed for unrecognized platforms.
func (m *Matrix) For(platform string) models.CapabilitySet {
	if c, ok := m.Platforms[platform]; ok {
		return c
	}
	slog.Warn("capabilities: unknown platform lookup, falling back to closed defaults", "platform", platform)
	return WithDefaults(platform)
}

// WithDefaults is the FAIL-CLOSED safety net used when the matrix
// is nil (e.g. before BuildRegistry wires it) or when an unknown
// platform name is looked up. Per the L1 close-out, the JSON in
// config/capabilities.json is the SINGLE source of truth for
// per-platform caps; WithDefaults intentionally returns the same
// fail-closed shape for every platform name (known and unknown
// alike) to eliminate the prior implementation's per-platform
// hardcoded duplication of the JSON. If a caller hits this
// function, they are missing the matrix wiring -- fix it by
// setting CapabilitiesMatrixPath in config and confirming the
// registry's SetMatrix call succeeds; otherwise the safety net
// here rejects all non-text-only payloads.
//
// Production path: BuildRegistry loads from cfg.CapabilitiesMatrixPath
// and calls router.SetMatrix(matrix). The router.PrePublishCheck /
// router.Validator paths then read from matrix.For(platform) and
// never fall through to this function in the happy path. WithDefaults
// remains the only fallback for unit tests and dev servers that
// don't wire a matrix file.
func WithDefaults(platform string) models.CapabilitySet {
	_ = platform
	return models.CapabilitySet{TextOnly: true}
}
