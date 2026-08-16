package models

import (
	"reflect"
	"sort"
	"testing"
)

// allYouTubeDeliveryStates is the closed 17-value set. Every exhaustive
// test below iterates it so a newly-added const that is not wired into a
// table/switch fails loudly.
var allYouTubeDeliveryStates = []YouTubeDeliveryState{
	YouTubeDeliveryPreflight,
	YouTubeDeliveryReadyToUpload,
	YouTubeDeliveryUploading,
	YouTubeDeliveryUploaded,
	YouTubeDeliveryProcessing,
	YouTubeDeliveryThumbnailPending,
	YouTubeDeliveryThumbnailReady,
	YouTubeDeliveryScheduled,
	YouTubeDeliveryPublished,
	YouTubeDeliveryVerified,
	YouTubeDeliveryRetryWait,
	YouTubeDeliveryQuotaWait,
	YouTubeDeliveryBlockedAuth,
	YouTubeDeliveryCopyrightReview,
	YouTubeDeliveryProcessingStuck,
	YouTubeDeliveryFailed,
	YouTubeDeliveryDeadLetter,
}

// TestYouTubeDeliveryState_IsTerminal pins the terminal partition:
// verified (success), failed (permanent), dead_letter (retry exhausted).
func TestYouTubeDeliveryState_IsTerminal(t *testing.T) {
	terminal := map[YouTubeDeliveryState]bool{
		YouTubeDeliveryVerified:   true,
		YouTubeDeliveryFailed:     true,
		YouTubeDeliveryDeadLetter: true,
	}
	for _, s := range allYouTubeDeliveryStates {
		if got := s.IsTerminal(); got != terminal[s] {
			t.Errorf("IsTerminal(%s): want %v, got %v", s, terminal[s], got)
		}
	}
}

// TestYouTubeDeliveryState_IsSideState pins the side-state partition: the
// five blocking states that carry a resume_state.
func TestYouTubeDeliveryState_IsSideState(t *testing.T) {
	side := map[YouTubeDeliveryState]bool{
		YouTubeDeliveryRetryWait:       true,
		YouTubeDeliveryQuotaWait:       true,
		YouTubeDeliveryBlockedAuth:     true,
		YouTubeDeliveryCopyrightReview: true,
		YouTubeDeliveryProcessingStuck: true,
	}
	for _, s := range allYouTubeDeliveryStates {
		if got := s.IsSideState(); got != side[s] {
			t.Errorf("IsSideState(%s): want %v, got %v", s, side[s], got)
		}
	}
}

// TestYouTubeDeliveryState_CanTransitionTo is the table-driven FSM
// spot-check. It covers the happy path, every error exit, every side-state
// resume, terminal→anything forbidden, step-skipping forbidden,
// self-transitions forbidden, and the defensive empty/unknown cases.
func TestYouTubeDeliveryState_CanTransitionTo(t *testing.T) {
	cases := []struct {
		from, to YouTubeDeliveryState
		want     bool
	}{
		// === Happy path (the plan's canonical examples) ===
		{YouTubeDeliveryPreflight, YouTubeDeliveryReadyToUpload, true},
		{YouTubeDeliveryReadyToUpload, YouTubeDeliveryUploading, true},
		{YouTubeDeliveryUploading, YouTubeDeliveryUploaded, true},
		{YouTubeDeliveryUploaded, YouTubeDeliveryProcessing, true},
		{YouTubeDeliveryProcessing, YouTubeDeliveryScheduled, true},
		{YouTubeDeliveryScheduled, YouTubeDeliveryPublished, true},
		{YouTubeDeliveryPublished, YouTubeDeliveryVerified, true},

		// === Error exits ===
		{YouTubeDeliveryPreflight, YouTubeDeliveryBlockedAuth, true},
		{YouTubeDeliveryPreflight, YouTubeDeliveryQuotaWait, true},
		{YouTubeDeliveryPreflight, YouTubeDeliveryFailed, true},
		{YouTubeDeliveryReadyToUpload, YouTubeDeliveryQuotaWait, true},
		{YouTubeDeliveryUploading, YouTubeDeliveryRetryWait, true},
		{YouTubeDeliveryUploading, YouTubeDeliveryDeadLetter, true},
		{YouTubeDeliveryUploaded, YouTubeDeliveryCopyrightReview, true},
		{YouTubeDeliveryProcessing, YouTubeDeliveryProcessingStuck, true},
		{YouTubeDeliveryProcessing, YouTubeDeliveryCopyrightReview, true},

		// === Thumbnail branch ===
		{YouTubeDeliveryProcessing, YouTubeDeliveryThumbnailPending, true},
		{YouTubeDeliveryThumbnailPending, YouTubeDeliveryThumbnailReady, true},
		{YouTubeDeliveryThumbnailPending, YouTubeDeliveryRetryWait, true},
		{YouTubeDeliveryThumbnailReady, YouTubeDeliveryScheduled, true},

		// === Side-state resumes ===
		{YouTubeDeliveryRetryWait, YouTubeDeliveryReadyToUpload, true},
		{YouTubeDeliveryRetryWait, YouTubeDeliveryUploading, true},
		{YouTubeDeliveryRetryWait, YouTubeDeliveryThumbnailPending, true},
		{YouTubeDeliveryRetryWait, YouTubeDeliveryDeadLetter, true},
		{YouTubeDeliveryQuotaWait, YouTubeDeliveryReadyToUpload, true},
		{YouTubeDeliveryQuotaWait, YouTubeDeliveryThumbnailPending, true},
		{YouTubeDeliveryBlockedAuth, YouTubeDeliveryPreflight, true},
		{YouTubeDeliveryBlockedAuth, YouTubeDeliveryReadyToUpload, true},
		{YouTubeDeliveryBlockedAuth, YouTubeDeliveryUploading, true},
		{YouTubeDeliveryCopyrightReview, YouTubeDeliveryProcessing, true},
		{YouTubeDeliveryCopyrightReview, YouTubeDeliveryScheduled, true},
		{YouTubeDeliveryProcessingStuck, YouTubeDeliveryThumbnailPending, true},
		{YouTubeDeliveryProcessingStuck, YouTubeDeliveryScheduled, true},

		// === Terminal → anywhere forbidden ===
		{YouTubeDeliveryVerified, YouTubeDeliveryUploading, false},
		{YouTubeDeliveryVerified, YouTubeDeliveryScheduled, false},
		{YouTubeDeliveryFailed, YouTubeDeliveryRetryWait, false},
		{YouTubeDeliveryDeadLetter, YouTubeDeliveryRetryWait, false}, // operator-only, not a worker transition
		{YouTubeDeliveryDeadLetter, YouTubeDeliveryPreflight, false},

		// === Skipping happy-path steps forbidden (the plan's example) ===
		{YouTubeDeliveryProcessing, YouTubeDeliveryVerified, false},
		{YouTubeDeliveryUploaded, YouTubeDeliveryScheduled, false},
		{YouTubeDeliveryPreflight, YouTubeDeliveryUploading, false},

		// === Self-transitions forbidden ===
		{YouTubeDeliveryPreflight, YouTubeDeliveryPreflight, false},
		{YouTubeDeliveryProcessing, YouTubeDeliveryProcessing, false},

		// === Defensive: empty / unknown ===
		{YouTubeDeliveryState(""), YouTubeDeliveryUploading, false},
		{YouTubeDeliveryPreflight, YouTubeDeliveryState(""), false},
		{YouTubeDeliveryState("garbage_unknown"), YouTubeDeliveryPreflight, false},
	}
	for _, tc := range cases {
		if got := tc.from.CanTransitionTo(tc.to); got != tc.want {
			t.Errorf("CanTransitionTo(%s → %s): want %v, got %v", tc.from, tc.to, tc.want, got)
		}
	}
}

// TestYouTubeDeliveryState_LegalTransitions exhaustively verifies the
// transition map: for each state with successors, LegalTransitions()
// returns exactly the declared set (sorted); for terminal states it
// returns nil. This is the canonical "did I forget an edge?" smoke test.
func TestYouTubeDeliveryState_LegalTransitions(t *testing.T) {
	expected := map[YouTubeDeliveryState][]YouTubeDeliveryState{
		YouTubeDeliveryPreflight: {
			YouTubeDeliveryReadyToUpload,
			YouTubeDeliveryQuotaWait,
			YouTubeDeliveryBlockedAuth,
			YouTubeDeliveryFailed,
		},
		YouTubeDeliveryReadyToUpload: {
			YouTubeDeliveryUploading,
			YouTubeDeliveryQuotaWait,
			YouTubeDeliveryBlockedAuth,
		},
		YouTubeDeliveryUploading: {
			YouTubeDeliveryUploaded,
			YouTubeDeliveryRetryWait,
			YouTubeDeliveryBlockedAuth,
			YouTubeDeliveryDeadLetter,
		},
		YouTubeDeliveryUploaded: {
			YouTubeDeliveryProcessing,
			YouTubeDeliveryCopyrightReview,
		},
		YouTubeDeliveryProcessing: {
			YouTubeDeliveryThumbnailPending,
			YouTubeDeliveryScheduled,
			YouTubeDeliveryProcessingStuck,
			YouTubeDeliveryCopyrightReview,
		},
		YouTubeDeliveryThumbnailPending: {
			YouTubeDeliveryThumbnailReady,
			YouTubeDeliveryRetryWait,
		},
		YouTubeDeliveryThumbnailReady: {
			YouTubeDeliveryScheduled,
		},
		YouTubeDeliveryScheduled: {
			YouTubeDeliveryPublished,
		},
		YouTubeDeliveryPublished: {
			YouTubeDeliveryVerified,
		},
		YouTubeDeliveryRetryWait: {
			YouTubeDeliveryReadyToUpload,
			YouTubeDeliveryUploading,
			YouTubeDeliveryThumbnailPending,
			YouTubeDeliveryDeadLetter,
		},
		YouTubeDeliveryQuotaWait: {
			YouTubeDeliveryReadyToUpload,
			YouTubeDeliveryThumbnailPending,
		},
		YouTubeDeliveryBlockedAuth: {
			YouTubeDeliveryPreflight,
			YouTubeDeliveryReadyToUpload,
			YouTubeDeliveryUploading,
		},
		YouTubeDeliveryCopyrightReview: {
			YouTubeDeliveryProcessing,
			YouTubeDeliveryScheduled,
		},
		YouTubeDeliveryProcessingStuck: {
			YouTubeDeliveryThumbnailPending,
			YouTubeDeliveryScheduled,
		},
	}
	// LegalTransitions() returns sorted output; sort the expected slices
	// so reflect.DeepEqual is order-stable.
	for k := range expected {
		sort.Slice(expected[k], func(i, j int) bool { return expected[k][i] < expected[k][j] })
	}

	terminalStates := []YouTubeDeliveryState{
		YouTubeDeliveryVerified,
		YouTubeDeliveryFailed,
		YouTubeDeliveryDeadLetter,
	}

	for from, want := range expected {
		got := from.LegalTransitions()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("LegalTransitions(%s): want %v, got %v", from, want, got)
		}
	}
	for _, term := range terminalStates {
		if got := term.LegalTransitions(); got != nil {
			t.Errorf("LegalTransitions(%s terminal): want nil, got %v", term, got)
		}
	}
}

// TestYouTubeDeliveryTransitionMapEnumCoverage guards against accidentally
// omitting a new enum value from youtubeDeliveryTransitionMap: every value
// MUST have an entry (possibly empty for terminal states).
func TestYouTubeDeliveryTransitionMapEnumCoverage(t *testing.T) {
	for _, s := range allYouTubeDeliveryStates {
		if _, ok := youtubeDeliveryTransitionMap[s]; !ok {
			t.Errorf("youtubeDeliveryTransitionMap missing entry for %s — every enum value MUST have a (possibly empty) entry", s)
		}
	}
}

// TestYouTubeDeliveryState_TerminalStatesHaveNoNormalExits pins the rule
// that terminal states (verified / failed / dead_letter) have empty
// WORKER transition maps — the only way out of dead_letter is the
// operator retry in youtubeDeliveryOperatorTransitionMap.
func TestYouTubeDeliveryState_TerminalStatesHaveNoNormalExits(t *testing.T) {
	for _, s := range []YouTubeDeliveryState{
		YouTubeDeliveryVerified,
		YouTubeDeliveryFailed,
		YouTubeDeliveryDeadLetter,
	} {
		if len(youtubeDeliveryTransitionMap[s]) != 0 {
			t.Errorf("terminal state %s must have no worker exits, got %v", s, youtubeDeliveryTransitionMap[s])
		}
	}
}

// TestYouTubeDeliveryState_SideStatesHaveResumePolicy pins the rule that
// every side state has at least one legal resume transition (so a resume
// is always possible once the blocking condition clears) and that every
// side state is NOT terminal.
func TestYouTubeDeliveryState_SideStatesHaveResumePolicy(t *testing.T) {
	for _, s := range allYouTubeDeliveryStates {
		if !s.IsSideState() {
			continue
		}
		if s.IsTerminal() {
			t.Errorf("side state %s must not be terminal", s)
		}
		if got := s.LegalTransitions(); len(got) == 0 {
			t.Errorf("side state %s has no resume transitions", s)
		}
	}
}

// TestYouTubeDeliveryState_CanOperatorTransitionTo pins the operator-only
// exit: dead_letter → retry_wait is legal ONLY via the operator gate, and
// the worker gate must reject it.
func TestYouTubeDeliveryState_CanOperatorTransitionTo(t *testing.T) {
	if !YouTubeDeliveryDeadLetter.CanOperatorTransitionTo(YouTubeDeliveryRetryWait) {
		t.Error("operator retry dead_letter → retry_wait must be legal")
	}
	// The worker gate must reject the same pair.
	if YouTubeDeliveryDeadLetter.CanTransitionTo(YouTubeDeliveryRetryWait) {
		t.Error("worker CanTransitionTo(dead_letter → retry_wait) must be illegal (operator-only)")
	}
	// No other operator transitions exist today.
	for _, s := range allYouTubeDeliveryStates {
		if s == YouTubeDeliveryDeadLetter {
			continue
		}
		for _, tgt := range allYouTubeDeliveryStates {
			if s.CanOperatorTransitionTo(tgt) {
				t.Errorf("unexpected operator transition %s → %s", s, tgt)
			}
		}
	}
	// Defensive: empty / unknown.
	if YouTubeDeliveryDeadLetter.CanOperatorTransitionTo("") {
		t.Error("CanOperatorTransitionTo(dead_letter → \"\") must be illegal")
	}
	if YouTubeDeliveryState("").CanOperatorTransitionTo(YouTubeDeliveryRetryWait) {
		t.Error("CanOperatorTransitionTo(\"\" → retry_wait) must be illegal")
	}
}

// TestYouTubeDeliveryState_CanTransitionTo_RoundTrip walks the full happy
// path end-to-end (including the thumbnail branch) via CanTransitionTo,
// proving the graph is a connected chain from preflight to verified.
func TestYouTubeDeliveryState_CanTransitionTo_RoundTrip(t *testing.T) {
	noThumbnailPath := []YouTubeDeliveryState{
		YouTubeDeliveryPreflight,
		YouTubeDeliveryReadyToUpload,
		YouTubeDeliveryUploading,
		YouTubeDeliveryUploaded,
		YouTubeDeliveryProcessing,
		YouTubeDeliveryScheduled,
		YouTubeDeliveryPublished,
		YouTubeDeliveryVerified,
	}
	thumbnailPath := []YouTubeDeliveryState{
		YouTubeDeliveryProcessing,
		YouTubeDeliveryThumbnailPending,
		YouTubeDeliveryThumbnailReady,
		YouTubeDeliveryScheduled,
	}
	for _, path := range [][]YouTubeDeliveryState{noThumbnailPath, thumbnailPath} {
		for i := 0; i < len(path)-1; i++ {
			from, to := path[i], path[i+1]
			if !from.CanTransitionTo(to) {
				t.Errorf("happy-path edge %s → %s: want legal, got illegal", from, to)
			}
		}
	}
}
