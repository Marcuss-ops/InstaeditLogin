package services

import (
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// TestIsEligibleForActivePromotion is the table-driven unit test for
// the eligibility gate (the single source of truth that every caller
// — AuthorizeChannel today, future worker reconnect handlers or
// admin re-auth tools tomorrow — MUST route through). The cases
// cover all 7 known AccountStatus constants + 2 defensive cases
// (empty string + an unknown literal) so a regression that
// accidentally widens the gate (e.g., adding 'expired' or
// 'disconnected' back into the allow-list) fails this test
// immediately, regardless of whether AuthorizeChannel's own flow is
// exercised by an integration test.
//
// Lock-down rationale (per the helper's doc-comment + the allow-list
// policy): pending_authorization / active / reauth_required are the
// three meaningful OAuth-callback transitions. The four "excluded"
// statuses are intentionally rejected — each has a separate reversal
// path that does NOT cross this gate (admin tool for revoked, fresh
// connect-link for disconnected, operator visibility for error,
// disconnect → reconnect for expired).
func TestIsEligibleForActivePromotion(t *testing.T) {
	cases := []struct {
		name     string
		status   string
		expected bool
	}{
		// ---- eligible (the allow-list) ----
		{"eligible/pending_authorization", models.AccountStatusPendingAuthorization, true},
		{"eligible/active", models.AccountStatusActive, true},
		{"eligible/reauth_required", models.AccountStatusReauthRequired, true},

		// ---- explicitly excluded (the gate trips) ----
		{"ineligible/expired", models.AccountStatusExpired, false},
		{"ineligible/revoked", models.AccountStatusRevoked, false},
		{"ineligible/disconnected", models.AccountStatusDisconnected, false},
		{"ineligible/error", models.AccountStatusError, false},

		// ---- defensive: unknown / empty literal ----
		// An unrecognised status MUST NOT silently widen the gate. The
		// helper returns false so AuthorizeChannel rejects the row
		// rather than promoting it to active on an unknown value.
		{"ineligible/empty", "", false},
		{"ineligible/unknown_literal", "garbage_status_literal", false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := IsEligibleForActivePromotion(tc.status)
			if got != tc.expected {
				t.Errorf("IsEligibleForActivePromotion(%q): want %v, got %v",
					tc.status, tc.expected, got)
			}
		})
	}
}
