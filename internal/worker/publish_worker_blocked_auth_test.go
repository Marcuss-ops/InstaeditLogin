// Package worker — Task 2/10 markPublishBlockedAuth helper tests.
//
// markPublishBlockedAuth is the post_target channel-drift transition
// helper (publish_worker.go). Distinct from markFailed: routes to
// PostStatusBlockedAuth AND stamps LastErrorCode="blocked_auth" so
// the operator dashboard's "what's pending reauth?" query can
// answer it without scanning ErrorMessage prose.
//
// Why a dedicated test file (not appended to publish_worker_test.go):
// the latter file holds the driver-path publishTarget tests and
// already strains the 20k-token / file-size budget. Splitting the
// helper-level tests into a leaf file mirrors artifact_verify_test.go
// (Task 4/10) and keeps each test category readable in isolation.
// No additional mock wiring — the shared fixtures in mocks_test.go
// (mockPostStore, mockProvider, mockCredentialVault, newTestWorker)
// are reused verbatim.
package worker

import (
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// TestMarkPublishBlockedAuth_Helper covers the Task 2/10 helper:
// transition target to PostStatusBlockedAuth + stamp
// LastErrorCode='blocked_auth' + UpdateStatus captured.
//
// Distinct from markFailed:
//
//   - status: blocked_auth (NOT failed; operator dashboard filter)
//   - LastErrorCode: "blocked_auth" (stable short code for
//     dashboard indexes; mirrors migration-018's
//     last_error_code pattern)
//   - ErrorMessage: full human prose (preserved verbatim from the
//     caller-supplied reason)
//
// The returned error MUST be non-nil so the tick counter increments
// in publishTarget; the wrapper's audit log surfaces the failure
// at WARN level.
func TestMarkPublishBlockedAuth_Helper(t *testing.T) {
	posts := &mockPostStore{}
	users := &mockUserStore{}
	svc := &mockProvider{baseMockProvider: baseMockProvider{platform: "youtube"}}
	vault := &mockCredentialVault{}
	w := newTestWorker(posts, users, "youtube", svc, vault)

	target := &models.PostTarget{
		ID:                7,
		PostID:            100,
		PlatformAccountID: 10,
		Status:            models.PostStatusPublishing,
	}
	reason := "youtube channel binding check: drift detected"

	if err := w.markPublishBlockedAuth(target, reason); err == nil {
		t.Fatal("markPublishBlockedAuth must return a non-nil error so the tick increments")
	}

	// In-memory target mutated correctly.
	if target.Status != models.PostStatusBlockedAuth {
		t.Errorf("target.Status: want blocked_auth, got %q", target.Status)
	}
	if target.LastErrorCode != "blocked_auth" {
		t.Errorf("target.LastErrorCode: want %q (operator dashboard filter), got %q", "blocked_auth", target.LastErrorCode)
	}
	if target.ErrorMessage != reason {
		t.Errorf("target.ErrorMessage: want %q, got %q", reason, target.ErrorMessage)
	}

	// UpdateStatus fired exactly once with the same shape.
	if posts.updateCalls != 1 {
		t.Fatalf("UpdateStatus calls: want 1, got %d", posts.updateCalls)
	}
	if len(posts.updateTargets) != 1 {
		t.Fatalf("UpdateStatus captures: want 1, got %d", len(posts.updateTargets))
	}
	final := posts.updateTargets[0]
	if final.Status != models.PostStatusBlockedAuth {
		t.Errorf("captured Status: want blocked_auth, got %q", final.Status)
	}
	if final.LastErrorCode != "blocked_auth" {
		t.Errorf("captured LastErrorCode: want %q, got %q", "blocked_auth", final.LastErrorCode)
	}
	if final.ErrorMessage != reason {
		t.Errorf("captured ErrorMessage: want %q, got %q", reason, final.ErrorMessage)
	}
}

// TestMarkPublishBlockedAuth_DistinctFromMarkFailed is the regression
// guard for the helper-vs-markFailed split: the two MUST NOT collapse
// into one another in a future refactor. The captured shape on
// UpdateStatus (status value + LastErrorCode field) is the fork point;
// collapsing them would silently break the operator dashboard's
// "blocked_auth" filter (the SpEL/JPA query on
// dashboard.last_error_code='blocked_auth' would return zero rows
// for genuine channel-drift refusals).
func TestMarkPublishBlockedAuth_DistinctFromMarkFailed(t *testing.T) {
	posts := &mockPostStore{}
	users := &mockUserStore{}
	svc := &mockProvider{baseMockProvider: baseMockProvider{platform: "youtube"}}
	vault := &mockCredentialVault{}
	w := newTestWorker(posts, users, "youtube", svc, vault)

	target := &models.PostTarget{
		ID:                8,
		PostID:            100,
		PlatformAccountID: 10,
		Status:            models.PostStatusPublishing,
	}
	reason := "youtube channel binding check: drift"

	if err := w.markPublishBlockedAuth(target, reason); err == nil {
		t.Fatal("markPublishBlockedAuth must return a non-nil error so the tick increments")
	}

	// Critical assertions: blocked_auth + LastErrorCode='blocked_auth'
	// distinguishes this from markFailed. A regression that re-uses
	// markFailed's body would set status=PostStatusFailed and leave
	// LastErrorCode="" — both assertions below would fire.
	if target.Status == models.PostStatusFailed {
		t.Errorf("markPublishBlockedAuth must NOT write status=PostStatusFailed (use markFailed for generic failures); got %q", target.Status)
	}
	if target.LastErrorCode == "" {
		t.Errorf("markPublishBlockedAuth must stamp LastErrorCode='blocked_auth' (operator dashboard filter); got empty string")
	}
}

// TestMarkPublishBlockedAuth_UpdateStatusErrorSwallowed matches
// markFailed's contract: an UpdateStatus error from the
// repository is intentionally ignored so the returned error reflects
// the underlying reason, not the bookkeeping error. Test fixture:
// mockPostStore.updateStatusFn returns an error; the helper must
// still return errors.New(reason) and not surface the bookkeeping
// error.
func TestMarkPublishBlockedAuth_UpdateStatusErrorSwallowed(t *testing.T) {
	posts := &mockPostStore{
		updateStatusFn: func(*models.PostTarget) error {
			return repository.ErrPostTargetNotFound // standalone-in-error (canonical sentinel lives in repository package)
		},
	}
	users := &mockUserStore{}
	svc := &mockProvider{baseMockProvider: baseMockProvider{platform: "youtube"}}
	vault := &mockCredentialVault{}
	w := newTestWorker(posts, users, "youtube", svc, vault)

	target := &models.PostTarget{ID: 9, PostID: 100, PlatformAccountID: 10, Status: models.PostStatusPublishing}
	reason := "youtube channel binding check: drift"
	err := w.markPublishBlockedAuth(target, reason)
	if err == nil {
		t.Fatal("helper must return non-nil error so tick counter increments")
	}
	if err.Error() != reason {
		t.Errorf("returned error: want %q (the underlying reason), got %q", reason, err.Error())
	}
	// The in-memory target is still mutated correctly even when
	// UpdateStatus failed — the worker writes inmemory-first +
	// repository-second so a transient DB blip doesn't lose the
	// status transition for caller-internal assertions.
	if target.Status != models.PostStatusBlockedAuth {
		t.Errorf("target.Status: want blocked_auth, got %q (UpdateStatus failure must NOT roll back the inmemory transition)", target.Status)
	}
}
