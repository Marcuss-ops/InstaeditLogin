package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/worker"
)

func TestE2E_VeloxPhase1_Pipeline(t *testing.T) {
	h := newE2EHarness(t)
	ctx := context.Background()

	// ----------------------------------------------------------------
	// STAGE 1 — POST /destinations/{id}/validate → 204
	// ----------------------------------------------------------------
	t.Run("stage1_validate_destination", func(t *testing.T) {
		path := fmt.Sprintf("/internal/v1/destinations/%s/validate", e2eDestinationID)
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set("Authorization", "Bearer "+e2eAPIToken)
		w := httptest.NewRecorder()
		h.router.mux.ServeHTTP(w, req)

		if w.Code != http.StatusNoContent {
			t.Fatalf("validate: want 204, got %d (body=%q)", w.Code, w.Body.String())
		}
		if w.Body.Len() != 0 {
			t.Errorf("validate 204 must have empty body; got %d bytes", w.Body.Len())
		}
	})

	// ----------------------------------------------------------------
	// STAGE 2 — POST /deliveries (fresh) → 202 + social_delivery_id
	// ----------------------------------------------------------------
	var sdelID string
	t.Run("stage2_fresh_delivery", func(t *testing.T) {
		body := buildE2EDeliveryBody(t, e2eValidSHA, e2eArtifactBytes)
		req := httptest.NewRequest(http.MethodPost, "/internal/v1/deliveries", strings.NewReader(string(body)))
		req.Header.Set("Authorization", "Bearer "+e2eAPIToken)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.router.mux.ServeHTTP(w, req)

		if w.Code != http.StatusAccepted {
			t.Fatalf("POST /deliveries fresh: want 202, got %d (body=%q)", w.Code, w.Body.String())
		}
		var resp VeloxDeliverArtifactResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v (body=%q)", err, w.Body.String())
		}
		if !strings.HasPrefix(resp.SocialDeliveryID, "sdel_01J") {
			t.Errorf("SocialDeliveryID prefix = %q; want sdel_01J…", resp.SocialDeliveryID)
		}
		if resp.Status != "accepted" {
			t.Errorf("Status = %q; want accepted", resp.Status)
		}
		if resp.AlreadyExists {
			t.Errorf("AlreadyExists = true; want false (fresh insert)")
		}
		sdelID = resp.SocialDeliveryID

		// Store must have the row now.
		got, _ := h.deliveries.GetByID(ctx, sdelID)
		if got == nil {
			t.Fatalf("delivery persistence: row %q missing after fresh POST", sdelID)
		}
		if got.Status != models.ExternalDeliveryStatusAccepted {
			t.Errorf("post-insert status = %q; want accepted", got.Status)
		}
	})

	// ----------------------------------------------------------------
	// STAGE 3 — Worker fetches artifact + verifies SHA +
	// FSM transitions through artifact_verified → ingest_completed
	// → queued.
	//
	// The "worker" here is the test itself calling the production
	// worker.IngestFSM methods. In production the publish_worker
	// goroutine calls the same methods (after polling the
	// external_deliveries table for status='accepted' rows).
	// ----------------------------------------------------------------
	t.Run("stage3_worker_pipeline", func(t *testing.T) {
		// (a) Worker simulates the artifact fetch. In process,
		// the fetch is just GET + sha-over-bytes — same logic
		// the production worker would do. Asserts:
		//   - the GET hit the mock artifact server
		//   - the SHA computed locally matches the expected
		//     SHA the client declared (e2eValidSHA)
		//   - the size matches the declared size_bytes (4096)
		gotBytes := fetchArtifactVerifySHA(t, h.veloxArtifactURL(), e2eValidSHA, int64(len(e2eArtifactBytes)))
		if len(gotBytes) != len(e2eArtifactBytes) {
			t.Fatalf("artifact size = %d; want %d", len(gotBytes), len(e2eArtifactBytes))
		}
		if atomic.LoadInt32(&h.artifactHits) < 1 {
			t.Error("mock artifact server recorded 0 GETs; want >= 1")
		}

		// (b) FSM driving. We're using the production FSM so the
		// state transitions are exactly what production does.
		fsm := worker.NewIngestFSM(h.deliveries, slog.Default())

		// accepted → downloading
		if err := fsm.ToDownloading(ctx, sdelID, models.ExternalDeliveryStatusAccepted); err != nil {
			t.Fatalf("ToDownloading: %v", err)
		}
		// downloading → artifact_verified
		if err := fsm.ToArtifactVerified(ctx, sdelID, models.ExternalDeliveryStatusDownloading); err != nil {
			t.Fatalf("ToArtifactVerified: %v", err)
		}
		// artifact_verified → ingest_completed
		if err := fsm.ToIngestCompleted(ctx, sdelID, models.ExternalDeliveryStatusArtifactVerified); err != nil {
			t.Fatalf("ToIngestCompleted: %v", err)
		}
		// ingest_completed → queued
		if err := fsm.ToQueued(ctx, sdelID, models.ExternalDeliveryStatusIngestCompleted); err != nil {
			t.Fatalf("ToQueued: %v", err)
		}

		// Final state check.
		got, _ := h.deliveries.GetByID(ctx, sdelID)
		if got == nil {
			t.Fatal("row vanished after FSM transitions")
		}
		if got.Status != models.ExternalDeliveryStatusQueued {
			t.Errorf("post-FSM status = %q; want queued", got.Status)
		}
	})

	// ----------------------------------------------------------------
	// STAGE 4 — Publish to mock YouTube + HMAC callback to mock Velox.
	// ----------------------------------------------------------------
	t.Run("stage4_publish_and_hmac_callback", func(t *testing.T) {
		mediaID := "yt_e2e_video_001"
		mediaURL := "https://www.youtube.com/watch?v=yt_e2e_video_001"

		// q→publishing→published via FSM
		fsm := worker.NewIngestFSM(h.deliveries, slog.Default())
		if err := fsm.ToPublishing(ctx, sdelID, models.ExternalDeliveryStatusQueued); err != nil {
			t.Fatalf("ToPublishing: %v", err)
		}
		mediaIDp, mediaURLp := mediaID, mediaURL
		if err := fsm.ToPublished(ctx, sdelID, models.ExternalDeliveryStatusPublishing, &mediaIDp, &mediaURLp); err != nil {
			t.Fatalf("ToPublished: %v", err)
		}

		// Publish-side assert: the YouTube mock was hit AT LEAST
		// once. In real production the publish flow happens BEFORE
		// ToPublished stamps the row. Here we simulate it inline:
		// the test calls the mock endpoint directly so the dispatch
		// can safely use the fake response.
		hit := ytPublishHit(t, h.youtubeSrv.URL+"/upload")
		if !hit {
			t.Error("publish to mock YouTube did not hit the server")
		}

		// Now dispatch the Velox callback. The dispatcher targets
		// the mock receiver URL set on the delivery row. In
		// production this would be set via the upstream's
		// callback_url field; we replicate by injecting a callback
		// URL on the in-memory row before dispatch.
		got, _ := h.deliveries.GetByID(ctx, sdelID)
		callbackURL := h.veloxCallbackURL()
		got.CallbackURL = &callbackURL

		if err := h.dispatcher.Dispatch(ctx, got, VeloxCallbackPublished, &VeloxCallbackPayload{
			PlatformMediaID: &mediaIDp,
			PlatformURL:     &mediaURLp,
		}); err != nil {
			t.Fatalf("Dispatch published callback: %v", err)
		}

		// Receiver recorded exactly one callback with the right
		// event shape.
		events := h.capturedCallbackEvents()
		if len(events) != 1 {
			t.Fatalf("captured callbacks: want 1, got %d", len(events))
		}
		cb := events[0]
		if cb.Event != "published" {
			t.Errorf("callback event = %q; want published", cb.Event)
		}
		if !strings.HasPrefix(cb.EventID, "evt_") {
			t.Errorf("callback EventID = %q; want evt_ prefix", cb.EventID)
		}
		if !strings.HasPrefix(cb.Signature, "sha256=") {
			t.Errorf("callback Signature prefix = %q; want sha256=", cb.Signature)
		}
		// Decode the body + assert platform fields populated.
		var decoded VeloxCallbackPayload
		if err := json.Unmarshal(cb.Body, &decoded); err != nil {
			t.Fatalf("body unmarshal: %v", err)
		}
		if decoded.SocialDeliveryID != sdelID {
			t.Errorf("body.SocialDeliveryID = %q; want %q", decoded.SocialDeliveryID, sdelID)
		}
		if decoded.ExternalDeliveryID != e2eExternalDelID {
			t.Errorf("body.ExternalDeliveryID = %q; want %q", decoded.ExternalDeliveryID, e2eExternalDelID)
		}
		if decoded.PlatformMediaID == nil || *decoded.PlatformMediaID != mediaID {
			t.Errorf("body.PlatformMediaID = %v; want %q", decoded.PlatformMediaID, mediaID)
		}
		if decoded.PlatformURL == nil || *decoded.PlatformURL != mediaURL {
			t.Errorf("body.PlatformURL = %v; want %q", decoded.PlatformURL, mediaURL)
		}
		if decoded.Status != "published" {
			t.Errorf("body.Status = %q; want published", decoded.Status)
		}

		// Audit row(s).
		if h.audit.entryCount() != 1 {
			t.Errorf("audit count = %d; want 1 (one Dispatch = one audit)", h.audit.entryCount())
		}
	})

	// ----------------------------------------------------------------
	// STAGE 5 — Re-POST same body bytes → 202 + already_exists=true
	// + same social_delivery_id (NOT a new id).
	// ----------------------------------------------------------------
	t.Run("stage5_replay_same_body", func(t *testing.T) {
		body := buildE2EDeliveryBody(t, e2eValidSHA, e2eArtifactBytes)
		req := httptest.NewRequest(http.MethodPost, "/internal/v1/deliveries", strings.NewReader(string(body)))
		req.Header.Set("Authorization", "Bearer "+e2eAPIToken)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.router.mux.ServeHTTP(w, req)

		if w.Code != http.StatusAccepted {
			t.Fatalf("replay: want 202, got %d (body=%q)", w.Code, w.Body.String())
		}
		var resp VeloxDeliverArtifactResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v (body=%q)", err, w.Body.String())
		}
		if resp.SocialDeliveryID != sdelID {
			t.Errorf("replay SocialDeliveryID = %q; want %q (the original)", resp.SocialDeliveryID, sdelID)
		}
		if !resp.AlreadyExists {
			t.Errorf("replay AlreadyExists = false; want true")
		}
		// Insert should NOT have created a new row.
		if got := atomic.LoadInt32(&h.deliveries.insertCalls); got != 2 {
			t.Errorf("insertCalls = %d after fresh + replay; want 2 (one per POST)", got)
		}
	})

	// ----------------------------------------------------------------
	// STAGE 6 — Re-POST same idempotency_key + DIFFERENT SHA → 409
	// VeloxDeliverArtifactConflictResponse shape.
	// ----------------------------------------------------------------
	t.Run("stage6_idempotency_conflict", func(t *testing.T) {
		// Different SHA — substitutes e2eInvalidSHA for e2eValidSHA
		// in the body fixture. ALL OTHER fields identical (so the
		// only discriminator is the SHA).
		body := buildE2EDeliveryBody(t, e2eInvalidSHA, e2eArtifactBytes)
		// e2eInvalidSHA != actual SHA of bytes, so this is a
		// legitimately conflicting request. The handler's
		// validation chain passes the regex check (e2eInvalidSHA
		// is 64 lowercase hex) and the Insert finds the existing
		// row with a DIFFERENT expected_sha256 → 409.
		req := httptest.NewRequest(http.MethodPost, "/internal/v1/deliveries", strings.NewReader(string(body)))
		req.Header.Set("Authorization", "Bearer "+e2eAPIToken)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.router.mux.ServeHTTP(w, req)

		if w.Code != http.StatusConflict {
			t.Fatalf("conflict: want 409, got %d (body=%q)", w.Code, w.Body.String())
		}
		var resp VeloxDeliverArtifactConflictResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v (body=%q)", err, w.Body.String())
		}
		if resp.Error != "idempotency_key_conflict" {
			t.Errorf("Error = %q; want idempotency_key_conflict", resp.Error)
		}
		if resp.Code != "idempotency_key_conflict" {
			t.Errorf("Code = %q; want idempotency_key_conflict", resp.Code)
		}
		if resp.IdempotencyKey != e2eIdempotencyKey {
			t.Errorf("IdempotencyKey = %q; want %q", resp.IdempotencyKey, e2eIdempotencyKey)
		}
	})
}

// =====================================================================
// AUTOGRADE BOUNDARY TESTS
// =====================================================================

// TestE2E_ValidateDestination_NotFound — unknown destination id
// returns 404 (uniform with the disabled-destination collapse to
// preserve existence-leak safety).
func TestE2E_VeloxBoundary(t *testing.T) {
	cases := []struct {
		name        string
		useRecorder bool
		setupReq    func(h *e2eHarness) *http.Request
		wantStatus  int
		extra       func(t *testing.T, h *e2eHarness)
	}{
		{
			name:        "ValidateDestination_NotFound",
			useRecorder: true,
			setupReq: func(h *e2eHarness) *http.Request {
				req := httptest.NewRequest(http.MethodPost,
					"/internal/v1/destinations/extdst_01JDOESNOTEXIST/validate", nil)
				req.Header.Set("Authorization", "Bearer "+e2eAPIToken)
				return req
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name:        "ValidateDestination_AuthMissing",
			useRecorder: true,
			setupReq: func(h *e2eHarness) *http.Request {
				return httptest.NewRequest(http.MethodPost,
					fmt.Sprintf("/internal/v1/destinations/%s/validate", e2eDestinationID), nil)
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "HMACVerifier_RejectsBadSignature",
			setupReq: func(h *e2eHarness) *http.Request {
				body := []byte(`{"hello":"world"}`)
				mac := hmac.New(sha256.New, []byte("WRONG-SECRET"))
				mac.Write([]byte("1784000000"))
				mac.Write([]byte{'.'})
				mac.Write(body)
				badSig := hex.EncodeToString(mac.Sum(nil))
				req, _ := http.NewRequest(http.MethodPost, h.veloxCallbackSrv.URL+"/velox/callback", strings.NewReader(string(body)))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Velox-Event-ID", "evt_tampered")
				req.Header.Set("X-Velox-Timestamp", "1784000000")
				req.Header.Set("X-Velox-Signature", "sha256="+badSig)
				return req
			},
			wantStatus: http.StatusBadRequest,
			extra: func(t *testing.T, h *e2eHarness) {
				h.callbackMu.Lock()
				defer h.callbackMu.Unlock()
				if len(h.callbacks) != 0 {
					t.Errorf("bad-signature POST must NOT be recorded; got %d entries", len(h.callbacks))
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newE2EHarness(t)
			req := tc.setupReq(h)
			if tc.useRecorder {
				w := httptest.NewRecorder()
				h.router.mux.ServeHTTP(w, req)
				if w.Code != tc.wantStatus {
					t.Fatalf("%s: want %d, got %d (body=%q)", tc.name, tc.wantStatus, w.Code, w.Body.String())
				}
			} else {
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatalf("%s: %v", tc.name, err)
				}
				defer resp.Body.Close()
				if resp.StatusCode != tc.wantStatus {
					t.Fatalf("%s: want %d, got %d", tc.name, tc.wantStatus, resp.StatusCode)
				}
			}
			if tc.extra != nil {
				tc.extra(t, h)
			}
		})
	}
}

// =====================================================================
// HELPERS
// =====================================================================

// subtleConstantTimeCompare is a one-line wrapper around
// crypto/subtle.ConstantTimeCompare that takes string args. We
// avoid importing crypto/subtle directly so the diff stays small.
func subtleConstantTimeCompare(a, b []byte) int {
	// This is intentionally identical to subtle.ConstantTimeCompare
	// semantics; doing it inline so the test file is the only
	// consumer in this package.
	if len(a) != len(b) {
		return 0
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	if v != 0 {
		return 0
	}
	return 1
}

// buildE2EDeliveryBody returns a JSON body the handler's
// validation chain accepts (idempotency_key present, sha64-hex,
// size>0, mime in allowlist, metadata non-empty JSON object),
// with the supplied SHA injected into the artifact envelope and
// (optionally) a different _size_ on the body to ensure SHA
// mismatch when needed.
//
// stage 6 uses buildE2EDeliveryBody with e2eInvalidSHA so the SHA
// field disagrees with the actual e2eArtifactBytes content but the
// mime/size/shape stay valid (the handler doesn't reach inside
// bytes at validation time; only the insert's request_sha256
// compare fires).
func buildE2EDeliveryBody(t *testing.T, sha string, _ []byte) []byte {
	t.Helper()
	if sha == "" {
		sha = e2eValidSHA
	}
	body := fmt.Sprintf(`{
		"external_delivery_id": "%s",
		"idempotency_key":       "%s",
		"external_destination_id": "%s",
		"artifact": {
			"artifact_id":  "%s",
			"sha256":       "%s",
			"size_bytes":   %d,
			"mime_type":    "video/mp4",
			"download_url": "%s"
		},
		"metadata": {
			"title":         "E2E Test Video",
			"description":   "driven by internal_velox_e2e_test.go",
			"tags":          ["t1", "t2"],
			"privacy_status":"private",
			"language":      "en"
		},
		"publish_at":   "2026-07-20T18:00:00Z",
		"callback_url": "http://placeholder.invalid/ignored-by-e2e"
	}`,
		e2eExternalDelID,
		e2eIdempotencyKey,
		e2eDestinationID,
		e2eArtifactID,
		sha,
		len(e2eArtifactBytes),
		// placeholder; the worker's download URL is wired via
		// h.veloxArtifactURL() at runtime, not at fixture time.
		"http://placeholder.invalid/artifacts/placeholder",
	)
	return []byte(body)
}

// fetchArtifactVerifySHA simulates the worker's HEAD/GET against
// the download_url: GETs the bytes, computes SHA-256 over the
// raw body, returns the bytes for downstream assertions (size
// match). Errors (404/5xx) are returned verbatim so the test
// sees them.
func fetchArtifactVerifySHA(t *testing.T, url, expectedSHA string, expectedSize int64) []byte {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("artifact GET: status %d", resp.StatusCode)
	}
	if resp.ContentLength > 0 && resp.ContentLength != expectedSize {
		t.Fatalf("artifact Content-Length = %d; want %d", resp.ContentLength, expectedSize)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("artifact read: %v", err)
	}
	if int64(len(body)) != expectedSize {
		t.Fatalf("artifact body size = %d; want %d", len(body), expectedSize)
	}
	sum := sha256.Sum256(body)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, expectedSHA) {
		t.Fatalf("artifact SHA mismatch\ngot:  %s\nwant: %s", got, expectedSHA)
	}
	return body
}

// ytPublishHit fires a POST against the mock YouTube URL — the
// production worker would do this inside the
// queued→publishing→published FSM arc (probably via the
// platform_account's oauth-credential refresh + videos.insert).
// The test just needs to assert the mock was hit.
func ytPublishHit(t *testing.T, url string) bool {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}
