package services

import "github.com/Marcuss-ops/InstaeditLogin/internal/models"

// TokenPolicyProvider is implemented by providers that want to declare
// which token types are relevant for their account lifecycle. The OAuth
// callback and account validation handlers use this list to decide which
// stored tokens to check, rather than hardcoding platform-specific token
// types in pkg/api.
type TokenPolicyProvider interface {
	NameProvider

	// PreferredTokenTypes returns the ordered list of token types the
	// provider may store in the credential vault. Validation checks each
	// type in order; the first non-error token marks the account as
	// active. Providers should list their canonical token type first.
	PreferredTokenTypes() []string
}

// DefaultTokenTypes is the fallback list used when a provider does not
// implement TokenPolicyProvider. It covers the union of token types
// currently used across all platforms.
func DefaultTokenTypes() []string {
	return []string{
		models.TokenTypeBearer,
		models.TokenTypeShortLived,
		models.TokenTypeLongLived,
		models.TokenTypePageAccess,
	}
}
