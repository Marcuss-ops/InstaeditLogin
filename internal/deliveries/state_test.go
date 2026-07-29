package deliveries

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestAllDeliveryStatesCount pins the 21-state total (14 happy
// path + 7 error exits). If a future refactor splits MERGED_STATES
// or adds PRIVATE_UPLOAD_RETRY, this assertion forces a decision:
// update the count AND the docs/velox-instaedit-contract.md §10
// matrix in the same commit.
func TestAllDeliveryStatesCount(t *testing.T) {
	all := AllDeliveryStates()
	const want = 21
	if len(all) != want {
		t.Fatalf("AllDeliveryStates returned %d states, want %d (happy-path + error-exits must sum to 21)", len(all), want)
	}
	// Distinctness: every element unique. Catches copy-paste duplicates.
	seen := make(map[DeliveryState]int, want)
	for _, s := range all {
		seen[s]++
	}
	for s, n := range seen {
		if n != 1 {
			t.Errorf("DeliveryState %q appears %d times in AllDeliveryStates", s, n)
		}
	}
}

// TestTransitionMapEnumCoverage guards against accidentally
// omitting a new enum value from the transitionMap. Every
// constant declared via DeliveryState<NAME> must appear in AllDeliveryStates
// AND have an entry in transitionMap (possibly empty for terminals).
// Mirrors the architecturally identical TestTransitionMapEnumCoverage
// on internal/models/external_delivery.go::ExternalDeliveryStatus.
func TestTransitionMapEnumCoverage(t *testing.T) {
	for _, s := range AllDeliveryStates() {
		_, ok := transitionMap[s]
		if !ok {
			t.Errorf("transitionMap missing entry for state %q", s)
		}
	}
	// Every key in transitionMap must appear in AllDeliveryStates —
	// no undocumented ghost states slipped into the graph.
	all := make(map[DeliveryState]bool, 21)
	for _, s := range AllDeliveryStates() {
		all[s] = true
	}
	for k := range transitionMap {
		if !all[k] {
			t.Errorf("transitionMap has key %q but AllDeliveryStates does not list it", k)
		}
	}
}

// TestHappyPathForwardWalk walks the canonical 14-state forward
// chain via Next() and asserts that each successor matches the
// expected name.
//
// The chain is documented in `docs/velox-instaedit-contract.md` §10:
//
//	DELIVERY_QUEUED → TARGET_VALIDATING → TARGET_VALIDATED →
//	MEDIA_DOWNLOADING → MEDIA_VERIFIED → PRIVATE_UPLOAD_QUEUED →
//	PRIVATE_UPLOADING → PRIVATE_UPLOADED → THUMBNAIL_PENDING →
//	THUMBNAIL_UPLOADING → THUMBNAIL_APPLIED → READY_TO_PUBLISH →
//	PUBLISHING → PUBLISHED
func TestHappyPathForwardWalk(t *testing.T) {
	wantChain := []DeliveryState{
		DeliveryStateDeliveryQueued,
		DeliveryStateTargetValidating,
		DeliveryStateTargetValidated,
		DeliveryStateMediaDownloading,
		DeliveryStateMediaVerified,
		DeliveryStatePrivateUploadQueued,
		DeliveryStatePrivateUploading,
		DeliveryStatePrivateUploaded,
		DeliveryStateThumbnailPending,
		DeliveryStateThumbnailUploading,
		DeliveryStateThumbnailApplied,
		DeliveryStateReadyToPublish,
		DeliveryStatePublishing,
		DeliveryStatePublished,
	}
	if len(wantChain) != 14 {
		t.Fatalf("github test fixture out of sync: wantChain=%d, expected 14 happy-path states", len(wantChain))
	}

	for i := 0; i < len(wantChain)-1; i++ {
		cur := wantChain[i]
		next := wantChain[i+1]
		got := cur.Next()
		if got != next {
			t.Errorf("happy path step %d→%d: Next()=%q, want %q", i+1, i+2, got, next)
		}
		if !cur.CanTransitionTo(next) {
			t.Errorf("happy path step %d→%d: CanTransitionTo returned false (regression)", i+1, i+2)
		}
	}

	// PUBLISHED is the canonical end of the happy chain; Next() must
	// return the zero value (terminal → no further forward step).
	if got := DeliveryStatePublished.Next(); got != "" {
		t.Errorf("PUBLISHED.Next()=%q, want zero (terminal)", got)
	}
}

// TestNextExcludesErrorExits confirms that Next() is the
// happy-path helper and never returns an error exit. Workers
// that rely on Next() must NOT use it to cross into terminal/error
// branches — those go via CanTransitionTo() directly.
func TestNextExcludesErrorExits(t *testing.T) {
	// Iterating through every non-error state, Next() must either
	// (a) return a happy-path successor or (b) return "" when the
	// state has no happy-path successors left.
	for _, s := range AllDeliveryStates() {
		n := s.Next()
		if n == "" {
			// Acceptable for terminal / post-error states with no
			// happy-path successor — but this is a property of the
			// graph, not Next() logic.
			continue
		}
		if n.IsErrorExit() {
			t.Errorf("Next() for non-error state %q returned error exit %q (Next is the HAPPY-PATH helper)", s, n)
		}
	}
}

// TestErrorExitsIsTerminal pins the seven states that end the
// pipeline without operator intervention as terminals. The 8th
// terminal is PUBLISHED (success terminus). isTerminal covers
// the 8 — both error and PUBLISHED.
func TestErrorExitsIsTerminal(t *testing.T) {
	errExits := []DeliveryState{
		DeliveryStateBlockedTarget,
		DeliveryStateBlockedAuth,
		DeliveryStateMediaInvalid,
		DeliveryStatePrivateUploadFailed,
		DeliveryStateThumbnailFailed,
		DeliveryStatePublishFailed,
		DeliveryStateCancelled,
	}
	if len(errExits) != 7 {
		t.Fatalf("github test fixture out of sync: errExits=%d, expected 7", len(errExits))
	}
	for _, e := range errExits {
		if !e.IsErrorExit() {
			t.Errorf("error state %q returned IsErrorExit()==false", e)
		}
		if !e.IsTerminal() {
			t.Errorf("error state %q returned IsTerminal()==false", e)
		}
		// Terminals must have no outgoing edges in the transitionMap.
		if got := e.LegalTransitions(); got != nil {
			t.Errorf("terminal error state %q has legal transitions: %v", e, got)
		}
		// Terminal error states MUST NOT be retryable (operator must
		// reconcile, worker pool should not chase them).
		if e.IsRetryable() {
			t.Errorf("terminal error state %q returned IsRetryable()==true", e)
		}
	}
}

// TestPublishedIsTerminalAndNotRetryable ensures the success
// terminus is treated as both terminal AND out of the worker
// pool. PUBLISHED must not be restated by the claim CTE on a
// future tick (a stuck row in this state would block visibility
// for an arbitrary time).
func TestPublishedIsTerminalAndNotRetryable(t *testing.T) {
	s := DeliveryStatePublished
	if !s.IsTerminal() {
		t.Errorf("PUBLISHED.IsTerminal()==false")
	}
	if s.IsRetryable() {
		t.Errorf("PUBLISHED.IsRetryable()==true")
	}
	if s.Next() != "" {
		t.Errorf("PUBLISHED.Next() must be zero, got %q", s.Next())
	}
	if got := s.LegalTransitions(); got != nil {
		t.Errorf("PUBLISHED has legal transitions: %v", got)
	}
}

// TestCanTransitionToRejectsEmpty/Invalid covers the defensive
// guards (matching the ExternalDeliveryStatus.CanTransitionTo
// pattern). Publishing an in-progress state with an empty target
// is a caller bug — refuse it rather than silently accept it.
func TestCanTransitionToRejectsEmpty(t *testing.T) {
	if DeliveryStateMediaVerified.CanTransitionTo("") {
		t.Errorf("CanTransitionTo(\"\") returned true for non-terminal source")
	}
	if DeliveryStateDeliveryQueued.CanTransitionTo("nonexistent_state") {
		t.Errorf("CanTransitionTo(\"nonexistent_state\") returned true")
	}
}

// TestIsPrivateFloorReachedBoundary is the SAFETY INVARIANT
// pin: every state from PRIVATE_UPLOADED onward (inclusive) is
// classified as "post-boundary"; nothing before PRIVATE_UPLOADED
// is. A row that crossed PRIVATE_UPLOADED but failed later must
// still hold privacy=private on the YouTube side, so the
// boundary classification is checked individually for every
// state.
//
// This is the single most important invariant in the spec —
// violating it (e.g. by mistakenly classifying MEDIA_VERIFIED as
// post-boundary) would silently allow PUBLISH to skip the
// video's privacy invariant.
func TestIsPrivateFloorReachedBoundary(t *testing.T) {
	cases := []struct {
		state DeliveryState
		want  bool
		why   string
	}{
		// Pre-boundary (false).
		{DeliveryStateDeliveryQueued, false, "queued pre-check"},
		{DeliveryStateTargetValidating, false, "destination not yet validated"},
		{DeliveryStateTargetValidated, false, "destination OK but no media yet"},
		{DeliveryStateMediaDownloading, false, "downloading"},
		{DeliveryStateMediaVerified, false, "verified but never uploaded"},
		{DeliveryStatePrivateUploadQueued, false, "queued for private upload"},
		{DeliveryStatePrivateUploading, false, "private upload in flight but not yet acknowledged"},
		// Boundary + post-boundary (true).
		{DeliveryStatePrivateUploaded, true, "B exactly — YouTube confirmed private"},
		{DeliveryStateThumbnailPending, true, "thumbnail orchestrator picking up"},
		{DeliveryStateThumbnailUploading, true, "thumbnail in flight"},
		{DeliveryStateThumbnailApplied, true, "thumbnail applied; ready_to_publish next"},
		{DeliveryStateReadyToPublish, true, "publish required"},
		{DeliveryStatePublishing, true, "videos.update in flight"},
		{DeliveryStatePublished, true, "success terminus is post-boundary (privacy was already enforced)"},
	}
	if len(cases) != 14 {
		t.Fatalf("github test fixture out of sync: private floor cases=%d, expected 14", len(cases))
	}
	for _, c := range cases {
		if got := c.state.IsPrivateFloorReached(); got != c.want {
			t.Errorf("IsPrivateFloorReached(%s) = %v, want %v (%s)", c.state, got, c.want, c.why)
		}
		expectedFloor := ""
		if c.want {
			expectedFloor = "private"
		}
		if got := c.state.PrivacyFloor(); got != expectedFloor {
			t.Errorf("PrivacyFloor(%s) = %q, want %q", c.state, got, expectedFloor)
		}
		// EnforcePrivacyInvariant must allow "private" (or empty)
		// post-boundary; everywhere else, it must allow ANY value.
		if c.want {
			if err := c.state.EnforcePrivacyInvariant("private"); err != nil {
				t.Errorf("EnforcePrivacyInvariant(%s, \"private\") returned %v (want nil)", c.state, err)
			}
			if err := c.state.EnforcePrivacyInvariant("public"); err == nil {
				t.Errorf("EnforcePrivacyInvariant(%s, \"public\") returned nil; should refuse non-private post-boundary", c.state)
			}
			if err := c.state.EnforcePrivacyInvariant("unlisted"); err == nil {
				t.Errorf("EnforcePrivacyInvariant(%s, \"unlisted\") returned nil; should refuse non-private post-boundary", c.state)
			}
		} else {
			// Pre-boundary: caller can choose any privacy because
			// YouTube has not been touched yet.
			if err := c.state.EnforcePrivacyInvariant("public"); err != nil {
				t.Errorf("EnforcePrivacyInvariant(%s, \"public\") returned %v; should be permissive pre-boundary", c.state, err)
			}
		}
	}
}

// TestPostBoundaryErrorsKeepPrivacy is the SPEC-defining
// invariant restated in the form of a contract: from any
// post-PRIVATE_UPLOADED state, every legal error transition
// (thumbnail_failed, publish_failed) refuses to enforce a
// non-private final_privacy. A worker that tried to set
// privacy=public while transitioning private_uploaded →
// thumbnail_failed would re-publish a stuck video; the
// EnforcePrivacyInvariant prevents that, and PublicFloor is the
// guard inside the persistence layer.
//
// This test also confirms the SAFETY EDGE property: the FAIL
// paths post-PRIVATE_UPLOADED do NOT include any privacy-moving
// target — once you're past PRIVATE_UPLOADED the producer can
// never regress to a less-private wrong way.
func TestPostBoundaryErrorsKeepPrivacy(t *testing.T) {
	postBoundary := []DeliveryState{
		DeliveryStatePrivateUploaded,
		DeliveryStateThumbnailPending,
		DeliveryStateThumbnailUploading,
		DeliveryStateThumbnailApplied,
		DeliveryStateReadyToPublish,
		DeliveryStatePublishing,
	}
	for _, s := range postBoundary {
		// PrivacyFloor must always be "private" post-boundary.
		if got := s.PrivacyFloor(); got != "private" {
			t.Errorf("post-boundary %s PrivacyFloor()=%q, want \"private\"", s, got)
		}
		// If a transition tried to set public/unlisted, invariant refuses.
		for _, bad := range []string{"public", "unlisted", "PUBLIC", "Unlisted"} {
			err := s.EnforcePrivacyInvariant(bad)
			if err == nil {
				t.Errorf("EnforcePrivacyInvariant(%s, %q) returned nil; must refuse to republish past the safety edge", s, bad)
			}
			if !strings.Contains(err.Error(), "privacy invariant") {
				t.Errorf("EnforcePrivacyInvariant(%s, %q) error %q should contain \"privacy invariant\"", s, bad, err.Error())
			}
		}
	}
}

// TestAllPostBoundaryErrorsAreLegalTransitions asserts that
// thumbnail_failed is reachable from each of the post-boundary
// states where it's natural (private_uploaded, thumbnail_pending,
// thumbnail_uploading), and publish_failed from the post-thumbnail
// states (thumbnail_applied, ready_to_publish, publishing). The
// spec says: from PRIVATE_UPLOADED onward, every error MUST leave
// the video private. The fact that the error exits ARE reachable
// from these states is what makes the EnforcePrivacyInvariant
// valuable.
func TestAllPostBoundaryErrorsAreLegalTransitions(t *testing.T) {
	cases := []struct {
		from   DeliveryState
		errors []DeliveryState
	}{
		// Failure modes during private upload: covered by the
		// pre-boundary private_upload_failed exits; not exercised here.
		// Post-private-upload: thumbnail pipeline + publish pipeline.
		{DeliveryStatePrivateUploaded, []DeliveryState{DeliveryStateThumbnailFailed}},
		{DeliveryStateThumbnailPending, []DeliveryState{DeliveryStateThumbnailFailed}},
		{DeliveryStateThumbnailUploading, []DeliveryState{DeliveryStateThumbnailFailed}},
		{DeliveryStateThumbnailApplied, []DeliveryState{DeliveryStatePublishFailed}},
		{DeliveryStateReadyToPublish, []DeliveryState{DeliveryStatePublishFailed}},
		{DeliveryStatePublishing, []DeliveryState{DeliveryStatePublishFailed}},
	}
	for _, c := range cases {
		legal := c.from.LegalTransitions()
		legalSet := make(map[DeliveryState]bool, len(legal))
		for _, l := range legal {
			legalSet[l] = true
		}
		for _, want := range c.errors {
			if !legalSet[want] {
				t.Errorf("expected %q → %q is a legal transition (post-boundary failure must be reachable per spec §10.3)", c.from, want)
			}
		}
	}
}

// TestPreBoundaryCancelAllowed asserts CANCELLED is reachable
// from every pre-boundary happy-path state but NOT from
// post-boundary. Producer-driven cancellation cannot revoke a
// video that's already been uploaded privately — that would
// race with the publish pipeline. Once PRIVATE_UPLOADED has been
// acknowledged by YouTube, the producer can no longer cancel.
func TestPreBoundaryCancelAllowed(t *testing.T) {
	pre := []DeliveryState{
		DeliveryStateDeliveryQueued,
		DeliveryStateTargetValidating,
		DeliveryStateTargetValidated,
		DeliveryStateMediaDownloading,
		DeliveryStateMediaVerified,
		DeliveryStatePrivateUploadQueued,
		DeliveryStatePrivateUploading,
	}
	for _, s := range pre {
		if !s.CanTransitionTo(DeliveryStateCancelled) {
			t.Errorf("pre-boundary %s CANCELLED is not a legal transition (regression: producer should be able to cancel)", s)
		}
	}
	// Post-boundary cancel must NOT be reachable.
	post := []DeliveryState{
		DeliveryStatePrivateUploaded,
		DeliveryStateThumbnailPending,
		DeliveryStateThumbnailUploading,
		DeliveryStateThumbnailApplied,
		DeliveryStateReadyToPublish,
		DeliveryStatePublishing,
	}
	for _, s := range post {
		if s.CanTransitionTo(DeliveryStateCancelled) {
			t.Errorf("post-boundary %s → CANCELLED is unexpectedly legal (producer must NOT be able to undo a private-uploaded row)", s)
		}
	}
}

// TestRetryableClassification pins the worker-pool claim set.
// Terminals are out; the 11 forward states (excluding
// TARGET_VALIDATING which is a transient) are in. Note: this
// test ALSO includes TARGET_VALIDATING in the forward pool
// because it has not yet emitted a terminal event.
func TestRetryableClassification(t *testing.T) {
	want := map[DeliveryState]bool{
		DeliveryStateDeliveryQueued:           true,
		DeliveryStateTargetValidating:         true,
		DeliveryStateTargetValidated:          true,
		DeliveryStateMediaDownloading:         true,
		DeliveryStateMediaVerified:            true,
		DeliveryStatePrivateUploadQueued:      true,
		DeliveryStatePrivateUploading:         true,
		DeliveryStateThumbnailPending:         true,
		DeliveryStateThumbnailUploading:       true,
		DeliveryStateReadyToPublish:           true,
		DeliveryStatePublishing:               true,
		// NOT retryable.
		DeliveryStatePrivateUploaded:           false,
		DeliveryStateThumbnailApplied:          false,
		DeliveryStatePublished:                false,
		DeliveryStateBlockedTarget:            false,
		DeliveryStateBlockedAuth:              false,
		DeliveryStateMediaInvalid:             false,
		DeliveryStatePrivateUploadFailed:      false,
		DeliveryStateThumbnailFailed:          false,
		DeliveryStatePublishFailed:            false,
		DeliveryStateCancelled:                false,
	}
	for s, want := range want {
		if got := s.IsRetryable(); got != want {
			t.Errorf("IsRetryable(%s) = %v, want %v", s, got, want)
		}
	}
}

// TestLegalTransitionsOrderIsStable calls LegalTransitions twice
// on the same state and confirms the result is identical (sorted
// alphabetically). The dashboard relies on stable ordering so
// per-call pagination does not "shuffle" between requests.
func TestLegalTransitionsOrderIsStable(t *testing.T) {
	for _, s := range AllDeliveryStates() {
		first := s.LegalTransitions()
		second := s.LegalTransitions()
		if len(first) != len(second) {
			t.Fatalf("LegalTransitions not stable for %s: first len=%d, second len=%d", s, len(first), len(second))
		}
		for i := range first {
			if first[i] != second[i] {
				t.Errorf("LegalTransitions not stable for %s at index %d: %q vs %q", s, i, first[i], second[i])
			}
		}
		// Alphabetical order check.
		for i := 1; i < len(first); i++ {
			if first[i-1] > first[i] {
				t.Errorf("LegalTransitions not sorted for %s: %q > %q at index %d", s, first[i-1], first[i], i)
			}
		}
	}
}

// TestStateNamesAreSnakeCaseAndUnique confirms the canonical
// invariants on the state names: every value is non-empty,
// lowercase, underscored (snake_case), space-free, and unique.
// The "delivery_" or error-prefix partition used in the prior
// design was an over-engineered business rule that conflicted
// with the canonical happy-path names ("target_validating",
// "media_downloaded", etc.); the actual naming contract comes
// from docs/velox-instaedit-contract.md §10.
func TestStateNamesAreSnakeCaseAndUnique(t *testing.T) {
	const reservedUpper = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	seen := make(map[DeliveryState]int, 21)
	for _, s := range AllDeliveryStates() {
		if s == "" {
			t.Errorf("AllDeliveryStates contains empty string")
			continue
		}
		if strings.ContainsAny(string(s), reservedUpper) {
			t.Errorf("DeliveryState %q should be lowercase (uppercase is reserved for SQL CHECK)", s)
		}
		if strings.Contains(string(s), " ") {
			t.Errorf("DeliveryState %q contains a space (must be snake_case)", s)
		}
		if strings.Contains(string(s), "-") {
			t.Errorf("DeliveryState %q contains a hyphen (must be snake_case, not kebab-case)", s)
		}
		seen[s]++
	}
	for s, n := range seen {
		if n != 1 {
			t.Errorf("DeliveryState %q appears %d times in AllDeliveryStates (must be unique)", s, n)
		}
	}
}

// TestUnmarshalJSONRoundtrip confirms the canonical-unmarshal
// path: every DeliveryState serialises to its snake_case JSON
// string and decodes back to the typed value. Because
// DeliveryState is `type DeliveryState string`, encoding/json
// marshals it without an explicit MarshalJSON override — the
// test exercises that default. The test also asserts that
// UnmarshalJSON rejects unknown / non-string payloads (covered
// by TestUnmarshalJSONRejectsInvalid below).
func TestUnmarshalJSONRoundtrip(t *testing.T) {
	for _, s := range AllDeliveryStates() {
		encoded, err := json.Marshal(s)
		if err != nil {
			t.Errorf("json.Marshal(%q) returned %v", s, err)
			continue
		}
		if got := string(encoded); got != `"`+string(s)+`"` {
			t.Errorf("json.Marshal(%q) = %s, want %q", s, got, `"`+string(s)+`"`)
		}
		var out DeliveryState
		if err := json.Unmarshal(encoded, &out); err != nil {
			t.Errorf("json.Unmarshal(%s) returned %v", encoded, err)
			continue
		}
		if out != s {
			t.Errorf("json.Unmarshal roundtrip mismatch for %q: got %q", s, out)
		}
	}
}

// TestUnmarshalJSONRejectsInvalid asserts the validation hook
// in (*DeliveryState).UnmarshalJSON refuses unknown state
// strings + non-string JSON shapes. This guard is what makes
// the DeliveryState type safe at the JSON boundary — typos and
// numbers cannot leak into the worker pipeline.
//
// Each subtest is a t.Run so a future regression that mutates
// `s` before validation surfaces as a focused failure rather
// than a cross-iteration state bleed. Subtests also carry
// human-readable names so a CI failure log points at the
// specific shape that broke.
func TestUnmarshalJSONRejectsInvalid(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte
		note string // human-readable explanation of the assertion
	}{
		{"unknown_typo", []byte(`"not_a_real_state"`), "near-miss unknown name"},
		{"uppercase_typo", []byte(`"PUBLISHED"`), "uppercase variant of canonical state"},
		{"empty_string", []byte(`""`), "empty string payload"},
		{"near_miss", []byte(`"delivery_queued_typo"`), "near-miss of delivery_queued"},
		{"number", []byte(`123`), "number, not string"},
		{"boolean", []byte(`true`), "boolean literal"},
		{"null_decodes_to_empty", []byte(`null`), "null JSON literal — Go decodes to empty-string, validator rejects"},
		{"object", []byte(`{"state":"published"}`), "JSON object, not string"},
		{"array", []byte(`["published"]`), "JSON array, not string"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			// Defensive reset: ensures a future regression that
			// mutates `s` before failing returns surfaces as a
			// focused test failure rather than as a state bleed
			// into a later subtest.
			var s DeliveryState
			err := s.UnmarshalJSON(c.raw)
			if err == nil {
				t.Errorf("UnmarshalJSON(%s) returned nil; want error (%s)", c.raw, c.note)
			}
			// Defensive: if a regression mutated on error, the
			// post-condition pin catches it.
			if s != "" {
				t.Errorf("UnmarshalJSON returned error AND mutated s to %q; want zero-value on failure", s)
			}
		})
	}
}

// TestTerminalAndNonTerminalPartition asserts every state is
// exactly one of {terminal, non-terminal}: there is no state
// that is classified as both OR that carries outgoing edges
// while classified as terminal. The IsTerminal function is a
// boolean partition — a regression that flips one entry would
// silently break the worker pool's claim CTE. The test also
// guards against the zero-value DeliveryState slipping into a
// future pipeline as a phantom "non-terminal + non-happy" state.
func TestTerminalAndNonTerminalPartition(t *testing.T) {
	for _, s := range AllDeliveryStates() {
		// Defensive: pin that the zero-value state "" is NOT in
		// the AllDeliveryStates() set. If a regression adds it
		// accidentally, IsTerminal returns false and
		// happyPathSuccessor would map "" to "" — confusing.
		if s == "" {
			t.Errorf("AllDeliveryStates contains the zero-value DeliveryState")
		}
		isTerm := s.IsTerminal()
		// Sanity: a state cannot be both terminal and have
		// outgoing edges.
		if isTerm && len(s.LegalTransitions()) > 0 {
			t.Errorf("terminal state %q has %d outgoing transitions", s, len(s.LegalTransitions()))
		}
		// Sanity: a state cannot be both terminal and retryable.
		if isTerm && s.IsRetryable() {
			t.Errorf("terminal state %q returned IsRetryable()==true", s)
		}
	}
}

// TestHappyPathSuccessorComplete pins the invariant between the
// happyPathSuccessor table and the canonical transitionMap: every
// non-terminal state must have (a) exactly one entry in
// happyPathSuccessor pointing at a non-error exit, and (b) the
// successor must agree with what transitionMap reports (i.e.
// the successor must be reachable from the source state via
// CanTransitionTo). Without this sanity check the two
// structures can drift silently — and TestHappyPathForwardWalk
// catches drift at the level of the 14-step chain, not at the
// level of the per-state lookup.
func TestHappyPathSuccessorComplete(t *testing.T) {
	covered := make(map[DeliveryState]bool, len(happyPathSuccessor))
	for src, dst := range happyPathSuccessor {
		// (a) every key is a canonical state.
		canonical := false
		for _, allowed := range AllDeliveryStates() {
			if src == allowed {
				canonical = true
				break
			}
		}
		if !canonical {
			t.Errorf("happyPathSuccessor has key %q but it is not in AllDeliveryStates", src)
		}
		covered[src] = true

		// Terminal states: dst must be the zero value.
		if src.IsTerminal() {
			if dst != "" {
				t.Errorf("happyPathSuccessor(%q) = %q; want \"\" for terminal state", src, dst)
			}
			continue
		}

		// Non-terminal states: dst must be non-empty, canonical,
		// non-error, and reachable via transitionMap[src][dst].
		if dst == "" {
			t.Errorf("happyPathSuccessor(%q) = \"\"; non-terminal must have a happy-path successor", src)
			continue
		}
		if dst.IsErrorExit() {
			t.Errorf("happyPathSuccessor(%q) = %q; must NOT point at an error exit (would silently bridge to terminal)", src, dst)
		}
		if !src.CanTransitionTo(dst) {
			t.Errorf("happyPathSuccessor(%q) = %q; transitionMap disagrees (CanTransitionTo returned false)", src, dst)
		}
	}
	// Every canonical AllDeliveryState must have a happyPathSuccessor
	// entry (possibly mapping terminal → ""). Guards against drop-outs
	// when somebody adds a new state but forgets to wire the chain.
	for _, s := range AllDeliveryStates() {
		if !covered[s] {
			t.Errorf("happyPathSuccessor is missing entry for canonical state %q", s)
		}
	}
}
