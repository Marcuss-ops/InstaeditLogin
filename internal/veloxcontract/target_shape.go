package veloxcontract

import (
	"errors"
	"strings"
)

// Sentinel errors returned by ValidateTargetShape. The structural
// DECISION lives here (single source of truth); each HTTP boundary maps
// these to its own wire message via errors.Is so response text stays
// stable per endpoint while the rules themselves can never drift apart
// again.
var (
	// ErrTargetChannelRequiresIdentifier: a channel target carried no
	// usable identifier under the active shape policy.
	ErrTargetChannelRequiresIdentifier = errors.New("target channel requires an identifier")
	// ErrTargetChannelHasGroupFields: a channel target mixed in group
	// discriminator fields.
	ErrTargetChannelHasGroupFields = errors.New("channel target cannot include group fields")
	// ErrTargetGroupRequiresIdentifier: a group target carried neither
	// group_id nor group_name.
	ErrTargetGroupRequiresIdentifier = errors.New("target group requires an identifier")
	// ErrTargetGroupHasChannelFields: a group target mixed in channel
	// discriminator fields.
	ErrTargetGroupHasChannelFields = errors.New("group target cannot include channel fields")
	// ErrTargetTypeUnsupported: target.type is neither channel nor
	// group. Boundaries that accept additional shapes (e.g. catalog)
	// must dispatch those BEFORE calling ValidateTargetShape.
	ErrTargetTypeUnsupported = errors.New("target.type must be channel or group")
)

// ValidateTargetShape is the single structural validator for the
// channel/group PublicationTarget discriminator shared by every
// boundary that accepts publication targets.
//
// History: the same rules were previously duplicated in
// JobSubmissionRequest.ValidateCanonical, the internal resolve-target
// handler, and (partially) the BFF channel_name resolution closure.
// The copies had already diverged — the canonical envelope accepted a
// bare channel_name while the resolve-target endpoint required
// platform_account_id OR channel_id — so the same payload could be
// valid on one endpoint and 422 on another. This function is now the
// only place that decides target shape validity.
//
// allowChannelNameOnly selects the one deliberate per-boundary
// difference: the canonical job envelope accepts a channel_name-only
// target (the BFF resolves it to a concrete account before the job is
// forwarded), while the internal resolve-target endpoint demands an
// explicit identifier because it resolves immediately.
//
// Catalog and other non-channel/group types are NOT handled here;
// boundaries owning those shapes must dispatch them before calling
// this validator (they will otherwise receive
// ErrTargetTypeUnsupported).
func ValidateTargetShape(t PublicationTarget, allowChannelNameOnly bool) error {
	switch strings.TrimSpace(t.Type) {
	case "channel":
		hasIdentifier := t.PlatformAccountID > 0 || strings.TrimSpace(t.ChannelID) != ""
		if !hasIdentifier && !(allowChannelNameOnly && strings.TrimSpace(t.ChannelName) != "") {
			return ErrTargetChannelRequiresIdentifier
		}
		if t.GroupID != 0 || strings.TrimSpace(t.GroupName) != "" {
			return ErrTargetChannelHasGroupFields
		}
	case "group":
		if t.GroupID <= 0 && strings.TrimSpace(t.GroupName) == "" {
			return ErrTargetGroupRequiresIdentifier
		}
		if strings.TrimSpace(t.ChannelID) != "" || strings.TrimSpace(t.ChannelName) != "" {
			return ErrTargetGroupHasChannelFields
		}
	default:
		return ErrTargetTypeUnsupported
	}
	return nil
}
