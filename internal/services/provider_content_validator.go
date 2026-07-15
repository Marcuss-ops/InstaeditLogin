package services

import "github.com/Marcuss-ops/InstaeditLogin/internal/models"

// ContentValidator validates that a publish payload is acceptable for the
// platform (e.g. YouTube requires a video, LinkedIn requires text).
// Every provider implements this so the per-platform Publish() can
// short-circuit before calling the upstream platform API.
//
// The interface declaration is retained because all 7 oauth_* files
// use `var _ ContentValidator = (*X)(nil)` as a compile-time
// conformance check, and each implementation is self-called inside
// Publish (e.g. linkedin's `s.ValidateContent(payload)` at the top of
// Publish). The router-side tracking of this capability (the
// capabilities.validate field + Validator(name) accessor +
// Register's type-assertion) was pruned in this commit because no
// external consumer queries `router.Validator(name)` today. Re-adding
// the accessor when a consumer arrives is a 5-line change.
type ContentValidator interface {
	// ValidateContent returns nil if the payload can be published, or a
	// descriptive error if a field is missing or out of range.
	ValidateContent(payload models.PublishPayload) error
}
