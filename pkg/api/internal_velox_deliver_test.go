package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// testVeloxAPIToken is shared with internal_velox_validate_test.go
// via the same package. The string is printable ASCII for easy
// eyeballing in failure messages.
const testDeliverAPIToken = "test-velox-secret-token-fixed-value"

// validSHA is a deterministic 64-char lowercase hex string for
// use in fixtures. It deliberately has a non-zero prefix (the
// regex enforces [a-f0-9]{64}, so any 64-char lowercase hex
// works) which makes it visually distinguishable in logs.
const validSHA = "e5f2c235e5f2c235e5f2c235e5f2c235e5f2c235e5f2c235e5f2c235e5f2c235"

// -----------------------------------------------------------------------
// Mocks (in-file to keep the test self-contained; mirrors the
// validate test file pattern)
// -----------------------------------------------------------------------

// mockExternalDestinations mirrors the validate-test mock. Single
// method (GetByID) since the deliver handler's only DB read is
// the destination lookup. Each scenario toggles the Result/Err
// fields per test.
type mockExternalDestinations struct {
	GetByIDResult *models.ExternalDestination
	GetByIDErr    error
	GetByIDCalls  int
	CreateErr     error
	CreateCalls   int
}

func (m *mockExternalDestinations) Create(_ context.Context, _ *models.ExternalDestination) error {
	m.CreateCalls++
	return m.CreateErr
}

func (m *mockExternalDestinations) GetByID(_ context.Context, _ string) (*models.ExternalDestination, error) {
	m.GetByIDCalls++
	return m.GetByIDResult, m.GetByIDErr
}

// destinationsAdapter mirrors the validate-test single-embed
// pattern: embeds the production ExternalDestinationStore
// interface (other methods nil-receiver-safe) + carries the mock
// so GetByID routes to the mock via depth-0 override.
type destinationsAdapter struct {
	ExternalDestinationStore
	m *mockExternalDestinations
}

func (a *destinationsAdapter) GetByID(ctx context.Context, id string) (*models.ExternalDestination, error) {
	return a.m.GetByID(ctx, id)
}

func wrapDestinations(m *mockExternalDestinations) ExternalDestinationStore {
	return &destinationsAdapter{m: m}
}

// mockExternalDeliveries is the deliveries-side mock. Insert is
// the only method the deliveries handler calls today.
type mockExternalDeliveries struct {
	InsertResult       *models.ExternalDelivery
	InsertErr          error
	InsertCalls        int
	LastInsertDelivery *models.ExternalDelivery
	LastInsertRawBody  []byte
}

func (m *mockExternalDeliveries) Insert(_ context.Context, e *models.ExternalDelivery, rawBody []byte) (*models.ExternalDelivery, error) {
	m.InsertCalls++
	m.LastInsertDelivery = e
	m.LastInsertRawBody = rawBody
	// Natural "fresh insert" semantic: when the test doesn't
	// pre-set InsertResult, return the input delivery ptr — this
	// simulates "the DB just inserted the row, here it is". The
	// minted-ID comparison in the handler then sees a matching
	// ID and reports already_exists=false. Tests for replay and
	// conflict explicitly set InsertResult / InsertErr to override.
	if m.InsertResult == nil {
		return e, m.InsertErr
	}
	return m.InsertResult, m.InsertErr
}

// GetByID is a stub so mockExternalDeliveries satisfies the
// expanded ExternalDeliveryStore interface (Phase 1 GET
// /internal/v1/deliveries/{id}). POST /deliveries tests dont
// exercise GetByID; the GET handler tests use a different
// fakeDeliveryStorage. (nil, nil) mirrors the production-repo
// semantic of id-never-accepted.
func (m *mockExternalDeliveries) GetByID(_ context.Context, _ string) (*models.ExternalDelivery, error) {
	return nil, nil
}

// deliveriesAdapter: embed ExternalDeliveryStore once + carry
// the mock. Depth-0 override shadows the promoted Insert.
type deliveriesAdapter struct {
	ExternalDeliveryStore
	m *mockExternalDeliveries
}

func (a *deliveriesAdapter) Insert(ctx context.Context, e *models.ExternalDelivery, rawBody []byte) (*models.ExternalDelivery, error) {
	return a.m.Insert(ctx, e, rawBody)
}

// GetByID depth-0 override — required because the adapter embeds
// ExternalDeliveryStore as a nil interface field. Without an
// explicit override, dispatch falls through to the nil embedded
// interface and PANICS at runtime. Depth-0 shadows promotion.
func (a *deliveriesAdapter) GetByID(ctx context.Context, id string) (*models.ExternalDelivery, error) {
	if a.m != nil {
		return a.m.GetByID(ctx, id)
	}
	return nil, nil
}
func wrapDeliveries(m *mockExternalDeliveries) ExternalDeliveryStore {
	return &deliveriesAdapter{m: m}
}

// -----------------------------------------------------------------------
// Router + request fixtures
// -----------------------------------------------------------------------

// buildDeliverRouter wires the two mocks + token onto a fresh
// Router. workspaceStore + userRepo are NOT wired — the deliver
// handler doesn't touch them. Matches the nil-guard pattern in
// the validate handler.
func buildDeliverRouter(dst ExternalDestinationStore, del ExternalDeliveryStore, token string) *Router {
	return &Router{
		externalDestinations: dst,
		externalDeliveries:   del,
		veloxAPIToken:        token,
	}
}

// runDeliver wires an httptest POST to the deliver handler +
// Bearer middleware, returns the recorded response. Uses
// chi.Mux (the production routing library) per the convergence
// with the production handler.
//
// Signature:
//   - dst / del: mock stores (adapter-wrapped before buildDeliverRouter)
//   - token: the veloxAPIToken ("" means "no auth configured")
//   - body: the raw request body bytes (nil → POST with no body)
//   - authHeader: optional Authorization header value ("" → no header)
func runDeliver(t *testing.T, dst ExternalDestinationStore, del ExternalDeliveryStore, token string, body []byte, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	r := buildDeliverRouter(dst, del, token)
	handler := r.internalVeloxAuth(http.HandlerFunc(r.handleCreateInternalDelivery))
	mux := chi.NewRouter()
	mux.Method(http.MethodPost, "/internal/v1/deliveries", handler)

	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(http.MethodPost, "/internal/v1/deliveries",
			strings.NewReader(string(body)))
	} else {
		req = httptest.NewRequest(http.MethodPost, "/internal/v1/deliveries", nil)
	}
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

// fixtureValidBody returns a body that passes every validation
// rule: SHA=validSHA, size>0, mime in allowlist, metadata a
// non-empty JSON object, idempotency_key present, all required
// fields populated. Tests that want to negate a single field
// call fixtureValidBody then string-replace the offending slice.
func fixtureValidBody() []byte {
	return []byte(`{
		"external_delivery_id": "delivery_8cc0f",
		"idempotency_key":      "delivery_8cc0f|dest_12",
		"external_destination_id": "extdst_01JABC",
		"artifact": {
			"artifact_id":  "artifact_01JXYZ",
			"sha256":       "` + validSHA + `",
			"size_bytes":   184729302,
			"mime_type":    "video/mp4",
			"download_url": "https://velox.internal/artifacts/abc"
		},
		"metadata": {
			"title":         "Test Video",
			"description":   "Test description",
			"tags":          ["t1", "t2"],
			"privacy_status":"private",
			"language":      "en"
		},
		"publish_at":   "2026-07-20T18:00:00Z",
		"callback_url": "https://velox.internal/api/internal/cb"
	}`)
}

// fixtureValidDestination returns a non-nil, enabled destination
// reference for the happy-path mocks.
func fixtureValidDestination() *models.ExternalDestination {
	return &models.ExternalDestination{
		ID:                "extdst_01JABC",
		SourceSystem:      "velox",
		WorkspaceID:       12,
		PlatformAccountID: 345,
		Enabled:           true,
	}
}

// -----------------------------------------------------------------------
// Auth tests — Bearer middleware inheritance from validate handler
// -----------------------------------------------------------------------

// TestDeliver_MissingAuth confirms an unauthenticated POST is
// rejected with 401 + JSON envelope. The destination + delivery
// stores must NOT have been queried (auth-fail short-circuit).
func TestDeliver_MissingAuth(t *testing.T) {
	dst := &mockExternalDestinations{}
	del := &mockExternalDeliveries{}
	w := runDeliver(t, dst, del, testDeliverAPIToken, fixtureValidBody(), "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("missing auth: want 401, got %d (body=%q)", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type: want application/json, got %q", got)
	}
	if dst.GetByIDCalls != 0 {
		t.Errorf("destination store must NOT be called when auth fails; got %d calls", dst.GetByIDCalls)
	}
	if del.InsertCalls != 0 {
		t.Errorf("delivery store must NOT be called when auth fails; got %d calls", del.InsertCalls)
	}
}

// TestDeliver_WrongToken confirms a wrong-token POST is rejected
// with 403 (per the Velox-specific 401/403 split we documented in
// internal_auth.go).
func TestDeliver_WrongToken(t *testing.T) {
	dst := &mockExternalDestinations{}
	del := &mockExternalDeliveries{}
	w := runDeliver(t, dst, del, testDeliverAPIToken, fixtureValidBody(),
		"Bearer wrong-token-32-chars-aaaaaa")
	if w.Code != http.StatusForbidden {
		t.Fatalf("wrong token: want 403, got %d", w.Code)
	}
	if del.InsertCalls != 0 {
		t.Errorf("delivery store must NOT be called on token mismatch")
	}
}

// TestDeliver_AuthHeaderOnly applies the prefix-only-edge case:
// baren "Bearer" (no token) returns 401 (malformed), per the
// middleware's len(prefix) check.
func TestDeliver_AuthHeaderOnly(t *testing.T) {
	dst := &mockExternalDestinations{}
	del := &mockExternalDeliveries{}
	w := runDeliver(t, dst, del, testDeliverAPIToken, fixtureValidBody(),
		"Bearer")
	if w.Code != http.StatusUnauthorized {
		t.Errorf("bare prefix: want 401, got %d", w.Code)
	}
}

// -----------------------------------------------------------------------
// Body-level tests — cap, parse errors, empty
// -----------------------------------------------------------------------

// TestDeliver_EmptyBody verifies the empty-body short-circuit
// (400 Bad Request). Distinct from 422 because the body itself
// is malformed (no JSON to validate); 422 fires AFTER parse.
func TestDeliver_EmptyBody(t *testing.T) {
	dst := &mockExternalDestinations{}
	del := &mockExternalDeliveries{}
	w := runDeliver(t, dst, del, testDeliverAPIToken, nil, "Bearer "+testDeliverAPIToken)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty body: want 400, got %d (body=%q)", w.Code, w.Body.String())
	}
}

// TestDeliver_BodyTooLarge verifies the 8 MB cap fires 413.
// We send an 8 MB + 1 byte body using payload strings to keep
// the test fast (no need to read a real file).
func TestDeliver_BodyTooLarge(t *testing.T) {
	dst := &mockExternalDestinations{}
	del := &mockExternalDeliveries{}
	// 9 MB of padding (well above 8 MB cap).
	bigBody := make([]byte, 9*1024*1024)
	for i := range bigBody {
		bigBody[i] = 'a'
	}
	w := runDeliver(t, dst, del, testDeliverAPIToken, bigBody, "Bearer "+testDeliverAPIToken)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("body too large: want 413, got %d (body=%q)", w.Code, w.Body.String())
	}
	if del.InsertCalls != 0 {
		t.Errorf("delivery store must NOT be called when body exceeds cap")
	}
}

// TestDeliver_InvalidJSON verifies a malformed body (here:
// trailing comma + unclosed brace) returns 400. Distinct from
// 422 because we never make it past Unmarshal.
func TestDeliver_InvalidJSON(t *testing.T) {
	dst := &mockExternalDestinations{}
	del := &mockExternalDeliveries{}
	w := runDeliver(t, dst, del, testDeliverAPIToken,
		[]byte(`{"idempotency_key":"abc",`),
		"Bearer "+testDeliverAPIToken)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid JSON: want 400, got %d (body=%q)", w.Code, w.Body.String())
	}
}

// -----------------------------------------------------------------------
// Field-level validation tests — fast-fail chain
// -----------------------------------------------------------------------

// TestDeliver_MissingIdempotencyKey confirms 422 fires before any
// other field validation when idempotency_key is empty.
func TestDeliver_MissingIdempotencyKey(t *testing.T) {
	dst := &mockExternalDestinations{}
	del := &mockExternalDeliveries{}
	body := []byte(`{"idempotency_key":"","external_destination_id":"extdst_01JABC"}`)
	w := runDeliver(t, dst, del, testDeliverAPIToken, body, "Bearer "+testDeliverAPIToken)
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("missing idem key: want 422, got %d (body=%q)", w.Code, w.Body.String())
	}
	if del.InsertCalls != 0 {
		t.Error("delivery Insert should not fire on validation failure")
	}
}

// TestDeliver_IdempotencyKeyTooLong checks the 256-char cap.
func TestDeliver_IdempotencyKeyTooLong(t *testing.T) {
	dst := &mockExternalDestinations{}
	del := &mockExternalDeliveries{}
	longKey := strings.Repeat("k", 257)
	body := []byte(`{"idempotency_key":"` + longKey + `","external_destination_id":"extdst_01JABC"}`)
	w := runDeliver(t, dst, del, testDeliverAPIToken, body, "Bearer "+testDeliverAPIToken)
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("long idem key: want 422, got %d", w.Code)
	}
}

// TestDeliver_InvalidSHA pins the sha256 regex. Variants:
//   - short length
//   - uppercase hex
//   - non-hex characters
//
// All three return 422.
func TestDeliver_InvalidSHA(t *testing.T) {
	dst := &mockExternalDestinations{}
	del := &mockExternalDeliveries{}
	cases := []struct {
		name string
		sha  string
	}{
		{"short length", strings.Repeat("a", 63) + "b"},
		{"uppercase hex", strings.ToUpper(validSHA)},
		{"non-hex has z", strings.Repeat("z", 64)},
		{"contains spaces", strings.Repeat("a", 60) + "    "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Replace(string(fixtureValidBody()), validSHA, tc.sha, 1)
			w := runDeliver(t, dst, del, testDeliverAPIToken,
				[]byte(body), "Bearer "+testDeliverAPIToken)
			if w.Code != http.StatusUnprocessableEntity {
				t.Errorf("invalid sha %q: want 422, got %d", tc.sha, w.Code)
			}
		})
	}
}

// TestDeliver_ZeroSize pins the size-positive rule.
func TestDeliver_ZeroSize(t *testing.T) {
	dst := &mockExternalDestinations{}
	del := &mockExternalDeliveries{}
	body := strings.Replace(string(fixtureValidBody()), `184729302`, `0`, 1)
	w := runDeliver(t, dst, del, testDeliverAPIToken,
		[]byte(body), "Bearer "+testDeliverAPIToken)
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("zero size: want 422, got %d (body=%q)", w.Code, w.Body.String())
	}
}

// TestDeliver_NegativeSize pins the size-positive rule for negatives.
func TestDeliver_NegativeSize(t *testing.T) {
	dst := &mockExternalDestinations{}
	del := &mockExternalDeliveries{}
	body := strings.Replace(string(fixtureValidBody()), `184729302`, `-1`, 1)
	w := runDeliver(t, dst, del, testDeliverAPIToken,
		[]byte(body), "Bearer "+testDeliverAPIToken)
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("negative size: want 422, got %d (body=%q)", w.Code, w.Body.String())
	}
}

// TestDeliver_UnsupportedMime pins the mime allowlist. Variants
// include PNG (image), AVI (a video but not in the Phase 1
// allowlist — flagged for follow-up), and a totally bogus type.
func TestDeliver_UnsupportedMime(t *testing.T) {
	dst := &mockExternalDestinations{}
	del := &mockExternalDeliveries{}
	cases := []struct {
		name string
		mime string
	}{
		{"image/jpeg", `image/jpeg`},
		{"video/x-msvideo", `video/x-msvideo`},
		{"audio/mpeg", `audio/mpeg`},
		{"garbage", `not-a-mime`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Replace(string(fixtureValidBody()),
				`"video/mp4"`, `"`+tc.mime+`"`, 1)
			w := runDeliver(t, dst, del, testDeliverAPIToken,
				[]byte(body), "Bearer "+testDeliverAPIToken)
			if w.Code != http.StatusUnprocessableEntity {
				t.Errorf("mime %q: want 422, got %d (body=%q)",
					tc.mime, w.Code, w.Body.String())
			}
		})
	}
}

// TestDeliver_EmptyMetadata pins the non-empty JSON object rule.
// Variants: literal {}, null, array, non-object.
func TestDeliver_EmptyMetadata(t *testing.T) {
	dst := &mockExternalDestinations{}
	del := &mockExternalDeliveries{}
	cases := []struct {
		name     string
		metadata string
	}{
		{"empty object", `{}`},
		{"null", `null`},
		{"array", `["no","good"]`},
		{"string", `"some string"`},
		{"number", `42`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Replace the metadata object with the invalid candidate.
			// The fixture contains `"metadata": { … }` — replace from
			// the start of `"metadata":` through the matching closing
			// `}`. Since metadata has no nested objects, the FIRST `}`
			// after `"metadata":` is the canonical close. Skip past
			// `}` (idx+closeIdx+1) so the trailing comma after `}`
			// in the original body is preserved.
			full := string(fixtureValidBody())
			idx := strings.Index(full, `"metadata":`)
			closeIdx := strings.Index(full[idx:], `}`)
			body := full[:idx] + `"metadata": ` + tc.metadata + full[idx+closeIdx+1:]
			w := runDeliver(t, dst, del, testDeliverAPIToken,
				[]byte(body), "Bearer "+testDeliverAPIToken)
			if w.Code != http.StatusUnprocessableEntity {
				t.Errorf("metadata %s: want 422, got %d (body=%q)",
					tc.name, w.Code, w.Body.String())
			}
		})
	}
}

// TestDeliver_UnknownDestination pins the destination-lookup
// gate. The mock returns nil dest → handler returns 422 with the
// "not found" message so the caller can pattern-match the
// destination id in the error.
func TestDeliver_UnknownDestination(t *testing.T) {
	dst := &mockExternalDestinations{GetByIDResult: nil}
	del := &mockExternalDeliveries{}
	w := runDeliver(t, dst, del, testDeliverAPIToken, fixtureValidBody(),
		"Bearer "+testDeliverAPIToken)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown destination: want 422, got %d (body=%q)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "extdst_01JABC") {
		t.Errorf("422 body should mention the bad destination id; got %q",
			w.Body.String())
	}
	if del.InsertCalls != 0 {
		t.Error("delivery Insert should not fire when destination is invalid")
	}
}

// -----------------------------------------------------------------------
// Three-way outcome tests — happy paths + conflict + internal error
// -----------------------------------------------------------------------

// TestDeliver_HappyFreshInsert verifies the FIRST-TIME success
// path: mock Insert returns the SAME ID the handler minted,
// handler returns 202 with already_exists=false. The minted
// ID MUST start with "sdel_01J" (the ULID-shaped prefix).
func TestDeliver_HappyFreshInsert(t *testing.T) {
	dst := &mockExternalDestinations{GetByIDResult: fixtureValidDestination()}
	del := &mockExternalDeliveries{
		InsertResult: nil, // populated below after capturing minted ID
	}
	body := fixtureValidBody()
	w := runDeliver(t, dst, del, testDeliverAPIToken, body, "Bearer "+testDeliverAPIToken)

	if w.Code != http.StatusAccepted {
		t.Fatalf("fresh insert: want 202, got %d (body=%q)", w.Code, w.Body.String())
	}
	if del.InsertCalls != 1 {
		t.Fatalf("Insert call count: want 1, got %d", del.InsertCalls)
	}
	// Verify Insert received the EXACT raw bytes (so repo SHA
	// computation matches the body hashed in the handler chain).
	if string(del.LastInsertRawBody) != string(body) {
		t.Errorf("LastInsertRawBody mismatch with sent body")
	}
	// Verify the minted ID prefix.
	mintedID := del.LastInsertDelivery.ID
	if !strings.HasPrefix(mintedID, "sdel_01J") {
		t.Errorf("minted ID should start with sdel_01J, got %q", mintedID)
	}
	// Verify the response shape.
	var resp VeloxDeliverArtifactResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body=%q)", err, w.Body.String())
	}
	if resp.SocialDeliveryID != mintedID {
		t.Errorf("SocialDeliveryID: want %q, got %q", mintedID, resp.SocialDeliveryID)
	}
	if resp.Status != "accepted" {
		t.Errorf("Status: want accepted, got %q", resp.Status)
	}
	if resp.AlreadyExists {
		t.Errorf("AlreadyExists: want false (fresh insert), got true")
	}
}

// TestDeliver_HappyReplayInsert verifies the SAME-SHA replay path:
// mock Insert returns a DIFFERENT ID (the existing row) with the
// same SHA. Handler detects the mismatch via minted != returned
// and sets already_exists=true.
func TestDeliver_HappyReplayInsert(t *testing.T) {
	dst := &mockExternalDestinations{GetByIDResult: fixtureValidDestination()}
	preExisting := &models.ExternalDelivery{
		ID:             "sdel_01JEXISTING01JEXISTING01JEXIST", // different from minted
		SourceSystem:   "velox",
		IdempotencyKey: "delivery_8cc0f|dest_12",
		Status:         models.ExternalDeliveryStatusAccepted,
	}
	del := &mockExternalDeliveries{InsertResult: preExisting}
	w := runDeliver(t, dst, del, testDeliverAPIToken, fixtureValidBody(),
		"Bearer "+testDeliverAPIToken)
	if w.Code != http.StatusAccepted {
		t.Fatalf("replay: want 202, got %d", w.Code)
	}
	var resp VeloxDeliverArtifactResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body=%q)", err, w.Body.String())
	}
	if resp.SocialDeliveryID != preExisting.ID {
		t.Errorf("SocialDeliveryID: want pre-existing %q, got %q",
			preExisting.ID, resp.SocialDeliveryID)
	}
	if !resp.AlreadyExists {
		t.Errorf("AlreadyExists: want true (replay), got false")
	}
}

// TestDeliver_IdempotencyConflict pins the 409 structured-body
// path. Mock returns ErrIdempotencyConflict + nil record (the
// repo contract for conflicts). Handler returns 409 with the
// VeloxDeliverArtifactConflictResponse shape (error/code/
// idempotency_key, NOT writeError).
func TestDeliver_IdempotencyConflict(t *testing.T) {
	dst := &mockExternalDestinations{GetByIDResult: fixtureValidDestination()}
	del := &mockExternalDeliveries{
		InsertResult: &models.ExternalDelivery{
			ID:             "sdel_01JCONFLICT000000000000000000",
			IdempotencyKey: "delivery_8cc0f|dest_12",
			SourceSystem:   "velox",
			Status:         models.ExternalDeliveryStatusAccepted,
		},
		InsertErr: repository.ErrIdempotencyConflict, // the repo sentinel
	}
	w := runDeliver(t, dst, del, testDeliverAPIToken, fixtureValidBody(),
		"Bearer "+testDeliverAPIToken)
	if w.Code != http.StatusConflict {
		t.Fatalf("conflict: want 409, got %d (body=%q)", w.Code, w.Body.String())
	}
	// Verify the structured body — NOT the writeError envelope.
	var resp VeloxDeliverArtifactConflictResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode 409 body: %v (body=%q)", err, w.Body.String())
	}
	if resp.Code != "idempotency_key_conflict" {
		t.Errorf("Code: want idempotency_key_conflict, got %q", resp.Code)
	}
	if resp.Error != "idempotency_key_conflict" {
		t.Errorf("Error field: want idempotency_key_conflict, got %q", resp.Error)
	}
	if resp.IdempotencyKey != "delivery_8cc0f|dest_12" {
		t.Errorf("IdempotencyKey: want delivery_8cc0f|dest_12, got %q",
			resp.IdempotencyKey)
	}
}

// TestDeliver_500InternalError pins the unexpected-error path.
// Any non-conflict error from Insert returns 500.
func TestDeliver_500InternalError(t *testing.T) {
	dst := &mockExternalDestinations{GetByIDResult: fixtureValidDestination()}
	del := &mockExternalDeliveries{
		InsertErr: context.DeadlineExceeded, // arbitrary non-conflict error
	}
	w := runDeliver(t, dst, del, testDeliverAPIToken, fixtureValidBody(),
		"Bearer "+testDeliverAPIToken)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("internal error: want 500, got %d (body=%q)", w.Code, w.Body.String())
	}
}

// TestDeliver_NilStore wires the nil-guard path: the Router
// struct without WithExternalDeliveryStore must return 501
// from the handler's defensive check, NOT 500.
func TestDeliver_NilStore(t *testing.T) {
	// Build a router with NO externalDeliveries wired.
	r := &Router{
		externalDestinations: &mockExternalDestinations{GetByIDResult: fixtureValidDestination()},
		externalDeliveries:   nil,
		veloxAPIToken:        testDeliverAPIToken,
	}
	handler := r.internalVeloxAuth(http.HandlerFunc(r.handleCreateInternalDelivery))
	mux := chi.NewRouter()
	mux.Method(http.MethodPost, "/internal/v1/deliveries", handler)
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/deliveries",
		strings.NewReader(string(fixtureValidBody())))
	req.Header.Set("Authorization", "Bearer "+testDeliverAPIToken)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotImplemented {
		t.Errorf("nil store guard: want 501, got %d (body=%q)",
			w.Code, w.Body.String())
	}
}

// TestDeliver_MintIDPrefix confirms the ULID-shaped mint produces
// an ID starting with sdel_01J and of reasonable length (the
// 5-char prefix + 3-char legacy + 26-char base32 = 34 chars).
func TestDeliver_MintIDPrefix(t *testing.T) {
	id, err := services.GenerateVeloxDeliveryID()
	if err != nil {
		t.Fatalf("generateVeloxDeliveryID: %v", err)
	}
	if !strings.HasPrefix(id, "sdel_01J") {
		t.Errorf("id prefix: want sdel_01J, got %q", id)
	}
	if len(id) < 30 {
		t.Errorf("id length: want >= 30 chars, got %d (%q)", len(id), id)
	}
	// Uniqueness sanity — mint twice, verify distinct.
	id2, err := services.GenerateVeloxDeliveryID()
	if err != nil {
		t.Fatalf("second mint: %v", err)
	}
	if id == id2 {
		t.Errorf("two mints collided: get %q == %q", id, id2)
	}
}
