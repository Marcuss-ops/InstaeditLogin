package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// contractValidEnv returns a fresh env pair (deliveries + destinations)
// wired so even the contract path's synthesised destination id won't
// collide with a real destination row in the fake store. The contract
// path never calls GetByID on ExternalDestinationStore, so an empty
// `destinations` map is fine.
func contractValidEnv() (ExternalDeliveryStore, ExternalDestinationStore) {
	return newFakeDeliveryEnv(), &fakeDestinationEnv{rows: map[string]*models.ExternalDestination{}}
}

// buildValidContractVeloxRequest constructs a contract-shape body that
// passes every check in `validateContractRequest`. Tests override
// individual fields via json.Unmarshal+Marshal to surface validation
// rejections.
func buildValidContractVeloxRequest(t *testing.T, jobID, artifactID string, workspaceID, platformAccountID int64) []byte {
	t.Helper()
	payload := map[string]any{
		// contract_version is the SPEC §7.1 DISCRIMINATOR — without it
		// the handler falls through to the legacy path (which expects
		// `idempotency_key` in the body) and the live contract tests
		// fail with 422 "idempotency_key is required".
		"contract_version": ContractVersionV1,
		"source": map[string]any{
			"system":      "velox",
			"job_id":      jobID,
			"task_id":     "task_" + jobID,
			"artifact_id": artifactID,
		},
		"media": map[string]any{
			"download_url":     "https://velox.internal/artifacts/" + artifactID + "/download",
			"sha256":           "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
			"size_bytes":       1915469,
			"mime_type":        "video/mp4",
			"duration_seconds": 180.4,
		},
		"destination": map[string]any{
			"workspace_id":        workspaceID,
			"platform":            "youtube",
			"target_type":         "channel",
			"platform_account_id": platformAccountID,
		},
		"publication": map[string]any{
			"title":             "Contract Test Title",
			"description":       "Contract test description",
			"initial_privacy":   "private",
			"final_privacy":     "public",
			"require_thumbnail": true,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return body
}

// fireContractDeliveryRequest posts the body with auth + the canonical
// Idempotency-Key header set. Mirrors firePostDeliveryRequest but adds
// the header required by the contract path.
func fireContractDeliveryRequest(t *testing.T, r *Router, body []byte, authHeader, idempotencyKey string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/deliveries", strings.NewReader(string(body)))
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("Idempotency-Key", idempotencyKey)
	w := httptest.NewRecorder()
	r.mux.ServeHTTP(w, req)
	return w
}

// TestContractDelivery_HappyPath pins that a contract-shape body with
// the canonical Idempotency-Key header returns 202 + the
// contract-shaped response (`delivery_id`, `status:"accepted"`,
// `duplicate:false`). Sanity: insertCallCount=1 (validation fires).
func TestContractDelivery_HappyPath(t *testing.T) {
	store, destStore := contractValidEnv()
	r := newPostVeloxTestRouter(t, store, destStore, "secret-token")
	body := buildValidContractVeloxRequest(t, "job_001", "artifact_001", 12, 381)
	key := "velox-job_001-artifact_001-youtube-381"
	w := fireContractDeliveryRequest(t, r, body, "Bearer secret-token", key)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status=%d want 202 body=%s", w.Code, w.Body.String())
	}
	var resp VeloxDeliverContractResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != "accepted" {
		t.Errorf("Status=%q want accepted", resp.Status)
	}
	if !strings.HasPrefix(resp.DeliveryID, "sdel_01J") {
		t.Errorf("DeliveryID=%q want sdel_01J prefix", resp.DeliveryID)
	}
	if resp.Duplicate {
		t.Errorf("Duplicate=true want false (fresh insert)")
	}
	if store.(*fakeDeliveryEnv).insertCallCount != 1 {
		t.Errorf("insertCallCount=%d want 1", store.(*fakeDeliveryEnv).insertCallCount)
	}
}

// TestContractDelivery_IdempotentReplay_SameBody pins the spec
// invariant: SAME Idempotency-Key + SAME body SHA → handler Insert
// returns the pre-seeded row (mintedID != inserted.ID) → 202 +
// `duplicate:true`. Producer can safely replay the same body after a
// network blip without triggering duplicate work downstream.
func TestContractDelivery_IdempotentReplay_SameBody(t *testing.T) {
	preSeededID := "sdel_01JREPLAY"
	fde := newFakeDeliveryEnv()
	fde.insertReturnValue = &models.ExternalDelivery{
		ID: preSeededID, SourceSystem: "velox",
		Status: models.ExternalDeliveryStatusAccepted,
	}
	destStore := &fakeDestinationEnv{rows: map[string]*models.ExternalDestination{}}
	r := newPostVeloxTestRouter(t, fde, destStore, "secret-token")
	body := buildValidContractVeloxRequest(t, "job_002", "artifact_002", 12, 381)
	key := "velox-job_002-artifact_002-youtube-381"
	w := fireContractDeliveryRequest(t, r, body, "Bearer secret-token", key)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status=%d want 202 body=%s", w.Code, w.Body.String())
	}
	var resp VeloxDeliverContractResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.DeliveryID != preSeededID {
		t.Errorf("DeliveryID=%q want %q (replay reuses pre-seeded row)",
			resp.DeliveryID, preSeededID)
	}
	if !resp.Duplicate {
		t.Errorf("Duplicate=false want true (mint id != inserted id collision)")
	}
	if fde.insertCallCount != 1 {
		t.Errorf("insertCallCount=%d want 1", fde.insertCallCount)
	}
}

// TestContractDelivery_Conflict_DifferentBody pins SAME key +
// DIFFERENT body SHA → ErrIdempotencyConflict → 409 with the
// structured VeloxDeliverArtifactConflictResponse.
// Producer MUST regenerate a fresh Idempotency-Key for the new
// payload; retrying with the same key always re-triggers 409.
func TestContractDelivery_Conflict_DifferentBody(t *testing.T) {
	fde := newFakeDeliveryEnv()
	fde.insertReturnErr = repository.ErrIdempotencyConflict
	destStore := &fakeDestinationEnv{rows: map[string]*models.ExternalDestination{}}
	r := newPostVeloxTestRouter(t, fde, destStore, "secret-token")
	body := buildValidContractVeloxRequest(t, "job_003", "artifact_003", 12, 381)
	key := "velox-job_003-artifact_003-youtube-381"
	w := fireContractDeliveryRequest(t, r, body, "Bearer secret-token", key)

	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d want 409 body=%s", w.Code, w.Body.String())
	}
	var resp VeloxDeliverArtifactConflictResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Code != "idempotency_key_conflict" {
		t.Errorf("Code=%q want idempotency_key_conflict", resp.Code)
	}
	if resp.IdempotencyKey != key {
		t.Errorf("IdempotencyKey=%q want %q", resp.IdempotencyKey, key)
	}
	if fde.insertCallCount != 1 {
		t.Errorf("insertCallCount=%d want 1", fde.insertCallCount)
	}
}

// TestContractDelivery_InvalidIdempotencyKeyFormat pins the regex
// enforcement: a key that does NOT match `^velox-[^-]+-[^-]+-[^-]+-[^-]+$`
// is rejected with 422 BEFORE the Insert call (insertCallCount=0).
// Uses the legacy `delivery_8cc0f|destination_12` shape to demonstrate
// that legacy-format keys fail the new contract path.
func TestContractDelivery_InvalidIdempotencyKeyFormat(t *testing.T) {
	store, destStore := newFakeDeliveryEnv(), &fakeDestinationEnv{rows: map[string]*models.ExternalDestination{}}
	r := newPostVeloxTestRouter(t, store, destStore, "secret-token")
	body := buildValidContractVeloxRequest(t, "job_004", "artifact_004", 12, 381)
	badKey := "delivery_8cc0f|destination_12" // legacy format, not contract
	w := fireContractDeliveryRequest(t, r, body, "Bearer secret-token", badKey)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d want 422 body=%s", w.Code, w.Body.String())
	}
	// Go's encoding/json escapes < > & as \u003c \u003e \u0026 in
	// JSON strings by default — the format placeholder embedded in
	// the validator's error message lands in the response body as
	// the literal ASCII-sequence "\u003cjob\u003e" (NOT as "<job>"
	// bytes). Match the raw output bytes; the backslashes are real
	// raw backslash characters.
	if !strings.Contains(w.Body.String(), "velox-\\u003cjob\\u003e") {
		t.Errorf("body should explain the contract format; got %s", w.Body.String())
	}
	if store.insertCallCount != 0 {
		t.Errorf("insertCallCount=%d want 0 (validation must short-circuit Insert)", store.insertCallCount)
	}
}

// TestContractDelivery_IdempotencyKeySegmentsDoNotMatchBody pins
// the segment-vs-body reconciliation: a well-formatted Idempotency-Key
// whose job_id segment disagrees with `source.job_id` is rejected
// with 422. Catches a mis-wired producer that mints a valid key off
// the wrong source triple.
func TestContractDelivery_IdempotencyKeySegmentsDoNotMatchBody(t *testing.T) {
	store, destStore := newFakeDeliveryEnv(), &fakeDestinationEnv{rows: map[string]*models.ExternalDestination{}}
	r := newPostVeloxTestRouter(t, store, destStore, "secret-token")
	body := buildValidContractVeloxRequest(t, "job_real", "artifact_real", 12, 381)
	// header uses lowercase-only segments (passes regex) but the
	// job_id + artifact_id segments disagree with the body
	// `source.job_id`/`source.artifact_id`, exercising the
	// per-segment reconciliation path in validateContractRequest.
	mismatchKey := "velox-job_zzz-artifact_zzz-youtube-381"
	w := fireContractDeliveryRequest(t, r, body, "Bearer secret-token", mismatchKey)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d want 422 body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "job_id") {
		t.Errorf("body should mention job_id mismatch; got %s", w.Body.String())
	}
	if store.insertCallCount != 0 {
		t.Errorf("insertCallCount=%d want 0", store.insertCallCount)
	}
}

// TestContractDelivery_InitialPrivacyMustBePrivate pins the hard
// invariant: publication.initial_privacy MUST be "private" (any
// other value is rejected with 422). The system MUST NEVER accept
// a public/private-by-design at the accept-delivery stage.
func TestContractDelivery_InitialPrivacyMustBePrivate(t *testing.T) {
	store, destStore := newFakeDeliveryEnv(), &fakeDestinationEnv{rows: map[string]*models.ExternalDestination{}}
	r := newPostVeloxTestRouter(t, store, destStore, "secret-token")
	body := buildValidContractVeloxRequest(t, "job_006", "artifact_006", 12, 381)
	var p map[string]any
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	p["publication"].(map[string]any)["initial_privacy"] = "public"
	body, _ = json.Marshal(p)
	key := "velox-job_006-artifact_006-youtube-381"
	w := fireContractDeliveryRequest(t, r, body, "Bearer secret-token", key)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d want 422 body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "initial_privacy") {
		t.Errorf("body should mention initial_privacy; got %s", w.Body.String())
	}
	if store.insertCallCount != 0 {
		t.Errorf("insertCallCount=%d want 0", store.insertCallCount)
	}
}

// TestContractDelivery_DurationMustBePositive pins media.duration_seconds
// strict positivity. A 0/negative value is rejected with 422 BEFORE
// the Insert call; the worker would otherwise fail on ffprobe but
// validation short-circuits the wasted DB round-trip.
func TestContractDelivery_DurationMustBePositive(t *testing.T) {
	store, destStore := newFakeDeliveryEnv(), &fakeDestinationEnv{rows: map[string]*models.ExternalDestination{}}
	r := newPostVeloxTestRouter(t, store, destStore, "secret-token")
	body := buildValidContractVeloxRequest(t, "job_005", "artifact_005", 12, 381)
	var p map[string]any
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	p["media"].(map[string]any)["duration_seconds"] = 0
	body, _ = json.Marshal(p)
	key := "velox-job_005-artifact_005-youtube-381"
	w := fireContractDeliveryRequest(t, r, body, "Bearer secret-token", key)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d want 422 body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "duration_seconds") {
		t.Errorf("body should mention duration_seconds; got %s", w.Body.String())
	}
	if store.insertCallCount != 0 {
		t.Errorf("insertCallCount=%d want 0", store.insertCallCount)
	}
}

// TestContractDelivery_DownloadURLMustBeHTTPS pins the transport
// requirement: download_url MUST be HTTPS (HTTP/HTTPS-or-other is
// rejected). Closes the man-in-the-middle vector on producer-side
// signed URLs.
func TestContractDelivery_DownloadURLMustBeHTTPS(t *testing.T) {
	store, destStore := newFakeDeliveryEnv(), &fakeDestinationEnv{rows: map[string]*models.ExternalDestination{}}
	r := newPostVeloxTestRouter(t, store, destStore, "secret-token")
	body := buildValidContractVeloxRequest(t, "job_007", "artifact_007", 12, 381)
	var p map[string]any
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	p["media"].(map[string]any)["download_url"] = "http://velox.internal/artifacts/artifact_007/download"
	body, _ = json.Marshal(p)
	key := "velox-job_007-artifact_007-youtube-381"
	w := fireContractDeliveryRequest(t, r, body, "Bearer secret-token", key)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d want 422 body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "HTTPS") {
		t.Errorf("body should mention HTTPS; got %s", w.Body.String())
	}
	if store.insertCallCount != 0 {
		t.Errorf("insertCallCount=%d want 0", store.insertCallCount)
	}
}

// TestContractDelivery_GroupRequiresGroupID pins target_type=group
// requires destination.group_id (NOT platform_account_id). Mirrors
// the channel-with-platform_account_id requirement symmetrically.
func TestContractDelivery_GroupRequiresGroupID(t *testing.T) {
	store, destStore := newFakeDeliveryEnv(), &fakeDestinationEnv{rows: map[string]*models.ExternalDestination{}}
	r := newPostVeloxTestRouter(t, store, destStore, "secret-token")
	body := buildValidContractVeloxRequest(t, "job_008", "artifact_008", 12, 381)
	var p map[string]any
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	d := p["destination"].(map[string]any)
	d["target_type"] = "group"
	delete(d, "platform_account_id")
	d["group_id"] = int64(27)
	body, _ = json.Marshal(p)
	// Idempotency-Key's AccountOrGroup segment must reconcile to 27.
	key := "velox-job_008-artifact_008-youtube-27"
	w := fireContractDeliveryRequest(t, r, body, "Bearer secret-token", key)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status=%d want 202 body=%s group target should pass with group_id=27 + matching key",
			w.Code, w.Body.String())
	}
	if store.insertCallCount != 1 {
		t.Errorf("insertCallCount=%d want 1", store.insertCallCount)
	}
}

// TestContractDelivery_LegacyShapeUsesLegacyResponse pins that the
// legacy VeloxDeliverArtifactRequest shape still fires the legacy
// response shape (no contract breach on existing fixtures). An old
// producer is unchanged.
func TestContractDelivery_LegacyShapeUsesLegacyResponse(t *testing.T) {
	fde := newFakeDeliveryEnv()
	destStore := &fakeDestinationEnv{rows: map[string]*models.ExternalDestination{}}
	// Seed one destination so the legacy path's destination lookup succeeds.
	destStore.rows["extdst_01JABC"] = &models.ExternalDestination{
		ID: "extdst_01JABC", SourceSystem: "velox", Enabled: true,
	}
	r := newPostVeloxTestRouter(t, fde, destStore, "secret-token")
	body := buildValidVeloxDeliveryRequest(t, "delivery_lgcy|destination_12", "delivery_lgcy")
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/deliveries", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer secret-token")
	w := httptest.NewRecorder()
	r.mux.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status=%d want 202 body=%s", w.Code, w.Body.String())
	}
	var resp VeloxDeliverArtifactResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.HasPrefix(resp.SocialDeliveryID, "sdel_01J") {
		t.Errorf("SocialDeliveryID=%q want sdel_01J prefix (legacy response shape)", resp.SocialDeliveryID)
	}
	if resp.AlreadyExists {
		t.Errorf("AlreadyExists=true want false (fresh insert on legacy path)")
	}
	if fde.insertCallCount != 1 {
		t.Errorf("insertCallCount=%d want 1", fde.insertCallCount)
	}
}

// TestParseVeloxContractIdempotencyKey pins the canonical-format
// parser. Round-trips through a few valid + invalid keys.
func TestParseVeloxContractIdempotencyKey(t *testing.T) {
	cases := []struct {
		in           string
		wantOK       bool
		wantJob      string
		wantArtifact string
		wantPlatform string
		wantAcct     string
	}{
		{"velox-job_123-artifact_abc-youtube-account_381", true, "job_123", "artifact_abc", "youtube", "account_381"},
		{"velox-j-a-b-c", true, "j", "a", "b", "c"},
		{"velox-job-foo-bar", false, "", "", "", ""},                          // 3 trailing segments
		{"prefix-velox-job-artifact-platform-account", false, "", "", "", ""}, // doesn't start with velox-
		{"velox--artifact-platform-account", false, "", "", "", ""},           // empty job segment
		{"", false, "", "", "", ""},                                           // empty
		{"velox-JOB-artifact-platform-account", false, "", "", "", ""},        // uppercase rejected by [a-z0-9_]+
		{"VELOX-job-artifact-platform-account", false, "", "", "", ""},        // capital V rejected
	}
	for _, c := range cases {
		c := c
		t.Run("key="+c.in, func(t *testing.T) {
			parts, ok := ParseVeloxContractIdempotencyKey(c.in)
			if ok != c.wantOK {
				t.Fatalf("ok=%v want %v (input=%q)", ok, c.wantOK, c.in)
			}
			if !ok {
				return
			}
			if parts.JobID != c.wantJob || parts.ArtifactID != c.wantArtifact ||
				parts.Platform != c.wantPlatform || parts.AccountOrGroup != c.wantAcct {
				t.Errorf("parts=%+v want job=%q artifact=%q platform=%q account=%q",
					parts, c.wantJob, c.wantArtifact, c.wantPlatform, c.wantAcct)
			}
		})
	}
}
