package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// testVeloxSecret is the deterministic secret used by every
// test below — keeps the HMAC pre-computation stable across
// runs so signature reconciliation is trivial.
const testVeloxSecret = "test-velox-webhook-secret-do-not-use-in-prod"

// mockAuditStore is an in-process audit-log fake. Records
// every Append for assertion; appendErr lets tests simulate
// the underlying store going sideways (rare-but-real in
// production).
type mockAuditStore struct {
	mu        sync.Mutex
	entries   []*models.AuditLog
	appendErr error
}

func (m *mockAuditStore) Append(_ context.Context, e *models.AuditLog) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.appendErr != nil {
		return m.appendErr
	}
	m.entries = append(m.entries, e)
	return nil
}

func (m *mockAuditStore) lastEntry(t *testing.T) *models.AuditLog {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.entries) == 0 {
		t.Fatal("no audit entries recorded")
	}
	return m.entries[len(m.entries)-1]
}

func (m *mockAuditStore) entryCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries)
}

// newTestDispatcher constructs a dispatcher with fast
// retry timings + deterministic rand so test wall-clock
// stays under 10s across the suite.
func newTestDispatcher(t *testing.T, _ []byte, httpClient *http.Client, audit VeloxCallbackAuditStore, fastClock func() time.Time) *VeloxCallbackDispatcher {
	t.Helper()
	d := NewVeloxCallbackDispatcher([]byte(testVeloxSecret), httpClient, audit, slog.Default())
	if fastClock != nil {
		d.clock = fastClock
	}
	d.randSrc = rand.New(rand.NewSource(42))
	d.baseDelay = 1 * time.Millisecond
	d.jitterMin = 0
	d.jitterMax = 1 * time.Millisecond
	return d
}

func ptrString(s string) *string { return &s }

// 1. Happy path: 200 OK → single POST, success audit, correct
// headers + body + signature.
func TestVeloxCallback_HappyPath(t *testing.T) {
	var receivedBody []byte
	var receivedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header.Clone()
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	audit := &mockAuditStore{}
	d := newTestDispatcher(t, nil, &http.Client{Timeout: 5 * time.Second}, audit, nil)
	d.idGen = func() string { return "evt_test_happy" }

	callbackURL := server.URL + "/velox/callback"
	delivery := &models.ExternalDelivery{
		ID:                 "sdel_01JABC",
		ExternalDeliveryID: "delivery_8cc0f",
		CallbackURL:        &callbackURL,
	}
	payload := &VeloxCallbackPayload{
		PlatformMediaID: ptrString("dQw4w9WgXcQ"),
		PlatformURL:     ptrString("https://www.youtube.com/watch?v=dQw4w9WgXcQ"),
	}

	if err := d.Dispatch(context.Background(), delivery, VeloxCallbackPublished, payload); err != nil {
		t.Fatalf("expected nil err; got %v", err)
	}

	if got := receivedHeaders.Get("X-Velox-Event-ID"); got != "evt_test_happy" {
		t.Errorf("X-Velox-Event-ID = %q; want evt_test_happy", got)
	}
	if got := receivedHeaders.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q; want application/json", got)
	}
	tsStr := receivedHeaders.Get("X-Velox-Timestamp")
	if tsStr == "" {
		t.Fatal("X-Velox-Timestamp missing")
	}
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		t.Fatalf("X-Velox-Timestamp parse: %v", err)
	}
	sigHeader := receivedHeaders.Get("X-Velox-Signature")
	if !strings.HasPrefix(sigHeader, "sha256=") {
		t.Errorf("X-Velox-Signature prefix = %q; want sha256=", sigHeader)
	}

	mac := hmac.New(sha256.New, []byte(testVeloxSecret))
	mac.Write([]byte(strconv.FormatInt(ts, 10)))
	mac.Write([]byte{'.'})
	mac.Write(receivedBody)
	expectedSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if sigHeader != expectedSig {
		t.Errorf("signature mismatch\n  got:  %q\n  want: %q", sigHeader, expectedSig)
	}

	var decoded VeloxCallbackPayload
	if err := json.Unmarshal(receivedBody, &decoded); err != nil {
		t.Fatalf("body unmarshal: %v", err)
	}
	if decoded.EventID != "evt_test_happy" {
		t.Errorf("body.EventID = %q; want evt_test_happy", decoded.EventID)
	}
	if decoded.SocialDeliveryID != "sdel_01JABC" {
		t.Errorf("body.SocialDeliveryID = %q; want sdel_01JABC", decoded.SocialDeliveryID)
	}
	if decoded.ExternalDeliveryID != "delivery_8cc0f" {
		t.Errorf("body.ExternalDeliveryID = %q; want delivery_8cc0f", decoded.ExternalDeliveryID)
	}
	if decoded.Status != "published" {
		t.Errorf("body.Status = %q; want published", decoded.Status)
	}
	if decoded.PlatformMediaID == nil || *decoded.PlatformMediaID != "dQw4w9WgXcQ" {
		t.Errorf("body.PlatformMediaID = %v; want dQw4w9WgXcQ", decoded.PlatformMediaID)
	}

	e := audit.lastEntry(t)
	if e.Action != models.AuditActionVeloxCallbackSent {
		t.Errorf("audit.Action = %q; want %q", e.Action, models.AuditActionVeloxCallbackSent)
	}
	if e.Result != models.AuditResultSuccess {
		t.Errorf("audit.Result = %q; want success", e.Result)
	}
	if e.ResourceType != "external_delivery" {
		t.Errorf("audit.ResourceType = %q; want external_delivery", e.ResourceType)
	}
}

// 2. 5xx exhaustion: 5 POSTs, all failing audit metadata correct.
func TestVeloxCallback_5xxExhaustsAllAttempts(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	audit := &mockAuditStore{}
	d := newTestDispatcher(t, nil, &http.Client{Timeout: 5 * time.Second}, audit, nil)

	url := server.URL + "/post"
	delivery := &models.ExternalDelivery{
		ID:                 "sdel_5xx",
		ExternalDeliveryID: "delivery_5xx",
		CallbackURL:        &url,
	}

	err := d.Dispatch(context.Background(), delivery, VeloxCallbackPublished, &VeloxCallbackPayload{EventID: "evt_5xx"})
	if err == nil {
		t.Fatal("expected non-nil err after exhaustion")
	}
	if got := atomic.LoadInt32(&calls); got != 5 {
		t.Errorf("expected 5 POST attempts; got %d", got)
	}
	if !strings.Contains(err.Error(), "5") || !strings.Contains(err.Error(), "502") {
		t.Errorf("err should mention 5 attempts + status 502; got %v", err)
	}

	e := audit.lastEntry(t)
	if e.Action != models.AuditActionVeloxCallbackFailed {
		t.Errorf("audit.Action = %q; want %q", e.Action, models.AuditActionVeloxCallbackFailed)
	}
	if e.Result != models.AuditResultFailure {
		t.Errorf("audit.Result = %q; want failure", e.Result)
	}
	metaBytes, _ := json.Marshal(e.Metadata)
	var meta map[string]string
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		t.Fatalf("metadata unmarshal: %v", err)
	}
	if meta["attempts"] != "5" {
		t.Errorf("audit.attempts = %q; want 5", meta["attempts"])
	}
	if meta["max_attempts"] != "5" {
		t.Errorf("audit.max_attempts = %q; want 5", meta["max_attempts"])
	}
	if meta["last_status"] != "502" {
		t.Errorf("audit.last_status = %q; want 502", meta["last_status"])
	}
	if meta["error"] == "" {
		t.Error("audit.error should be present for failure")
	}
}

// 3. 5xx recovery: 3 attempts then ok.
func TestVeloxCallback_5xxRecoverOnThirdAttempt(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	audit := &mockAuditStore{}
	d := newTestDispatcher(t, nil, &http.Client{Timeout: 5 * time.Second}, audit, nil)

	url := server.URL + "/post"
	delivery := &models.ExternalDelivery{
		ID:                 "sdel_recover",
		ExternalDeliveryID: "delivery_recover",
		CallbackURL:        &url,
	}

	if err := d.Dispatch(context.Background(), delivery, VeloxCallbackPublished, &VeloxCallbackPayload{EventID: "evt_recover"}); err != nil {
		t.Fatalf("expected nil err; got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("expected 3 POST attempts; got %d", got)
	}
	e := audit.lastEntry(t)
	if e.Result != models.AuditResultSuccess {
		t.Errorf("audit.Result = %q; want success", e.Result)
	}
}

// 4. 4xx terminal: 1 attempt + audit failure.
func TestVeloxCallback_4xxTerminalNoRetry(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	audit := &mockAuditStore{}
	d := newTestDispatcher(t, nil, &http.Client{Timeout: 5 * time.Second}, audit, nil)

	url := server.URL + "/post"
	delivery := &models.ExternalDelivery{
		ID:                 "sdel_4xx",
		ExternalDeliveryID: "delivery_4xx",
		CallbackURL:        &url,
	}

	err := d.Dispatch(context.Background(), delivery, VeloxCallbackPublished, &VeloxCallbackPayload{EventID: "evt_4xx"})
	if err == nil {
		t.Fatal("expected non-nil err on 4xx")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("expected exactly 1 POST attempt; got %d", got)
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("err should reference 400 status; got %v", err)
	}
	e := audit.lastEntry(t)
	if e.Action != models.AuditActionVeloxCallbackFailed {
		t.Errorf("audit should be failed; got %q", e.Action)
	}
}

// 5. Missing callback_url: error without HTTP + no audit.
func TestVeloxCallback_MissingCallbackURL(t *testing.T) {
	audit := &mockAuditStore{}
	d := newTestDispatcher(t, nil, &http.Client{Timeout: 5 * time.Second}, audit, nil)

	delivery := &models.ExternalDelivery{
		ID:                 "sdel_nourl",
		ExternalDeliveryID: "delivery_nourl",
		CallbackURL:        nil,
	}
	err := d.Dispatch(context.Background(), delivery, VeloxCallbackPublished, &VeloxCallbackPayload{EventID: "evt_nourl"})
	if err == nil {
		t.Fatal("expected err on missing callback_url")
	}
	if !strings.Contains(err.Error(), "callback_url") {
		t.Errorf("err should mention callback_url; got %v", err)
	}
	if got := audit.entryCount(); got != 0 {
		t.Errorf("audit must NOT be emitted for missing URL; got %d entries", got)
	}
}

// 6. Nil dispatcher + empty secret → ErrNotConfigured.
func TestVeloxCallback_NotConfigured(t *testing.T) {
	var nilDispatcher *VeloxCallbackDispatcher
	if err := nilDispatcher.Dispatch(context.Background(), &models.ExternalDelivery{}, VeloxCallbackPublished, &VeloxCallbackPayload{}); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("nil dispatcher: err = %v; want ErrNotConfigured", err)
	}

	emptySecretDispatcher := NewVeloxCallbackDispatcher(nil, &http.Client{}, nil, slog.Default())
	cbURL := "https://example.com/cb"
	if err := emptySecretDispatcher.Dispatch(context.Background(), &models.ExternalDelivery{CallbackURL: &cbURL}, VeloxCallbackPublished, &VeloxCallbackPayload{}); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("empty secret: err = %v; want ErrNotConfigured", err)
	}
}

// 7. HMAC reconciliation with stable clock.
func TestVeloxCallback_SignatureMatchStableClock(t *testing.T) {
	var captured struct {
		ts   int64
		body []byte
		sig  string
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ts, _ := strconv.ParseInt(r.Header.Get("X-Velox-Timestamp"), 10, 64)
		body, _ := io.ReadAll(r.Body)
		captured.ts = ts
		captured.body = body
		captured.sig = strings.TrimPrefix(r.Header.Get("X-Velox-Signature"), "sha256=")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	audit := &mockAuditStore{}
	stableClock := func() time.Time { return time.Unix(1784000000, 0) }
	d := NewVeloxCallbackDispatcher([]byte("keyboard cat"), &http.Client{Timeout: 5 * time.Second}, audit, slog.Default())
	d.clock = stableClock

	url := server.URL + "/post"
	delivery := &models.ExternalDelivery{
		ID:                 "sdel_sig",
		ExternalDeliveryID: "delivery_sig",
		CallbackURL:        &url,
	}
	if err := d.Dispatch(context.Background(), delivery, VeloxCallbackPublished, &VeloxCallbackPayload{EventID: "evt_sig", Status: "published"}); err != nil {
		t.Fatalf("expected nil err; got %v", err)
	}

	mac := hmac.New(sha256.New, []byte("keyboard cat"))
	mac.Write([]byte(strconv.FormatInt(captured.ts, 10)))
	mac.Write([]byte{'.'})
	mac.Write(captured.body)
	expected := hex.EncodeToString(mac.Sum(nil))
	if expected != captured.sig {
		t.Errorf("HMAC mismatch\n  got:  %s\n  want: %s", captured.sig, expected)
	}
}

// 8. Auto-fill payload fields.
func TestVeloxCallback_AutoFillPayload(t *testing.T) {
	var receivedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	audit := &mockAuditStore{}
	d := newTestDispatcher(t, nil, &http.Client{Timeout: 5 * time.Second}, audit, nil)
	d.idGen = func() string { return "evt_auto_fill" }

	url := server.URL + "/post"
	delivery := &models.ExternalDelivery{
		ID:                 "sdel_autofill",
		ExternalDeliveryID: "delivery_autofill",
		CallbackURL:        &url,
	}
	if err := d.Dispatch(context.Background(), delivery, VeloxCallbackPublished, &VeloxCallbackPayload{}); err != nil {
		t.Fatalf("expected nil err; got %v", err)
	}
	var decoded VeloxCallbackPayload
	if err := json.Unmarshal(receivedBody, &decoded); err != nil {
		t.Fatalf("body unmarshal: %v", err)
	}
	if decoded.EventID != "evt_auto_fill" {
		t.Errorf("auto-fill EventID = %q; want evt_auto_fill", decoded.EventID)
	}
	if decoded.SocialDeliveryID != "sdel_autofill" {
		t.Errorf("auto-fill SocialDeliveryID = %q; want sdel_autofill", decoded.SocialDeliveryID)
	}
	if decoded.ExternalDeliveryID != "delivery_autofill" {
		t.Errorf("auto-fill ExternalDeliveryID = %q; want delivery_autofill", decoded.ExternalDeliveryID)
	}
	if decoded.Status != "published" {
		t.Errorf("auto-fill Status = %q; want published", decoded.Status)
	}
}

// 9. Audit metadata fields populated correctly on failure.
func TestVeloxCallback_AuditMetadataFields(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	audit := &mockAuditStore{}
	d := newTestDispatcher(t, nil, &http.Client{Timeout: 5 * time.Second}, audit, nil)

	url := server.URL + "/audit-test"
	delivery := &models.ExternalDelivery{
		ID:                 "sdel_meta",
		ExternalDeliveryID: "delivery_meta",
		CallbackURL:        &url,
	}
	err := d.Dispatch(context.Background(), delivery, VeloxCallbackDeadLetter, &VeloxCallbackPayload{EventID: "evt_meta"})
	if err == nil {
		t.Fatal("expected err")
	}
	e := audit.lastEntry(t)

	metaBytes, _ := json.Marshal(e.Metadata)
	var meta map[string]string
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		t.Fatalf("metadata unmarshal: %v", err)
	}
	if meta["external_delivery_id"] != "delivery_meta" {
		t.Errorf("metadata.external_delivery_id = %q", meta["external_delivery_id"])
	}
	if meta["callback_url"] != url {
		t.Errorf("metadata.callback_url = %q", meta["callback_url"])
	}
	if meta["event"] != "dead_letter" {
		t.Errorf("metadata.event = %q; want dead_letter", meta["event"])
	}
	if meta["event_id"] != "evt_meta" {
		t.Errorf("metadata.event_id = %q; want evt_meta", meta["event_id"])
	}
	if meta["attempts"] != "5" {
		t.Errorf("metadata.attempts = %q; want 5", meta["attempts"])
	}
	if meta["max_attempts"] != "5" {
		t.Errorf("metadata.max_attempts = %q; want 5", meta["max_attempts"])
	}
	if meta["last_status"] != "502" {
		t.Errorf("metadata.last_status = %q; want 502", meta["last_status"])
	}
	if meta["error"] == "" {
		t.Error("metadata.error should be set on failure")
	}
}

// 10. nil auditStore is non-fatal.
func TestVeloxCallback_NilAuditStoreNoOp(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	d := NewVeloxCallbackDispatcher([]byte(testVeloxSecret), &http.Client{Timeout: 5 * time.Second}, nil, slog.Default())
	d.randSrc = rand.New(rand.NewSource(42))

	url := server.URL + "/post"
	delivery := &models.ExternalDelivery{
		ID:                 "sdel_noaudit",
		ExternalDeliveryID: "delivery_noaudit",
		CallbackURL:        &url,
	}
	if err := d.Dispatch(context.Background(), delivery, VeloxCallbackPublished, &VeloxCallbackPayload{EventID: "evt_noaudit"}); err != nil {
		t.Fatalf("nil auditStore should not affect success path; got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("expected 1 POST; got %d", got)
	}
}

// 11. auditStore.Append error does not fail dispatch.
func TestVeloxCallback_AuditStoreErrorNonFatal(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	audit := &mockAuditStore{appendErr: errors.New("db connection refused")}
	d := NewVeloxCallbackDispatcher([]byte(testVeloxSecret), &http.Client{Timeout: 5 * time.Second}, audit, slog.Default())
	d.randSrc = rand.New(rand.NewSource(42))

	url := server.URL + "/post"
	delivery := &models.ExternalDelivery{
		ID:                 "sdel_auderr",
		ExternalDeliveryID: "delivery_auderr",
		CallbackURL:        &url,
	}
	if err := d.Dispatch(context.Background(), delivery, VeloxCallbackPublished, &VeloxCallbackPayload{EventID: "evt_auderr"}); err != nil {
		t.Fatalf("audit-store error should be non-fatal; got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("expected 1 POST despite audit err; got %d", got)
	}
}

// 12. Context cancellation during retry backoff short-circuits.
func TestVeloxCallback_ContextCancelDuringBackoff(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	audit := &mockAuditStore{}
	d := NewVeloxCallbackDispatcher([]byte(testVeloxSecret), &http.Client{Timeout: 5 * time.Second}, audit, slog.Default())
	d.baseDelay = 500 * time.Millisecond
	d.jitterMin = 0
	d.jitterMax = 1 * time.Millisecond
	d.randSrc = rand.New(rand.NewSource(42))

	url := server.URL + "/post"
	delivery := &models.ExternalDelivery{
		ID:                 "sdel_cancel",
		ExternalDeliveryID: "delivery_cancel",
		CallbackURL:        &url,
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	err := d.Dispatch(ctx, delivery, VeloxCallbackPublished, &VeloxCallbackPayload{EventID: "evt_cancel"})
	if err == nil {
		t.Fatal("expected non-nil err on context cancel")
	}
	if !strings.Contains(err.Error(), "cancel") && !errors.Is(err, context.Canceled) {
		t.Errorf("err should mention cancel/ctx.Canceled; got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got >= 5 {
		t.Errorf("expected fewer than 5 attempts after ctx cancel; got %d", got)
	}
}

// 13. Default event-id generator produces evt_<32-hex>.
func TestVeloxCallback_DefaultEventIDFormat(t *testing.T) {
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Velox-Event-ID")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	audit := &mockAuditStore{}
	d := NewVeloxCallbackDispatcher([]byte(testVeloxSecret), &http.Client{Timeout: 5 * time.Second}, audit, slog.Default())
	d.randSrc = rand.New(rand.NewSource(42))

	url := server.URL + "/post"
	delivery := &models.ExternalDelivery{
		ID:                 "sdel_eid",
		ExternalDeliveryID: "delivery_eid",
		CallbackURL:        &url,
	}
	if err := d.Dispatch(context.Background(), delivery, VeloxCallbackPublished, &VeloxCallbackPayload{}); err != nil {
		t.Fatalf("expected nil err; got %v", err)
	}
	if !strings.HasPrefix(got, "evt_") {
		t.Errorf("default event id should start with evt_; got %q", got)
	}
	if len(got) != len("evt_")+32 {
		t.Errorf("default event id should be 36 chars (evt_ + 32 hex); got %q (%d chars)", got, len(got))
	}
}
