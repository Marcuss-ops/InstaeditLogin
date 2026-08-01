// Package deliveries — Unified TargetResolver for the Velox → InstaEdit
// destination validation and target resolution use case.
//
// Consolidates the duplicated validation logic previously spread across:
//   - pkg/api/velox_handlers.go::handleValidateInternalDestination
//     (POST /internal/v1/destinations/{id}/validate — id-based)
//   - pkg/api/destinations_resolve_target.go::resolveChannelTarget /
//     resolveGroupTarget
//     (POST /internal/v1/destinations/resolve-target — body-based)
//
// Both endpoints are now thin HTTP adapters that call
// TargetResolver.Resolve() and map the result to their respective
// wire shapes (204/404 for validate; 200/422 JSON for resolve-target).
//
// The resolver supports two input modes:
//   - SavedDestination (DestID): looks up an existing
//     external_destinations row, then validates workspace + account +
//     eligibility.
//   - DirectTarget (WorkspaceID + Platform + Target): resolves a
//     channel or group descriptor directly, without requiring a
//     stored destinations row.
//
// Both paths converge on the same eligibility checks (workspace
// existence, platform_account status, reauth_required, binding,
// disabled channel, group expansion) — eliminating the duplication
// the audit flagged.
package deliveries

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ---------------------------------------------------------------------------
// Narrow typed ports (Pattern 0) — defined locally so the resolver
// doesn't import pkg/api. Matches the group_expand.go pattern.
// ---------------------------------------------------------------------------

// TargetDestinationStore is the persistence contract for
// external_destinations lookups. Subset of the pkg/api
// ExternalDestinationStore interface.
type TargetDestinationStore interface {
	GetByID(ctx context.Context, id string) (*models.ExternalDestination, error)
}

// TargetWorkspaceStore is the persistence contract for workspace +
// binding checks. Subset of the pkg/api WorkspaceStore interface.
type TargetWorkspaceStore interface {
	FindByID(id int64) (*models.Workspace, error)
	FindChannel(ctx context.Context, workspaceID, platformAccountID int64) (*models.WorkspaceChannel, error)
	ListChannels(ctx context.Context, workspaceID int64) ([]models.WorkspaceChannel, error)
}

// TargetUserStore is the persistence contract for platform_account
// lookups. Subset of the pkg/api UserStore interface.
type TargetUserStore interface {
	FindPlatformAccountByID(id int64) (*models.PlatformAccount, error)
}

// TargetGroupStore is the persistence contract for group expansion.
// Subset of the pkg/api GroupStore interface.
type TargetGroupStore interface {
	FindByID(id int64) (*models.Group, error)
	ListAccountsInGroup(groupID int64) ([]int64, error)
}

// ---------------------------------------------------------------------------
// Resolver types
// ---------------------------------------------------------------------------

// TargetResolverDeps holds the four store references the resolver
// needs. Every field is required (nil stores cause the resolver to
// return an error on Resolve, not crash).
type TargetResolverDeps struct {
	DestinationStore TargetDestinationStore
	WorkspaceStore   TargetWorkspaceStore
	UserStore        TargetUserStore
	GroupStore       TargetGroupStore
}

// TargetResolver is the unified destination validation + target
// resolution use case. One Resolve method with two input modes
// (SavedDestination / DirectTarget) converging on the same
// workspace → account → binding → eligibility pipeline.
type TargetResolver struct {
	deps TargetResolverDeps
}

// NewTargetResolver creates a resolver. All deps are required; nil
// stores are checked at Resolve-time (not construction) so production
// wiring that passes nil for an optional store returns a clear error
// rather than panicking.
func NewTargetResolver(deps TargetResolverDeps) *TargetResolver {
	return &TargetResolver{deps: deps}
}

// ---------------------------------------------------------------------------
// Request / response types
// ---------------------------------------------------------------------------

// ResolveRequest is the canonical input shape for the resolver.
// Exactly one of the two input modes must be populated:
//
//   - SavedDestination: DestID is non-empty (id-based validate path).
//   - DirectTarget: WorkspaceID > 0 AND Platform is non-empty
//     AND Target is populated (body-based resolve-target path).
type ResolveRequest struct {
	// SavedDestination mode — look up an existing destinations row.
	DestID string

	// DirectTarget mode — resolve a channel/group descriptor.
	WorkspaceID int64
	Platform    string
	Target      TargetDescriptor
}

// TargetDescriptor carries the per-target resolution fields.
// Exactly one of {PlatformAccountID, ChannelID, GroupID} must be
// set, matching the target.type discriminator.
type TargetDescriptor struct {
	Type              string // "channel" or "group"
	PlatformAccountID int64
	ChannelID         string
	GroupID           int64
}

// ResolveResult is the canonical output shape. Both endpoints
// consume it and map to their specific wire shapes.
type ResolveResult struct {
	// Valid is the boolean pass/fail signal. The validate endpoint
	// maps this to 204/404; the resolve-target endpoint maps it to
	// the top-level valid field.
	Valid bool

	// ErrorCode is the narrow taxonomy used by resolve-target's
	// structured response. One of TARGET_NOT_AVAILABLE, GROUP_EMPTY,
	// BLOCKED_AUTH, or empty on success.
	ErrorCode string

	// Message is the human-readable companion to ErrorCode.
	Message string

	// DestinationID is the resolved destination identifier, populated
	// on success. For the SavedDestination path this is the row id;
	// for the DirectTarget path this is "instaedit_<platform>".
	DestinationID string

	// Platform is the resolved platform (always populated on success).
	Platform string

	// ResolvedTargets carries the per-target resolved entries. On
	// success this is a 1-or-more slice; on failure it may carry
	// partial entries (for group partial-failure visibility).
	ResolvedTargets []ResolvedTargetEntry

	// Diagnostic carries additional info for the validate endpoint's
	// ?diagnostic=true mode. nil when diagnostic mode is off.
	Diagnostic *DiagnosticInfo
}

// DiagnosticInfo carries extra detail for the validate endpoint's
// diagnostic JSON mode.
type DiagnosticInfo struct {
	Status   string
	Platform string
}

// ResolvedTargetEntry is one row in the resolved_targets array.
type ResolvedTargetEntry struct {
	PlatformAccountID int64  `json:"platform_account_id"`
	Platform          string `json:"platform"`
	ChannelID         string `json:"channel_id"`
	ChannelName       string `json:"channel_name,omitempty"`
	Status            string `json:"status"`
	Enabled           bool   `json:"enabled"`
	TargetErrorCode   string `json:"target_error_code,omitempty"`
}

// Error code taxonomy — shared with the resolve-target handler.
const (
	ErrCodeTargetNotAvailable = "TARGET_NOT_AVAILABLE"
	ErrCodeGroupEmpty         = "GROUP_EMPTY"
	ErrCodeBlockedAuth        = "BLOCKED_AUTH"
)

// Sentinel errors for the SavedDestination path (used by the
// validate handler to map to 404).
var (
	ErrDestinationNotFound = errors.New("destination not found")
	ErrWorkspaceNotFound   = errors.New("workspace not found")
	ErrAccountNotFound     = errors.New("platform_account not found")
	ErrAccountNotEligible  = errors.New("platform_account not eligible")
)

// ---------------------------------------------------------------------------
// Resolve — the single entry point
// ---------------------------------------------------------------------------

// Resolve is the single canonical validation entry point.
//
// Dispatches on input mode:
//   - DestID != "" → SavedDestination path (validate)
//   - WorkspaceID > 0 && Platform != "" → DirectTarget path (resolve-target)
//
// Returns a non-nil error for infra failures (DB down); returns a
// nil error + ResolveResult.Valid=false for domain rejection.
func (r *TargetResolver) Resolve(ctx context.Context, req ResolveRequest) (*ResolveResult, error) {
	// Mode dispatch.
	if req.DestID != "" {
		return r.resolveSavedDestination(ctx, req)
	}
	if req.WorkspaceID > 0 && req.Platform != "" {
		return r.resolveDirectTarget(ctx, req)
	}
	return nil, errors.New("target resolver: invalid request — DestID or (WorkspaceID + Platform) must be set")
}

// ---------------------------------------------------------------------------
// SavedDestination path (id-based validate)
// ---------------------------------------------------------------------------

func (r *TargetResolver) resolveSavedDestination(ctx context.Context, req ResolveRequest) (*ResolveResult, error) {
	if r.deps.DestinationStore == nil {
		return nil, errors.New("target resolver: DestinationStore not wired")
	}

	dest, err := r.deps.DestinationStore.GetByID(ctx, req.DestID)
	// dest==nil is checked BEFORE err!=nil because production repos
	// return (nil, ErrExternalDestinationNotFound) for missing rows.
	// The resolver treats any nil-dest result — regardless of error —
	// as a clean "not found" so the handler maps it to 404.
	if dest == nil || !dest.Enabled {
		return &ResolveResult{
			Valid:           false,
			ErrorCode:       ErrCodeTargetNotAvailable,
			Message:         ErrDestinationNotFound.Error(),
			ResolvedTargets: []ResolvedTargetEntry{},
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("target resolver: destination lookup failed: %w", err)
	}

	// Workspace check.
	if r.deps.WorkspaceStore == nil {
		return nil, errors.New("target resolver: WorkspaceStore not wired")
	}
	ws, err := r.deps.WorkspaceStore.FindByID(dest.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("target resolver: workspace lookup failed: %w", err)
	}
	if ws == nil {
		return &ResolveResult{
			Valid:           false,
			ErrorCode:       ErrCodeTargetNotAvailable,
			Message:         ErrWorkspaceNotFound.Error(),
			ResolvedTargets: []ResolvedTargetEntry{},
		}, nil
	}

	// Platform account check.
	if r.deps.UserStore == nil {
		return nil, errors.New("target resolver: UserStore not wired")
	}
	pa, err := r.deps.UserStore.FindPlatformAccountByID(dest.PlatformAccountID)
	if err != nil {
		return nil, fmt.Errorf("target resolver: platform_account lookup failed: %w", err)
	}
	if pa == nil {
		return &ResolveResult{
			Valid:           false,
			ErrorCode:       ErrCodeTargetNotAvailable,
			Message:         ErrAccountNotFound.Error(),
			ResolvedTargets: []ResolvedTargetEntry{},
		}, nil
	}

	// Binding check (for disabled channel visibility).
	binding, _ := r.deps.WorkspaceStore.FindChannel(ctx, dest.WorkspaceID, dest.PlatformAccountID)

	// Eligibility check — shared core.
	entry, eligibility := checkAccountEligibility(pa, binding)
	if !eligibility.Valid {
		return &ResolveResult{
			Valid:           false,
			ErrorCode:       eligibility.ErrorCode,
			Message:         eligibility.Message,
			ResolvedTargets: []ResolvedTargetEntry{entry},
			Diagnostic: &DiagnosticInfo{
				Status:   pa.Status,
				Platform: pa.Platform,
			},
		}, nil
	}

	return &ResolveResult{
		Valid:           true,
		DestinationID:   dest.ID,
		Platform:        pa.Platform,
		ResolvedTargets: []ResolvedTargetEntry{entry},
		Diagnostic: &DiagnosticInfo{
			Status:   pa.Status,
			Platform: pa.Platform,
		},
	}, nil
}

// ---------------------------------------------------------------------------
// DirectTarget path (body-based resolve-target)
// ---------------------------------------------------------------------------

func (r *TargetResolver) resolveDirectTarget(ctx context.Context, req ResolveRequest) (*ResolveResult, error) {
	// Step 1: structural validation done by caller (handler).
	// Step 2: workspace existence.
	if r.deps.WorkspaceStore == nil {
		return nil, errors.New("target resolver: WorkspaceStore not wired")
	}
	ws, err := r.deps.WorkspaceStore.FindByID(req.WorkspaceID)
	if err != nil {
		slog.Error("target resolver: workspace lookup failed",
			"workspace_id", req.WorkspaceID, "err", err)
		return nil, fmt.Errorf("workspace lookup failed: %w", err)
	}
	if ws == nil {
		return &ResolveResult{
			Valid:           false,
			ErrorCode:       ErrCodeTargetNotAvailable,
			Message:         "workspace not found",
			ResolvedTargets: []ResolvedTargetEntry{},
		}, nil
	}

	if r.deps.UserStore == nil {
		return nil, errors.New("target resolver: UserStore not wired")
	}

	// Step 3: dispatch on target type.
	switch req.Target.Type {
	case "channel":
		return r.resolveChannelTarget(ctx, req)
	case "group":
		return r.resolveGroupTarget(ctx, req)
	default:
		return nil, fmt.Errorf("target resolver: unsupported target type %q", req.Target.Type)
	}
}
