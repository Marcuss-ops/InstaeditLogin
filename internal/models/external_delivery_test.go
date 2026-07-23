package models

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestExternalDeliveryStatus_IsTerminal covers all 11 enum values.
//
// Expectations match the lifecycle doc-comment on the enum:
// published (success terminal), failed (operational terminal),
// dead_letter (exhausted retry terminal), blocked_auth (auth
// terminal — admin must reconnect). All other states are
// mid-lifecycle and not terminal.
func TestExternalDeliveryStatus_IsTerminal(t *testing.T) {
	cases := []struct {
		status ExternalDeliveryStatus
		want   bool
	}{
		{ExternalDeliveryStatusAccepted, false},
		{ExternalDeliveryStatusDownloading, false},
		{ExternalDeliveryStatusArtifactVerified, false},
		{ExternalDeliveryStatusIngestCompleted, false},
		{ExternalDeliveryStatusQueued, false},
		{ExternalDeliveryStatusPublishing, false},
		{ExternalDeliveryStatusPublished, true},
		{ExternalDeliveryStatusRetryWait, false},
		{ExternalDeliveryStatusBlockedAuth, true},
		{ExternalDeliveryStatusFailed, true},
		{ExternalDeliveryStatusDeadLetter, true},
	}
	for _, tc := range cases {
		if got := tc.status.IsTerminal(); got != tc.want {
			t.Errorf("IsTerminal(%s): want %v, got %v", tc.status, tc.want, got)
		}
	}
}

// TestExternalDeliveryStatus_IsRetryable covers all 11 enum values.
//
// Seven values are "worker pool claimable":
//   - The 6 happy-path forward states: accepted → publishing
//   - retry_wait: re-claim when next_attempt_at elapses
//
// Four values exclude the worker pool claim:
//   - Terminal: published / failed / dead_letter (no successor)
//   - blocked_auth: re-claim only AFTER admin reconnects
//     (separate path: blocked_auth → queued manual transition)
func TestExternalDeliveryStatus_IsRetryable(t *testing.T) {
	cases := []struct {
		status ExternalDeliveryStatus
		want   bool
	}{
		{ExternalDeliveryStatusAccepted, true},
		{ExternalDeliveryStatusDownloading, true},
		{ExternalDeliveryStatusArtifactVerified, true},
		{ExternalDeliveryStatusIngestCompleted, true},
		{ExternalDeliveryStatusQueued, true},
		{ExternalDeliveryStatusPublishing, true},
		{ExternalDeliveryStatusRetryWait, true},
		{ExternalDeliveryStatusPublished, false},
		{ExternalDeliveryStatusBlockedAuth, false},
		{ExternalDeliveryStatusFailed, false},
		{ExternalDeliveryStatusDeadLetter, false},
	}
	for _, tc := range cases {
		if got := tc.status.IsRetryable(); got != tc.want {
			t.Errorf("IsRetryable(%s): want %v, got %v", tc.status, tc.want, got)
		}
	}
}

// TestExternalDeliveryStatus_Next covers the happy-path one-step
// forward transition + all 5 non-happy-path states returning "".
// Side-state (retry_wait / blocked_auth) and terminal-state
// (published / failed / dead_letter) callers should use
// CanTransitionTo / LegalTransitions to navigate the graph —
// Next() intentionally returns "" for these because there's no
// single canonical successor.
func TestExternalDeliveryStatus_Next(t *testing.T) {
	cases := []struct {
		from ExternalDeliveryStatus
		want ExternalDeliveryStatus
	}{
		// Happy-path forward — 6 one-step transitions
		{ExternalDeliveryStatusAccepted, ExternalDeliveryStatusDownloading},
		{ExternalDeliveryStatusDownloading, ExternalDeliveryStatusArtifactVerified},
		{ExternalDeliveryStatusArtifactVerified, ExternalDeliveryStatusIngestCompleted},
		{ExternalDeliveryStatusIngestCompleted, ExternalDeliveryStatusQueued},
		{ExternalDeliveryStatusQueued, ExternalDeliveryStatusPublishing},
		{ExternalDeliveryStatusPublishing, ExternalDeliveryStatusPublished},

		// Side states → no canonical forward successor
		{ExternalDeliveryStatusRetryWait, ""},
		{ExternalDeliveryStatusBlockedAuth, ""},

		// Terminal → ""
		{ExternalDeliveryStatusPublished, ""},
		{ExternalDeliveryStatusFailed, ""},
		{ExternalDeliveryStatusDeadLetter, ""},
	}
	for _, tc := range cases {
		if got := tc.from.Next(); got != tc.want {
			t.Errorf("Next(%s): want %q, got %q", tc.from, tc.want, got)
		}
	}
}

// TestExternalDeliveryStatus_CanTransitionTo is a SPOT-CHECK of
// CanTransitionTo, not the full 11×11=121 grid (LegalTransitions
// test below verifies the full map exhaustively). The spot-checks
// here hit every class of legal/illegal transition:
//
//   - Happy-path forward (6 edges)
//   - Error exits from each pre-terminal to retry_wait /
//     blocked_auth / failed (3 spot-checks; the LegalTransitions
//     test covers all of them too)
//   - Resume paths from retry_wait (3 spot-checks)
//   - Resume after admin reauth (1 edge)
//   - Terminal → everywhere forbidden (5 spot-checks across
//     3 terminal states)
//   - Skipping happy-path steps forbidden (3 spot-checks)
//   - Self-transitions forbidden (2 spot-checks)
//   - Defensive: empty / unknown values (3 spot-checks)
func TestExternalDeliveryStatus_CanTransitionTo(t *testing.T) {
	cases := []struct {
		from, to ExternalDeliveryStatus
		want     bool
	}{
		// === Happy-path forward ===
		{ExternalDeliveryStatusAccepted, ExternalDeliveryStatusDownloading, true},
		{ExternalDeliveryStatusDownloading, ExternalDeliveryStatusArtifactVerified, true},
		{ExternalDeliveryStatusArtifactVerified, ExternalDeliveryStatusIngestCompleted, true},
		{ExternalDeliveryStatusIngestCompleted, ExternalDeliveryStatusQueued, true},
		{ExternalDeliveryStatusQueued, ExternalDeliveryStatusPublishing, true},
		{ExternalDeliveryStatusPublishing, ExternalDeliveryStatusPublished, true},

		// === Error exits to retry_wait / blocked_auth / failed ===
		{ExternalDeliveryStatusDownloading, ExternalDeliveryStatusRetryWait, true},
		{ExternalDeliveryStatusQueued, ExternalDeliveryStatusBlockedAuth, true},
		{ExternalDeliveryStatusArtifactVerified, ExternalDeliveryStatusFailed, true},

		// === Resume paths from retry_wait ===
		{ExternalDeliveryStatusRetryWait, ExternalDeliveryStatusDownloading, true},
		{ExternalDeliveryStatusRetryWait, ExternalDeliveryStatusQueued, true},
		{ExternalDeliveryStatusRetryWait, ExternalDeliveryStatusDeadLetter, true},

		// === Resume after admin reauth ===
		{ExternalDeliveryStatusBlockedAuth, ExternalDeliveryStatusQueued, true},
		// blocked_auth must NOT allow dead-end failure (admin intervention
		// semantics: there's always a recovery path via reconnect)
		{ExternalDeliveryStatusBlockedAuth, ExternalDeliveryStatusFailed, false},

		// === Terminal → everywhere forbidden ===
		{ExternalDeliveryStatusPublished, ExternalDeliveryStatusAccepted, false},
		{ExternalDeliveryStatusPublished, ExternalDeliveryStatusQueued, false},
		{ExternalDeliveryStatusFailed, ExternalDeliveryStatusRetryWait, false},
		{ExternalDeliveryStatusFailed, ExternalDeliveryStatusQueued, false},
		{ExternalDeliveryStatusDeadLetter, ExternalDeliveryStatusQueued, false},

		// === Skipping happy-path steps forbidden ===
		{ExternalDeliveryStatusAccepted, ExternalDeliveryStatusArtifactVerified, false},
		{ExternalDeliveryStatusAccepted, ExternalDeliveryStatusPublishing, false},
		{ExternalDeliveryStatusQueued, ExternalDeliveryStatusPublished, false},

		// === Self-transitions forbidden ===
		{ExternalDeliveryStatusAccepted, ExternalDeliveryStatusAccepted, false},
		{ExternalDeliveryStatusQueued, ExternalDeliveryStatusQueued, false},

		// === Defensive: empty / unknown values ===
		{ExternalDeliveryStatus(""), ExternalDeliveryStatusDownloading, false},
		{ExternalDeliveryStatusAccepted, ExternalDeliveryStatus(""), false},
		{ExternalDeliveryStatus("garbage_unknown"), ExternalDeliveryStatusAccepted, false},
	}
	for _, tc := range cases {
		if got := tc.from.CanTransitionTo(tc.to); got != tc.want {
			t.Errorf("CanTransitionTo(%s → %s): want %v, got %v",
				tc.from, tc.to, tc.want, got)
		}
	}
}

// TestExternalDeliveryStatus_LegalTransitions exhaustively verifies
// the transition map: for each from-status with NON-empty
// successors, LegalTransitions() returns exactly the set declared
// in transitionMap (sorted for determinism). For each terminal
// status (3 of them), returns nil.
//
// This is the canonical "did I forget a transition edge?" smoke
// test for the state machine. Updates to transitionMap require
// updating the expected list in lockstep.
func TestExternalDeliveryStatus_LegalTransitions(t *testing.T) {
	expected := map[ExternalDeliveryStatus][]ExternalDeliveryStatus{
		ExternalDeliveryStatusAccepted: {
			ExternalDeliveryStatusDownloading, ExternalDeliveryStatusRetryWait,
			ExternalDeliveryStatusBlockedAuth, ExternalDeliveryStatusFailed,
		},
		ExternalDeliveryStatusDownloading: {
			ExternalDeliveryStatusArtifactVerified, ExternalDeliveryStatusRetryWait,
			ExternalDeliveryStatusBlockedAuth, ExternalDeliveryStatusFailed,
		},
		ExternalDeliveryStatusArtifactVerified: {
			ExternalDeliveryStatusIngestCompleted, ExternalDeliveryStatusRetryWait,
			ExternalDeliveryStatusBlockedAuth, ExternalDeliveryStatusFailed,
		},
		ExternalDeliveryStatusIngestCompleted: {
			ExternalDeliveryStatusQueued, ExternalDeliveryStatusRetryWait,
			ExternalDeliveryStatusBlockedAuth, ExternalDeliveryStatusFailed,
		},
		ExternalDeliveryStatusQueued: {
			ExternalDeliveryStatusPublishing, ExternalDeliveryStatusRetryWait,
			ExternalDeliveryStatusBlockedAuth, ExternalDeliveryStatusFailed,
		},
		ExternalDeliveryStatusPublishing: {
			ExternalDeliveryStatusPublished, ExternalDeliveryStatusRetryWait,
			ExternalDeliveryStatusBlockedAuth, ExternalDeliveryStatusFailed,
		},
		ExternalDeliveryStatusRetryWait: {
			ExternalDeliveryStatusDownloading, ExternalDeliveryStatusQueued,
			ExternalDeliveryStatusBlockedAuth, ExternalDeliveryStatusFailed,
			ExternalDeliveryStatusDeadLetter,
		},
		ExternalDeliveryStatusBlockedAuth: {
			ExternalDeliveryStatusQueued,
		},
	}
	// Sort the expected slices because LegalTransitions() returns
	// sorted output — the comparison must be order-stable.
	for k := range expected {
		sort.Slice(expected[k], func(i, j int) bool { return expected[k][i] < expected[k][j] })
	}

	// Terminal → nil
	terminalStates := []ExternalDeliveryStatus{
		ExternalDeliveryStatusPublished,
		ExternalDeliveryStatusFailed,
		ExternalDeliveryStatusDeadLetter,
	}

	// Walk the expected map: for each from-status, verify the
	// slice matches (sorted for both, so reflect.DeepEqual is
	// correct).
	for from, want := range expected {
		got := from.LegalTransitions()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("LegalTransitions(%s): want %v, got %v", from, want, got)
		}
	}
	for _, term := range terminalStates {
		got := term.LegalTransitions()
		if got != nil {
			t.Errorf("LegalTransitions(%s terminal): want nil, got %v", term, got)
		}
	}
}

// TestTransitionMapEnumCoverage guards against accidentally
// omitting a new enum value from transitionMap. If somebody adds
// a new const but forgets to wire it into the map, CanTransitionTo
// would silently return false for ALL transitions from the new
// status — masking bugs in worker / handler code paths.
//
// Every enum value MUST have an entry (possibly empty for
// terminals). When adding a new status, add it here too.
func TestTransitionMapEnumCoverage(t *testing.T) {
	allStatuses := []ExternalDeliveryStatus{
		ExternalDeliveryStatusAccepted,
		ExternalDeliveryStatusDownloading,
		ExternalDeliveryStatusArtifactVerified,
		ExternalDeliveryStatusIngestCompleted,
		ExternalDeliveryStatusQueued,
		ExternalDeliveryStatusPublishing,
		ExternalDeliveryStatusPublished,
		ExternalDeliveryStatusRetryWait,
		ExternalDeliveryStatusBlockedAuth,
		ExternalDeliveryStatusFailed,
		ExternalDeliveryStatusDeadLetter,
	}
	for _, s := range allStatuses {
		if _, ok := transitionMap[s]; !ok {
			t.Errorf("transitionMap missing entry for %s — every enum value MUST have a (possibly empty) entry", s)
		}
	}
}

// TestExternalDeliveryStatus_CanTransitionTo_RoundTrip asserts
// the happy-path can be walked end-to-end via CanTransitionTo
// (a more rigorous chain than spot-checks alone). Each pair
// (k_n, k_n+1) where k_n is the n-th happy-path step must
// report legal=true.
func TestExternalDeliveryStatus_CanTransitionTo_RoundTrip(t *testing.T) {
	happyPath := []ExternalDeliveryStatus{
		ExternalDeliveryStatusAccepted,
		ExternalDeliveryStatusDownloading,
		ExternalDeliveryStatusArtifactVerified,
		ExternalDeliveryStatusIngestCompleted,
		ExternalDeliveryStatusQueued,
		ExternalDeliveryStatusPublishing,
		ExternalDeliveryStatusPublished,
	}
	for i := 0; i < len(happyPath)-1; i++ {
		from, to := happyPath[i], happyPath[i+1]
		if !from.CanTransitionTo(to) {
			t.Errorf("happy-path edge %s → %s: want legal, got illegal", from, to)
		}
	}
	// Spot-check that Published-CanTransitionTo-anything returns
	// false (terminal reachability test).
	for _, target := range []ExternalDeliveryStatus{
		ExternalDeliveryStatusAccepted, ExternalDeliveryStatusDownloading,
		ExternalDeliveryStatusFailed, ExternalDeliveryStatusRetryWait,
	} {
		if ExternalDeliveryStatusPublished.CanTransitionTo(target) {
			t.Errorf("Published → %s should be illegal (terminal)", target)
		}
	}
}

// TestExternalDeliveryStatus_CanonicalResume pins the operator's
// decision rule for the dual-target retry_wait resume:
//
//   - download_url valid (URL signer TTL hasn't elapsed, or the
//     HMAC signature is still within its 30-min window) → resume
//     from queued (artifact already in InstaEdit storage; no
//     re-fetch needed).
//   - download_url invalid (signed URL expired, signature stale)
//     → re-fetch from downloading (must re-validate SHA + size +
//     MIME against the original Velox contract).
//
// Non-retry_wait input → returns "" (no canonical resume target).
// The behaviour is intentionally a function of the input state,
// not the publish_worker pulling an opinion out of thin air;
// this is the centralising helper.
func TestExternalDeliveryStatus_CanonicalResume(t *testing.T) {
	cases := []struct {
		name             string
		from             ExternalDeliveryStatus
		downloadURLValid bool
		want             ExternalDeliveryStatus
	}{
		{
			name:             "retry_wait + valid url → queued (no re-fetch)",
			from:             ExternalDeliveryStatusRetryWait,
			downloadURLValid: true,
			want:             ExternalDeliveryStatusQueued,
		},
		{
			name:             "retry_wait + invalid url → downloading (re-fetch)",
			from:             ExternalDeliveryStatusRetryWait,
			downloadURLValid: false,
			want:             ExternalDeliveryStatusDownloading,
		},
		{
			name:             "non-retry_wait → empty (no canonical resume)",
			from:             ExternalDeliveryStatusAccepted,
			downloadURLValid: true,
			want:             "",
		},
		{
			name:             "non-retry_wait terminal → empty",
			from:             ExternalDeliveryStatusPublished,
			downloadURLValid: false,
			want:             "",
		},
		{
			name:             "empty status → empty (defensive)",
			from:             ExternalDeliveryStatus(""),
			downloadURLValid: true,
			want:             "",
		},
		{
			// Documents the current behaviour: CanonicalResume
			// does NOT have visibility into ExternalDelivery.DownloadURL
			// being nil; the caller (worker) is responsible for
			// short-circuiting the metadata-only path BEFORE calling
			// here. This test pins the present-day contract so a
			// future edit adding URL-null awareness is caught by
			// the test diff rather than silently regressing.
			name:             "retry_wait + urlValid + DownloadURL==nil (worker MUST special-case first)",
			from:             ExternalDeliveryStatusRetryWait,
			downloadURLValid: true,
			want:             ExternalDeliveryStatusQueued, // current: ignores nil URL, returns queued
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.from.CanonicalResume(tc.downloadURLValid); got != tc.want {
				t.Errorf("CanonicalResume(%s, urlValid=%v): want %q, got %q",
					tc.from, tc.downloadURLValid, tc.want, got)
			}
		})
	}
}

// TestExternalDeliveryStatus_CanonicalResume_AlwaysLegal asserts
// that the canonical resume target is always in the transition
// map for retry_wait → either queued or downloading. This is
// a coherence invariant between CanonicalResume and transitionMap:
// if a future edit to transitionMap accidentally removes one of
// the resume targets, CanonicalResume would return an illegal
// suggestion and the worker would error out. Pin this.
func TestExternalDeliveryStatus_CanonicalResume_AlwaysLegal(t *testing.T) {
	for _, valid := range []bool{true, false} {
		target := ExternalDeliveryStatusRetryWait.CanonicalResume(valid)
		if !ExternalDeliveryStatusRetryWait.CanTransitionTo(target) {
			t.Errorf("CanonicalResume(retry_wait, valid=%v) returned illegal target %s", valid, target)
		}
	}
}

// ptrTo returns a pointer to v. Helper for metadata tests.
func ptrTo[T any](v T) *T { return &v }

// TestParseVeloxDeliveryMetadata_Valid parses a representative
// metadata blob and asserts every typed field is decoded.
func TestParseVeloxDeliveryMetadata_Valid(t *testing.T) {
	raw := []byte(`{
		"title":"a title",
		"description":"a description",
		"privacy_status":"private",
		"language":"en-US",
		"timezone":"Europe/Rome",
		"tags":["short","clip"],
		"target_account_ids":[123,456],
		"drive_account_id":42,
		"folder_id":"fid"
	}`)
	m, err := ParseVeloxDeliveryMetadata(raw)
	if err != nil {
		t.Fatalf("ParseVeloxDeliveryMetadata: %v", err)
	}
	if m.Title != "a title" {
		t.Errorf("Title: want %q, got %q", "a title", m.Title)
	}
	if m.Description != "a description" {
		t.Errorf("Description: want %q, got %q", "a description", m.Description)
	}
	if m.PrivacyStatus != "private" {
		t.Errorf("PrivacyStatus: want private, got %q", m.PrivacyStatus)
	}
	if m.Language == nil || *m.Language != "en-US" {
		t.Errorf("Language: want en-US, got %v", m.Language)
	}
	if m.Timezone == nil || *m.Timezone != "Europe/Rome" {
		t.Errorf("Timezone: want Europe/Rome, got %v", m.Timezone)
	}
	if len(m.Tags) != 2 || m.Tags[0] != "short" || m.Tags[1] != "clip" {
		t.Errorf("Tags: want [short clip], got %v", m.Tags)
	}
	if len(m.TargetAccountIDs) != 2 || m.TargetAccountIDs[0] != 123 || m.TargetAccountIDs[1] != 456 {
		t.Errorf("TargetAccountIDs: want [123 456], got %v", m.TargetAccountIDs)
	}
	if m.DriveAccountID == nil || *m.DriveAccountID != 42 {
		t.Errorf("DriveAccountID: want 42, got %v", m.DriveAccountID)
	}
	if m.FolderID == nil || *m.FolderID != "fid" {
		t.Errorf("FolderID: want fid, got %v", m.FolderID)
	}
}

// TestParseVeloxDeliveryMetadata_Errors covers empty and malformed input.
func TestParseVeloxDeliveryMetadata_Errors(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte
	}{
		{"empty", []byte{}},
		{"not json", []byte("not-json")},
		{"not object", []byte("123")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseVeloxDeliveryMetadata(tc.raw); err == nil {
				t.Errorf("expected error for %s", tc.name)
			}
		})
	}
}

// TestVeloxDeliveryMetadata_Validate covers the centralised
// validation rules for the typed metadata struct.
func TestVeloxDeliveryMetadata_Validate(t *testing.T) {
	valid := func() *VeloxDeliveryMetadata {
		return &VeloxDeliveryMetadata{
			Title:            "title",
			Description:      "description",
			PrivacyStatus:    "private",
			Language:         ptrTo("en-US"),
			Timezone:         ptrTo("Europe/Rome"),
			Tags:             []string{"tag1", "tag2"},
			TargetAccountIDs: []int64{1, 2},
			DriveAccountID:   ptrTo[int64](1),
			FolderID:         ptrTo("folder"),
		}
	}

	cases := []struct {
		name    string
		mutate  func(*VeloxDeliveryMetadata)
		wantErr string
	}{
		{"valid", func(m *VeloxDeliveryMetadata) {}, ""},
		{"title only", func(m *VeloxDeliveryMetadata) { m.Description = "" }, ""},
		{"description only", func(m *VeloxDeliveryMetadata) { m.Title = "" }, ""},
		{"empty title and description", func(m *VeloxDeliveryMetadata) {
			m.Title = ""
			m.Description = "   "
		}, "title or description"},
		{"invalid privacy_status", func(m *VeloxDeliveryMetadata) {
			m.PrivacyStatus = "secret"
		}, "privacy_status"},
		{"non-positive target_account_id", func(m *VeloxDeliveryMetadata) {
			m.TargetAccountIDs = []int64{0}
		}, "target_account_ids"},
		{"non-positive drive_account_id", func(m *VeloxDeliveryMetadata) {
			m.DriveAccountID = ptrTo[int64](0)
		}, "drive_account_id"},
		{"empty folder_id", func(m *VeloxDeliveryMetadata) {
			m.FolderID = ptrTo("   ")
		}, "folder_id"},
		{"invalid language", func(m *VeloxDeliveryMetadata) {
			m.Language = ptrTo("123")
		}, "language"},
		{"uppercase language", func(m *VeloxDeliveryMetadata) {
			m.Language = ptrTo("EN-US")
		}, "language"},
		{"invalid timezone", func(m *VeloxDeliveryMetadata) {
			m.Timezone = ptrTo("Mars/Station")
		}, "timezone"},
		{"local timezone", func(m *VeloxDeliveryMetadata) {
			m.Timezone = ptrTo("Local")
		}, "timezone"},
		{"empty tag", func(m *VeloxDeliveryMetadata) {
			m.Tags = []string{"ok", ""}
		}, "tags"},
		{"duplicate tag", func(m *VeloxDeliveryMetadata) {
			m.Tags = []string{"dup", "dup"}
		}, "tags"},
		{"too many tags", func(m *VeloxDeliveryMetadata) {
			m.Tags = make([]string, maxVeloxTags+1)
		}, "too many tags"},
		{"tag too long", func(m *VeloxDeliveryMetadata) {
			m.Tags = []string{strings.Repeat("x", maxVeloxTagLen+1)}
		}, "exceeds"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := valid()
			tc.mutate(m)
			err := m.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Errorf("expected error containing %q, got nil", tc.wantErr)
				return
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}
