package services

import (
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// IsEligibleForActivePromotion is the SINGLE source of truth for the
// eligibility gate that decides whether a platform_account row may
// be flipped to models.AccountStatusActive.
//
// Allow-list (pre-OAuth-callback active promotion):
//   - AccountStatusPendingAuthorization: CSV-import reset (P2 —
//     admin connect-link happy path).
//   - AccountStatusActive: refresh-on-the-same-grant via re-consent
//     (P1 — double-autorizzazione stesso canale).
//   - AccountStatusReauthRequired: previous refresh failed, operator
//     clicked "reconnect" and the new code-exchange succeeded.
//
// Explicitly excluded (intentional):
//   - AccountStatusExpired: today's only minter is the worker via
//     vault.Renew; reconnect-from-expired through OAuth must first
//     flip to reauth_required via the disconnect → reconnect flow.
//     Adding 'expired' here would risk resurrecting an account whose
//     grant has been lost.
//   - AccountStatusRevoked: admin/operator-initiated revocation;
//     reversal is a deliberate action, not an OAuth callback.
//   - AccountStatusDisconnected: user-initiated revoke; reversal
//     requires a fresh connect-link, not a token-exchange callback.
//   - AccountStatusError: transient-stamp sentinel; the row needs
//     operator visibility before any promotion to active.
//
// Empty/unknown status is treated as INELIGIBLE (returns false) so
// the AuthorizeChannel caller rejects unrecognised values rather
// than silently widening the gate.
//
// Cross-references:
//   - internal/services/channel_authorization.go::AuthorizeChannel
//     (sole current caller; protects from drift across future callers
//     like an admin re-auth tool or a worker reconnect handler)
//   - internal/services/eligibility_gate_test.go::TestIsEligibleForActivePromotion
//     (table-driven coverage of the allow-list + explicit rejections)
func IsEligibleForActivePromotion(status string) bool {
	switch status {
	case models.AccountStatusPendingAuthorization,
		models.AccountStatusActive,
		models.AccountStatusReauthRequired:
		return true
	default:
		return false
	}
}
