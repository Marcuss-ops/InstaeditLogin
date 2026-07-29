package deliveries

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// -----------------------------------------------------------------------
// Test fixtures — in-file fakes (mirror the state_test.go pattern;
// no external fixture file).
// -----------------------------------------------------------------------

// fakeGroupLister is the in-memory ActiveGroupAccountLister
// implementation. accountIDs are returned ascending-sorted so
// callers don't need to re-sort. Err is returned verbatim if set.
type fakeGroupLister struct {
	mu          sync.Mutex
	accountIDs  []int64
	err         error
	listCalls   int
	lastWSID    int64
	lastGroupID int64
}

func (f *fakeGroupLister) ListActiveAccountsInGroup(_ context.Context, workspaceID, groupID int64) ([]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	f.lastWSID = workspaceID
	f.lastGroupID = groupID
	if f.err != nil {
		return nil, f.err
	}
	out := make([]int64, len(f.accountIDs))
	copy(out, f.accountIDs)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// fakeDeliveryInserter mirrors the production wiring contract:
// echo `params.DeliveryID` back unchanged. The id is set by
// ExpandForGroup BEFORE the Insert call (computed deterministically
// via deriveChildID), and the inserter's only job is to persist
// the row + return the canonical id. This is exactly what a
// production Postgres INSERT with explicit `external_delivery_id`
// + `RETURNING id` does, so the same fake shape matches a real
// repo wiring.
//
// The `muteAccount int64` field, if set, causes the inserter to
// return an error for that specific account id so tests can
// exercise the per-child Insert error path.
type fakeDeliveryInserter struct {
	mu          sync.Mutex
	inserted    []ChildDeliveryParams
	muteAccount int64
}

func (f *fakeDeliveryInserter) InsertChildDelivery(_ context.Context, params ChildDeliveryParams) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.muteAccount != 0 && params.PlatformAccountID == f.muteAccount {
		return "", errors.New("simulated per-child insert failure")
	}
	f.inserted = append(f.inserted, params)
	return params.DeliveryID, nil
}

// fakeSetRecorder records the (set row, membership rows) tuple
// emitted by ExpandForGroup's Step 4. Tests inspect the recorded
// slices to verify round-trip correctness.
type fakeSetRecorder struct {
	mu          sync.Mutex
	sets        []DeliverySet
	memberships []DeliverySetMembership
	failSetOn   bool // when true, RecordDeliverySet returns an error
}

func (f *fakeSetRecorder) RecordDeliverySet(_ context.Context, set DeliverySet) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failSetOn {
		return errors.New("simulated set-record failure")
	}
	f.sets = append(f.sets, set)
	return nil
}

func (f *fakeSetRecorder) RecordMemberships(_ context.Context, m []DeliverySetMembership) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.memberships = append(f.memberships, m...)
	return nil
}

// happyParams is the canonical 4-channel-group ExpandForGroupParams
// used by most of the fixture-style tests. The IDs (381/382/442/605)
// match the spec doc §3 examples; the platform is youtube per
// spec §1.
func happyParams() ExpandForGroupParams {
	return ExpandForGroupParams{
		System:      "velox",
		JobID:       "job_123",
		TaskID:      "task_456",
		ArtifactID:  "artifact_abc",
		WorkspaceID: 12,
		Platform:    models.PlatformYouTube,
		GroupID:     27,
	}
}

// -----------------------------------------------------------------------
// Tests
// -----------------------------------------------------------------------

func TestExpandForGroup_Happy_4Channels(t *testing.T) {
	g := &fakeGroupLister{accountIDs: []int64{605, 381, 442, 382}} // shuffled input; deterministic ascending output
	d := &fakeDeliveryInserter{}
	r := &fakeSetRecorder{}

	set, memberships, err := ExpandForGroup(context.Background(), happyParams(), g, d, r)
	if err != nil {
		t.Fatalf("happy 4-channel group: want nil err, got %v", err)
	}
	if set == nil {
		t.Fatalf("happy: set is nil")
	}

	// 4 children, ascending order.
	wantAccounts := []int64{381, 382, 442, 605}
	if len(set.ChildDeliveries) != 4 {
		t.Fatalf("set.ChildDeliveries len: want 4, got %d (%v)", len(set.ChildDeliveries), set.ChildDeliveries)
	}
	if len(memberships) != 4 {
		t.Fatalf("memberships len: want 4, got %d", len(memberships))
	}
	for i, want := range wantAccounts {
		if memberships[i].PlatformAccountID != want {
			t.Errorf("membership[%d].PlatformAccountID: want %d, got %d", i, want, memberships[i].PlatformAccountID)
		}
		if memberships[i].Ordinal != i {
			t.Errorf("membership[%d].Ordinal: want %d, got %d", i, i, memberships[i].Ordinal)
		}
		wantChildID := deriveChildID(happyParams(), want)
		if memberships[i].DeliveryID != wantChildID {
			t.Errorf("membership[%d].DeliveryID: want %q, got %q", i, wantChildID, memberships[i].DeliveryID)
		}
		if set.ChildDeliveries[i] != wantChildID {
			t.Errorf("set.ChildDeliveries[%d]: want %q, got %q", i, wantChildID, set.ChildDeliveries[i])
		}
	}

	// Status: in-progress (children just accepted, haven't run
	// yet). Aggregate against the freshly-accepted children states.
	wantSetID := deriveSetID(happyParams())
	if set.ID != wantSetID {
		t.Errorf("set.ID: want %q, got %q", wantSetID, set.ID)
	}
	if set.Status != DeliverySetStatusInProgress {
		t.Errorf("set.Status: want %s, got %s", DeliverySetStatusInProgress, set.Status)
	}

	// Aggregator call: with 4 fresh children all in delivery_queued (the
	// post-Insert default), the rolled-up status is IN_PROGRESS.
	if got := AggregateStatus([]DeliveryState{
		DeliveryStateDeliveryQueued, DeliveryStateDeliveryQueued,
		DeliveryStateDeliveryQueued, DeliveryStateDeliveryQueued,
	}); got != DeliverySetStatusInProgress {
		t.Errorf("AggregateStatus(4× delivery_queued): want %s, got %s",
			DeliverySetStatusInProgress, got)
	}

	// Then 4 children PUBLISHED → SUCCEEDED.
	if got := AggregateStatus([]DeliveryState{
		DeliveryStatePublished, DeliveryStatePublished,
		DeliveryStatePublished, DeliveryStatePublished,
	}); got != DeliverySetStatusSucceeded {
		t.Errorf("AggregateStatus(4× published): want %s, got %s",
			DeliverySetStatusSucceeded, got)
	}

	// Recorder: 1 set + 4 memberships actually persisted.
	if len(r.sets) != 1 {
		t.Errorf("recorder.sets len: want 1, got %d", len(r.sets))
	}
	if len(r.memberships) != 4 {
		t.Errorf("recorder.memberships len: want 4, got %d", len(r.memberships))
	}

	// Inserter: 4 inserts recorded with correct SetID/Ordinal AND
	// the canonical child id threaded through.
	if len(d.inserted) != 4 {
		t.Fatalf("inserter inserted len: want 4, got %d", len(d.inserted))
	}
	for i, ins := range d.inserted {
		if ins.SetID != wantSetID {
			t.Errorf("inserter[%d].SetID: want %q, got %q", i, wantSetID, ins.SetID)
		}
		if ins.Ordinal != i {
			t.Errorf("inserter[%d].Ordinal: want %d, got %d", i, i, ins.Ordinal)
		}
		wantChildID := deriveChildID(happyParams(), wantAccounts[i])
		if ins.DeliveryID != wantChildID {
			t.Errorf("inserter[%d].DeliveryID: want %q, got %q", i, wantChildID, ins.DeliveryID)
		}
	}
}

func TestExpandForGroup_OneOfFourFails_PARTIAL(t *testing.T) {
	// 3 PUBLISHED + 1 error_exit (NOT cancelled) → PARTIAL.
	if got := AggregateStatus([]DeliveryState{
		DeliveryStatePublished, DeliveryStatePublished,
		DeliveryStateBlockedAuth, // 1 of 4 fails
		DeliveryStatePublished,
	}); got != DeliverySetStatusPartial {
		t.Errorf("AggregateStatus(3 published + 1 blocked_auth): want %s, got %s",
			DeliverySetStatusPartial, got)
	}
}

func TestExpandForGroup_AllFourFailed_FAILED(t *testing.T) {
	cases := []struct {
		name   string
		states []DeliveryState
	}{
		{"all blocked_auth", []DeliveryState{
			DeliveryStateBlockedAuth, DeliveryStateBlockedAuth,
			DeliveryStateBlockedAuth, DeliveryStateBlockedAuth,
		}},
		{"all blocked_target", []DeliveryState{
			DeliveryStateBlockedTarget, DeliveryStateBlockedTarget,
			DeliveryStateBlockedTarget, DeliveryStateBlockedTarget,
		}},
		{"all publish_failed", []DeliveryState{
			DeliveryStatePublishFailed, DeliveryStatePublishFailed,
			DeliveryStatePublishFailed, DeliveryStatePublishFailed,
		}},
		{"mix 4 errors", []DeliveryState{
			DeliveryStateBlockedAuth, DeliveryStateBlockedTarget,
			DeliveryStatePublishFailed, DeliveryStateMediaInvalid,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AggregateStatus(tc.states); got != DeliverySetStatusFailed {
				t.Errorf("AggregateStatus(%s): want %s, got %s",
					tc.name, DeliverySetStatusFailed, got)
			}
		})
	}
}

func TestExpandForGroup_AllFourCancelled_CANCELLED(t *testing.T) {
	if got := AggregateStatus([]DeliveryState{
		DeliveryStateCancelled, DeliveryStateCancelled,
		DeliveryStateCancelled, DeliveryStateCancelled,
	}); got != DeliverySetStatusCancelled {
		t.Errorf("AggregateStatus(4× cancelled): want %s, got %s",
			DeliverySetStatusCancelled, got)
	}
}

func TestExpandForGroup_CancelledDoesNotDemoteSuccess(t *testing.T) {
	// 2 PUBLISHED + 2 CANCELLED → SUCCEEDED (cancelled children
	// neutral; surviving PUBLISHED children carry the set).
	cases := []struct {
		name   string
		states []DeliveryState
	}{
		{"2-published-2-cancelled", []DeliveryState{
			DeliveryStatePublished, DeliveryStateCancelled,
			DeliveryStatePublished, DeliveryStateCancelled,
		}},
		{"3-published-1-cancelled", []DeliveryState{
			DeliveryStatePublished, DeliveryStateCancelled,
			DeliveryStatePublished, DeliveryStatePublished,
		}},
		{"1-published-3-cancelled", []DeliveryState{
			DeliveryStatePublished, DeliveryStateCancelled,
			DeliveryStateCancelled, DeliveryStateCancelled,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AggregateStatus(tc.states); got != DeliverySetStatusSucceeded {
				t.Errorf("AggregateStatus(%s): want %s, got %s",
					tc.name, DeliverySetStatusSucceeded, got)
			}
		})
	}
}

func TestExpandForGroup_CancelledDoesNotMaskFailure(t *testing.T) {
	// 1 PUBLISHED + 1 error + 2 cancelled: precedence —
	// not all cancelled (one cancelled, one published, one error),
	// not in-progress, not all non-cancelled published (one error),
	// published>0 && error>0 → PARTIAL.
	cases := []struct {
		name   string
		states []DeliveryState
		want   DeliverySetStatus
	}{
		{
			"1-published-1-error-2-cancelled",
			[]DeliveryState{
				DeliveryStatePublished, DeliveryStateBlockedAuth,
				DeliveryStateCancelled, DeliveryStateCancelled,
			},
			DeliverySetStatusPartial,
		},
		{
			"0-published-2-error-2-cancelled",
			[]DeliveryState{
				DeliveryStateBlockedAuth, DeliveryStateBlockedTarget,
				DeliveryStateCancelled, DeliveryStateCancelled,
			},
			DeliverySetStatusFailed,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AggregateStatus(tc.states); got != tc.want {
				t.Errorf("AggregateStatus(%s): want %s, got %s",
					tc.name, tc.want, got)
			}
		})
	}
}

func TestExpandForGroup_InProgress_StillRunning(t *testing.T) {
	// Any child non-terminal → IN_PROGRESS, regardless of mix with
	// PUBLISHED / errors / cancelled.
	cases := []struct {
		name   string
		states []DeliveryState
	}{
		{"4-queued", []DeliveryState{
			DeliveryStateDeliveryQueued, DeliveryStateDeliveryQueued,
			DeliveryStateDeliveryQueued, DeliveryStateDeliveryQueued,
		}},
		{"1-published-3-running", []DeliveryState{
			DeliveryStatePublished, DeliveryStateMediaDownloading,
			DeliveryStateThumbnailPending, DeliveryStatePublishing,
		}},
		{"1-published-1-error-2-running", []DeliveryState{
			DeliveryStatePublished, DeliveryStateBlockedAuth,
			DeliveryStateMediaVerified, DeliveryStatePublishing,
		}},
		{"3-published-1-running", []DeliveryState{
			DeliveryStatePublished, DeliveryStatePublished,
			DeliveryStatePublished, DeliveryStatePrivateUploading,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AggregateStatus(tc.states); got != DeliverySetStatusInProgress {
				t.Errorf("AggregateStatus(%s): want %s, got %s",
					tc.name, DeliverySetStatusInProgress, got)
			}
		})
	}
}

func TestExpandForGroup_GroupEmpty(t *testing.T) {
	g := &fakeGroupLister{accountIDs: nil}
	d := &fakeDeliveryInserter{}
	r := &fakeSetRecorder{}

	set, memberships, err := ExpandForGroup(context.Background(), happyParams(), g, d, r)
	if !errors.Is(err, ErrGroupEmpty) {
		t.Errorf("want ErrGroupEmpty, got %v", err)
	}
	if set != nil {
		t.Errorf("set must be nil on GROUP_EMPTY, got %+v", set)
	}
	if memberships != nil {
		t.Errorf("memberships must be nil on GROUP_EMPTY, got %+v", memberships)
	}
	if len(d.inserted) != 0 {
		t.Errorf("inserter must NOT fire on GROUP_EMPTY, got %d inserts", len(d.inserted))
	}
	if len(r.sets) != 0 {
		t.Errorf("recorder must NOT fire on GROUP_EMPTY, got %d sets", len(r.sets))
	}
}

func TestExpandForGroup_ReplayDeterministicIDs(t *testing.T) {
	// Two runs of ExpandForGroup with the SAME params + same
	// lister/inserter/recorder must produce IDENTICAL set IDs +
	// identical child IDs in identical order. A producer replay
	// of the same Idempotency-Key (per spec §7.2) reproduces the
	// same set/child tuple so the worker pool's INSERT-OR-DEDUP
	// path stays correct.
	g1 := &fakeGroupLister{accountIDs: []int64{381, 382, 442, 605}}
	d1 := &fakeDeliveryInserter{}
	r1 := &fakeSetRecorder{}
	set1, ms1, err := ExpandForGroup(context.Background(), happyParams(), g1, d1, r1)
	if err != nil {
		t.Fatalf("replay 1: %v", err)
	}

	g2 := &fakeGroupLister{accountIDs: []int64{381, 382, 442, 605}}
	d2 := &fakeDeliveryInserter{}
	r2 := &fakeSetRecorder{}
	set2, ms2, err := ExpandForGroup(context.Background(), happyParams(), g2, d2, r2)
	if err != nil {
		t.Fatalf("replay 2: %v", err)
	}

	if set1.ID != set2.ID {
		t.Errorf("set.ID drift across replays: %q vs %q", set1.ID, set2.ID)
	}
	if len(set1.ChildDeliveries) != len(set2.ChildDeliveries) {
		t.Fatalf("len drift: %d vs %d", len(set1.ChildDeliveries), len(set2.ChildDeliveries))
	}
	for i := range set1.ChildDeliveries {
		if set1.ChildDeliveries[i] != set2.ChildDeliveries[i] {
			t.Errorf("child[%d] drift: %q vs %q",
				i, set1.ChildDeliveries[i], set2.ChildDeliveries[i])
		}
		if ms1[i].DeliveryID != ms2[i].DeliveryID {
			t.Errorf("membership[%d].DeliveryID drift: %q vs %q",
				i, ms1[i].DeliveryID, ms2[i].DeliveryID)
		}
		if ms1[i].PlatformAccountID != ms2[i].PlatformAccountID {
			t.Errorf("membership[%d].PlatformAccountID drift", i)
		}
	}
}

func TestExpandForGroup_SortedAscendingEvenWithShuffledLister(t *testing.T) {
	// Producer order: 605, 381, 442, 382. Determinism rule says
	// children get ordinals 0..3 mapped to ASCENDING account id
	// (381, 382, 442, 605) regardless of lister order.
	g := &fakeGroupLister{accountIDs: []int64{605, 381, 442, 382}}
	d := &fakeDeliveryInserter{}
	r := &fakeSetRecorder{}

	_, memberships, err := ExpandForGroup(context.Background(), happyParams(), g, d, r)
	if err != nil {
		t.Fatalf("shuffled lister: %v", err)
	}
	want := []int64{381, 382, 442, 605}
	for i, m := range memberships {
		if m.PlatformAccountID != want[i] {
			t.Errorf("membership[%d]: want account %d, got %d (determinism violation)", i, want[i], m.PlatformAccountID)
		}
	}
}

func TestExpandForGroup_ValidateRejectsMalformed(t *testing.T) {
	cases := []struct {
		name    string
		params  ExpandForGroupParams
		wantErr string
	}{
		{
			"non-velox system",
			func() ExpandForGroupParams {
				p := happyParams()
				p.System = "unknown"
				return p
			}(),
			"system must be 'velox'",
		},
		{
			"empty job_id",
			func() ExpandForGroupParams {
				p := happyParams()
				p.JobID = ""
				return p
			}(),
			"job_id must be non-empty",
		},
		{
			"platform not youtube",
			func() ExpandForGroupParams {
				p := happyParams()
				p.Platform = "tiktok"
				return p
			}(),
			"platform must be",
		},
		{
			"group_id <= 0",
			func() ExpandForGroupParams {
				p := happyParams()
				p.GroupID = 0
				return p
			}(),
			"group_id must be > 0",
		},
		{
			"workspace_id <= 0",
			func() ExpandForGroupParams {
				p := happyParams()
				p.WorkspaceID = 0
				return p
			}(),
			"workspace_id must be > 0",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.params.Validate(); err == nil {
				t.Fatalf("want err containing %q, got nil", tc.wantErr)
			} else if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err msg: want substring %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

func TestExpandForGroup_PartialInsertFailureBubbles(t *testing.T) {
	// 4-channel group, account 442 insert fails. ExpandForGroup
	// must abort BEFORE persisting either set or memberships.
	g := &fakeGroupLister{accountIDs: []int64{381, 382, 442, 605}}
	d := &fakeDeliveryInserter{muteAccount: 442}
	r := &fakeSetRecorder{}

	_, _, err := ExpandForGroup(context.Background(), happyParams(), g, d, r)
	if err == nil {
		t.Fatalf("want err on per-child insert failure; got nil")
	}
	if !strings.Contains(err.Error(), "insert child 442") {
		t.Errorf("err msg: want substring 'insert child 442', got %q", err.Error())
	}
	if len(r.sets) != 0 {
		t.Errorf("recorder.sets must NOT fire on partial insert failure, got %d", len(r.sets))
	}
	if len(r.memberships) != 0 {
		t.Errorf("recorder.memberships must NOT fire on partial insert failure, got %d", len(r.memberships))
	}
}

func TestExpandForGroup_NilDepsRejected(t *testing.T) {
	g := &fakeGroupLister{accountIDs: []int64{381}}
	d := &fakeDeliveryInserter{}
	r := &fakeSetRecorder{}

	cases := []struct {
		name string
		recv func() ActiveGroupAccountLister
		d    func() DeliveryInserter
		rec  func() DeliverySetRecorder
	}{
		{"nil-lister", func() ActiveGroupAccountLister { return nil }, func() DeliveryInserter { return d }, func() DeliverySetRecorder { return r }},
		{"nil-inserter", func() ActiveGroupAccountLister { return g }, func() DeliveryInserter { return nil }, func() DeliverySetRecorder { return r }},
		{"nil-recorder", func() ActiveGroupAccountLister { return g }, func() DeliveryInserter { return d }, func() DeliverySetRecorder { return nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ExpandForGroup(context.Background(), happyParams(),
				tc.recv(), tc.d(), tc.rec())
			if err == nil {
				t.Fatalf("want err on nil dep; got nil")
			}
		})
	}
}

// TestAggregateStatus_Combinatorics walks the canonical 4-channel-
// group scenarios plus the edge cases that surfaced during design
// review. A protective pin against an off-by-one in the
// precedence rules.
func TestAggregateStatus_Combinatorics(t *testing.T) {
	// Empty → PENDING (degenerate; caller bug).
	if got := AggregateStatus(nil); got != DeliverySetStatusPending {
		t.Errorf("empty: want PENDING, got %s", got)
	}

	now := time.Now().UTC()
	_ = now // unused; defensive against linter "declared but not used" if assertions removed
	scenarios := []struct {
		name   string
		states []DeliveryState
		want   DeliverySetStatus
	}{
		{"happy-4-published", []DeliveryState{
			DeliveryStatePublished, DeliveryStatePublished,
			DeliveryStatePublished, DeliveryStatePublished,
		}, DeliverySetStatusSucceeded},
		{"1-of-4-failed", []DeliveryState{
			DeliveryStatePublished, DeliveryStatePublished,
			DeliveryStateBlockedAuth, DeliveryStatePublished,
		}, DeliverySetStatusPartial},
		{"4-of-4-failed", []DeliveryState{
			DeliveryStateBlockedAuth, DeliveryStateBlockedTarget,
			DeliveryStatePublishFailed, DeliveryStateMediaInvalid,
		}, DeliverySetStatusFailed},
		{"all-cancelled", []DeliveryState{
			DeliveryStateCancelled, DeliveryStateCancelled,
			DeliveryStateCancelled, DeliveryStateCancelled,
		}, DeliverySetStatusCancelled},
		{"cancelled-doesnt-demote-success", []DeliveryState{
			DeliveryStatePublished, DeliveryStateCancelled,
			DeliveryStatePublished, DeliveryStateCancelled,
		}, DeliverySetStatusSucceeded},
		{"cancelled-doesnt-mask-partial", []DeliveryState{
			DeliveryStatePublished, DeliveryStateBlockedAuth,
			DeliveryStateCancelled, DeliveryStateCancelled,
		}, DeliverySetStatusPartial},
		{"cancelled-doesnt-mask-failed", []DeliveryState{
			DeliveryStateBlockedAuth, DeliveryStateBlockedTarget,
			DeliveryStateCancelled, DeliveryStateCancelled,
		}, DeliverySetStatusFailed},
		{"in-progress-overrides-success", []DeliveryState{
			DeliveryStatePublished, DeliveryStatePublished,
			DeliveryStatePublished, DeliveryStatePrivateUploading,
		}, DeliverySetStatusInProgress},
		{"in-progress-with-error", []DeliveryState{
			DeliveryStatePublished, DeliveryStateBlockedAuth,
			DeliveryStateMediaVerified, DeliveryStatePublishing,
		}, DeliverySetStatusInProgress},
	}
	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			if got := AggregateStatus(s.states); got != s.want {
				t.Errorf("%s: want %s, got %s", s.name, s.want, got)
			}
		})
	}
}

// TestDeliverySetStatus_IsTerminal pins the terminal-class
// partition of the 6-value enum. Mirrors DeliveryState.IsTerminal
// for the per-parent layer.
func TestDeliverySetStatus_IsTerminal(t *testing.T) {
	for _, s := range AllDeliverySetStatuses() {
		switch s {
		case DeliverySetStatusSucceeded, DeliverySetStatusPartial,
			DeliverySetStatusFailed, DeliverySetStatusCancelled:
			if !s.IsTerminal() {
				t.Errorf("%s should be terminal; is not", s)
			}
		default:
			if s.IsTerminal() {
				t.Errorf("%s should NOT be terminal; is", s)
			}
		}
	}
}

// TestAggregateStatus_All21StatesClassifiedExactlyOnce pins the
// per-state singleton classification: when a single child is in
// any of the 21 canonical DeliveryState values, the rolled-up
// DeliverySetStatus is fully determined by that single value.
// This catches two regression classes at once:
//
//  1. A NEW DeliveryState added to state.go without updating the
//     branches in AggregateStatus would silently classify as
//     `errorExits` (or `inProgress`) and the test would fail when
//     the new state lacks an entry in `want`.
//  2. An accidental reordering that double-counts Published would
//     flip SUCCEEDED into FAILED for the singleton case.
//
// The table below MUST stay aligned with the transitionMap in
// state.go: each terminal state's class (published / cancelled /
// errorExit) maps to the expected singleton rollup; non-terminal
// states always rollup to IN_PROGRESS.
func TestAggregateStatus_All21StatesClassifiedExactlyOnce(t *testing.T) {
	want := map[DeliveryState]DeliverySetStatus{
		// 13 happy-path forward (non-terminal) → IN_PROGRESS.
		DeliveryStateDeliveryQueued:      DeliverySetStatusInProgress,
		DeliveryStateTargetValidating:    DeliverySetStatusInProgress,
		DeliveryStateTargetValidated:     DeliverySetStatusInProgress,
		DeliveryStateMediaDownloading:    DeliverySetStatusInProgress,
		DeliveryStateMediaVerified:       DeliverySetStatusInProgress,
		DeliveryStatePrivateUploadQueued: DeliverySetStatusInProgress,
		DeliveryStatePrivateUploading:    DeliverySetStatusInProgress,
		DeliveryStatePrivateUploaded:     DeliverySetStatusInProgress,
		DeliveryStateThumbnailPending:    DeliverySetStatusInProgress,
		DeliveryStateThumbnailUploading:  DeliverySetStatusInProgress,
		DeliveryStateThumbnailApplied:    DeliverySetStatusInProgress,
		DeliveryStateReadyToPublish:      DeliverySetStatusInProgress,
		DeliveryStatePublishing:          DeliverySetStatusInProgress,
		// Terminal success → SUCCEEDED (singleton = 1/1 published).
		DeliveryStatePublished: DeliverySetStatusSucceeded,
		// 6 terminal errors → FAILED (singleton = 1 error, 0 published).
		DeliveryStateBlockedTarget:       DeliverySetStatusFailed,
		DeliveryStateBlockedAuth:         DeliverySetStatusFailed,
		DeliveryStateMediaInvalid:        DeliverySetStatusFailed,
		DeliveryStatePrivateUploadFailed: DeliverySetStatusFailed,
		DeliveryStateThumbnailFailed:     DeliverySetStatusFailed,
		DeliveryStatePublishFailed:       DeliverySetStatusFailed,
		// Terminal neutral → CANCELLED (singleton = all cancelled).
		DeliveryStateCancelled: DeliverySetStatusCancelled,
	}

	for _, s := range AllDeliveryStates() {
		got := AggregateStatus([]DeliveryState{s})
		expected, ok := want[s]
		if !ok {
			t.Errorf("DeliveryState(%q) has no singleton expectation; AggregateStatus returned %s. The map above guards against a future state addition.", s, got)
			continue
		}
		if got != expected {
			t.Errorf("DeliveryState(%q) singleton: want %s, got %s", s, expected, got)
		}
	}
	// Empty-slice degenerate case (caller bug; should be PENDING).
	if got := AggregateStatus(nil); got != DeliverySetStatusPending {
		t.Errorf("nil children: want PENDING, got %s", got)
	}
}
