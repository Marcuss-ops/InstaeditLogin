//go:build e2e

package e2e

import (
	"testing"
)

// TestPipelineE2E is the Task 9/10 headliner: the suite that proves
// the full Drive → ingest → S3 → publish → Velox-callback pipeline
// holds up against the "Definition of Done" 7-bucket acceptance
// criteria the source document enumerates, plus the 4 extended
// scenarios (8-11) for lease, retry budget, dead-letter terminality,
// and HMAC signature verification.
//
// Structure: one top-level test with 12 t.Run subtests. Each subtest
// shares the E2EHarness fixture (Postgres via testcontainers + the
// 3 httptest fakes). Per-subtest setup creates user + workspace IDs
// with timestamps so the subtests are independently runnable.
//
// The scenario functions live in concern-scoped files:
//
//	pipeline_e2e_scenarios1_4_test.go   — drive ingest, crash resume,
//	                                      velox idempotency, S3 verify
//	pipeline_e2e_scenarios5_8_test.go   — post scheduling, YouTube
//	                                      crash, velox callback, lease
//	pipeline_e2e_scenarios9_12_test.go  — retry budget, dead-letter
//	                                      terminal, HMAC, heartbeat
//
// Shared helpers (insertPublishTarget / acquireLeaseInTx /
// attemptAcquireWithNowait / updateTargetStatus / artifactVerifyOK /
// insertScheduledPost / runPublishClaimGate / transientErrMsg) live in
// e2e_harness_helpers.go so the harness layer owns shared fixture
// tooling; the scenario files only contain the orchestration +
// per-test assertions.
//
// Why in-process instead of full containerised API+worker:
//
//   - The pipeline agreement lives in repository + service code,
//     not in HTTP layer wiring. Going in-process keeps the test
//     focused on what flows (rows, SHA, MIME, IDs, state) without
//     chasing HTTP transport bugs the user-facing E2E suite
//     (pkg/api/internal_velox_e2e_test.go) already covers.
//   - CI runners already have docker for testcontainers-go. No
//     new infra dependency.
func TestPipelineE2E(t *testing.T) {
	h := NewE2EHarness(t)
	if h == nil {
		t.Skip("E2EHarness: precondition unmet (Docker unavailable or container start failed)")
		return
	}
	defer h.Close()

	// Subtests (sequential; each resets per-subtest mutable state
	// on the fakes, owns its data set).
	t.Run("scenario_1_drive_ingest_201_videos_two_pages_no_duplicates", func(t *testing.T) {
		h.ResetFakes()
		scenario1_DriveIngest(t, h)
	})
	t.Run("scenario_2_crash_mid_crawl_resume_from_page_2", func(t *testing.T) {
		h.ResetFakes()
		scenario2_CrashMidCrawl(t, h)
	})
	t.Run("scenario_3_velox_idempotency_same_vs_diff_sha", func(t *testing.T) {
		h.ResetFakes()
		scenario3_VeloxIdempotency(t, h)
	})
	t.Run("scenario_4_s3_minio_verify_sha_size_mime", func(t *testing.T) {
		h.ResetFakes()
		scenario4_S3Verify(t, h)
	})
	t.Run("scenario_5_post_scheduling_publish_at_future_no_early_publish", func(t *testing.T) {
		h.ResetFakes()
		scenario5_PostScheduling(t, h)
	})
	t.Run("scenario_6_youtube_resumable_crash_recovery", func(t *testing.T) {
		h.ResetFakes()
		scenario6_YouTubeCrash(t, h)
	})
	t.Run("scenario_7_velox_callback_final", func(t *testing.T) {
		h.ResetFakes()
		scenario7_VeloxCallback(t, h)
	})
	t.Run("scenario_8_lease_contention_two_workers_one_winner", func(t *testing.T) {
		h.ResetFakes()
		scenario8_LeaseContention(t, h)
	})
	t.Run("scenario_9_retry_budget_exhaustion_flip_to_dead_letter", func(t *testing.T) {
		h.ResetFakes()
		scenario9_RetryBudgetExhaustion(t, h)
	})
	t.Run("scenario_10_dead_letter_terminal_no_further_transitions", func(t *testing.T) {
		h.ResetFakes()
		scenario10_DeadLetterTerminal(t, h)
	})
	t.Run("scenario_11_velox_callback_hmac_signature_verify", func(t *testing.T) {
		h.ResetFakes()
		scenario11_VeloxCallbackHMAC(t, h)
	})
	t.Run("scenario_12_heartbeat_staleness_reclaim", func(t *testing.T) {
		h.ResetFakes()
		scenario12_HeartbeatReclaim(t, h)
	})
}
