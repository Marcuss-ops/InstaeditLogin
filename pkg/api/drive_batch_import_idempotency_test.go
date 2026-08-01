package api

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// Idempotency-Key tests for the Drive batch import endpoint
// POST /api/v1/media/import/drive/folder (Stripe-style semantics).

func TestDriveBatchImport_IdempotencyKey_HappyPath_InsertsCache(t *testing.T) {
	// 3-video batch + Idempotency-Key=batch-key-v1. Verifies:
	//   - 202 returned with the scheduled entries
	//   - parent idempotency_records row created with resource_type=
	//     "drive_batch" and resource_id=first job's id
	//   - side row in idempotency_batch_replays created with the
	//     same JSON bytes that were written to the wire (byte-for-byte)
	files := []services.GoogleDriveFile{
		{ID: "f-1", Name: "intro.mp4", MimeType: "video/mp4"},
		{ID: "f-2", Name: "demo.mp4", MimeType: "video/mp4"},
		{ID: "f-3", Name: "outro.mp4", MimeType: "video/mp4"},
	}
	lister := &mockDriveFolderLister{files: files}
	store := &mockUploadJobStore{}
	idemStore := newMockIdempotencyStore()
	r := newBatchImportTestRouterWithIdem(lister, store, idemStore)

	const idemKey = "batch-key-v1"
	body := `{"folder_id":"abc-folder","workspace_id":1,"facebook_account_id":50, "drive_account_id":99}`
	w := runBatchImportPost(t, r, body, idemKey)

	if w.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d: %s", w.Code, w.Body.String())
	}
	respBytes := w.Body.Bytes()

	// parent record exists.
	parent, err := idemStore.FindActiveByKey(1, idemKey, time.Now())
	if err != nil {
		t.Fatalf("FindActiveByKey: %v", err)
	}
	if parent == nil {
		t.Fatal("expected parent idempotency record to be persisted on first-call success")
	}
	if parent.ResourceType != "drive_batch" {
		t.Errorf("parent.ResourceType: want drive_batch, got %q", parent.ResourceType)
	}
	if parent.ResponseStatus != http.StatusAccepted {
		t.Errorf("parent.ResponseStatus: want 202, got %d", parent.ResponseStatus)
	}
	if parent.ResourceID <= 0 {
		t.Errorf("parent.ResourceID should be the first job id (>0), got %d", parent.ResourceID)
	}
	// Tighten: resource_id must be the FIRST scheduled job's id, not
	// just any positive number. Catches the regression where a future
	// refactor accidentally points at a different entry (e.g. the LAST
	// scheduled job, or 0-as-sentinel).
	if len(store.jobs) == 0 {
		t.Fatal("no upload jobs created; cannot verify resource_id contract")
	}
	if parent.ResourceID != store.jobs[0].ID {
		t.Errorf("parent.ResourceID should equal first job id (=%d), got %d (regression: caching wrong entry?)",
			store.jobs[0].ID, parent.ResourceID)
	}
	wantReqHash := idempotencyHash([]byte(body))
	if !bytes.Equal(parent.RequestHash, wantReqHash) {
		t.Errorf("parent.RequestHash mismatch (sha256 of body)")
	}

	// side row exists with byte-identical payload.
	side, err := idemStore.FindBatchReplay(parent.ID)
	if err != nil {
		t.Fatalf("FindBatchReplay: %v", err)
	}
	if side == nil {
		t.Fatal("expected batch_replay side row to be persisted alongside the parent")
	}
	if !bytes.Equal(side.ResponsePayload, respBytes) {
		t.Errorf("side.ResponsePayload should equal wire bytes byte-for-byte\n   wire:  %q\n   cache: %q",
			string(respBytes), string(side.ResponsePayload))
	}
}

func TestDriveBatchImport_IdempotencyKey_ReplaySameHash_ReturnsCachedEntries(t *testing.T) {
	// First call populates the cache; second call (same key + same
	// hash) replays byte-identical JSON. The mock upload job store
	// records ANY Create call, so we also assert the replay did NOT
	// create new upload_jobs (otherwise we'd end up with 4+4=8 jobs
	// instead of 4).
	files := []services.GoogleDriveFile{
		{ID: "f-1", Name: "a.mp4", MimeType: "video/mp4"},
		{ID: "f-2", Name: "b.mp4", MimeType: "video/mp4"},
	}
	lister := &mockDriveFolderLister{files: files}
	store := &mockUploadJobStore{}
	idemStore := newMockIdempotencyStore()
	r := newBatchImportTestRouterWithIdem(lister, store, idemStore)

	const idemKey = "batch-replay-key"
	body := `{"folder_id":"replay-folder","workspace_id":1,"facebook_account_id":50, "drive_account_id":99}`

	// First call writes to cache.
	w1 := runBatchImportPost(t, r, body, idemKey)
	if w1.Code != http.StatusAccepted {
		t.Fatalf("first call want 202, got %d: %s", w1.Code, w1.Body.String())
	}
	if len(store.jobs) != 2 {
		t.Fatalf("first call: want 2 jobs created, got %d", len(store.jobs))
	}
	firstWire := w1.Body.Bytes()

	// Second call (same key + same body hash) REPLAYS byte-for-byte.
	w2 := runBatchImportPost(t, r, body, idemKey)
	if w2.Code != http.StatusAccepted {
		t.Fatalf("replay want 202, got %d: %s", w2.Code, w2.Body.String())
	}
	if !bytes.Equal(w2.Body.Bytes(), firstWire) {
		t.Errorf("replay bytes differ from original wire bytes\n   wire:  %q\n   cache: %q",
			string(firstWire), string(w2.Body.Bytes()))
	}
	// Critical: replay must NOT have created new upload jobs.
	if len(store.jobs) != 2 {
		t.Errorf("replay must not create new jobs; want 2 total, got %d", len(store.jobs))
	}
}

func TestDriveBatchImport_IdempotencyKey_DifferentHash_Returns409(t *testing.T) {
	// First call with body A populates the cache. Second call with
	// body B but the same Idempotency-Key MUST fail with 409 — the
	// client sent a different request body under the same key, which
	// is the Stripe-documented conflict semantics.
	files := []services.GoogleDriveFile{
		{ID: "f-1", Name: "a.mp4", MimeType: "video/mp4"},
	}
	lister := &mockDriveFolderLister{files: files}
	store := &mockUploadJobStore{}
	idemStore := newMockIdempotencyStore()
	r := newBatchImportTestRouterWithIdem(lister, store, idemStore)

	const idemKey = "conflict-key"
	bodyA := `{"folder_id":"folder-A","workspace_id":1,"facebook_account_id":50, "drive_account_id":99}`
	bodyB := `{"folder_id":"folder-B","workspace_id":1,"facebook_account_id":50, "drive_account_id":99}`

	w1 := runBatchImportPost(t, r, bodyA, idemKey)
	if w1.Code != http.StatusAccepted {
		t.Fatalf("first call want 202, got %d: %s", w1.Code, w1.Body.String())
	}

	w2 := runBatchImportPost(t, r, bodyB, idemKey)
	if w2.Code != http.StatusConflict {
		t.Fatalf("hash mismatch want 409, got %d: %s", w2.Code, w2.Body.String())
	}
	// Critical: the conflict must NOT create new upload jobs.
	if len(store.jobs) != 1 {
		t.Errorf("conflict path must not create new jobs; want 1 from first call, got %d", len(store.jobs))
	}
}

func TestDriveBatchImport_IdempotencyKey_NoHeader_DoesNotCache(t *testing.T) {
	// Pure positive control: a request without Idempotency-Key runs
	// the handler normally and writes NO cache row. We assert the
	// store is empty after a single call so future contributors can't
	// silently flip the default to "cache everything".
	files := []services.GoogleDriveFile{
		{ID: "f-1", Name: "no-cache.mp4", MimeType: "video/mp4"},
	}
	lister := &mockDriveFolderLister{files: files}
	store := &mockUploadJobStore{}
	idemStore := newMockIdempotencyStore()
	r := newBatchImportTestRouterWithIdem(lister, store, idemStore)

	body := `{"folder_id":"no-key","workspace_id":1,"facebook_account_id":50, "drive_account_id":99}`
	w := runBatchImportPost(t, r, body, "")
	if w.Code != http.StatusAccepted {
		t.Fatalf("no-header want 202, got %d: %s", w.Code, w.Body.String())
	}

	got, err := idemStore.FindActiveByKey(1, "", time.Now())
	if err != nil {
		t.Fatalf("FindActiveByKey: %v", err)
	}
	if got != nil {
		t.Errorf("no-header should not cache; got %+v", got)
	}
	if len(idemStore.records) != 0 {
		t.Errorf("no-header should leave store empty; got %d records", len(idemStore.records))
	}
	if len(store.jobs) != 1 {
		t.Errorf("handler still ran (1 job expected), got %d", len(store.jobs))
	}
}

func TestDriveBatchImport_IdempotencyKey_TooLong_Returns400(t *testing.T) {
	// Stripe-mandated limit: 255 chars. A 256-char key MUST 400.
	files := []services.GoogleDriveFile{
		{ID: "f-1", Name: "x.mp4", MimeType: "video/mp4"},
	}
	lister := &mockDriveFolderLister{files: files}
	store := &mockUploadJobStore{}
	idemStore := newMockIdempotencyStore()
	r := newBatchImportTestRouterWithIdem(lister, store, idemStore)

	longKey := strings.Repeat("k", 256) // 256 > 255 (Stripe limit)
	body := `{"folder_id":"long-key","workspace_id":1,"facebook_account_id":50, "drive_account_id":99}`
	w := runBatchImportPost(t, r, body, longKey)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 on 256-char key, got %d: %s", w.Code, w.Body.String())
	}
	// The lookup short-circuited, so no cache row + no upload_jobs.
	if len(store.jobs) != 0 {
		t.Errorf("long-key path must not create jobs; got %d", len(store.jobs))
	}
	if len(idemStore.records) != 0 {
		t.Errorf("long-key path must not cache; got %d records", len(idemStore.records))
	}
}

func TestDriveBatchImport_IdempotencyKey_EmptyBatchNotCached(t *testing.T) {
	// Defence-in-depth: a successful first call that returned 200
	// (empty folder / needs_google_drive_api_key) MUST NOT cache —
	// re-trying after fixing the underlying issue should re-run the
	// handler to get a fresh response.
	lister := &mockDriveFolderLister{files: nil}
	store := &mockUploadJobStore{}
	idemStore := newMockIdempotencyStore()
	r := newBatchImportTestRouterWithIdem(lister, store, idemStore)

	const idemKey = "empty-folder-key"
	body := `{"folder_id":"empty","workspace_id":1,"facebook_account_id":50, "drive_account_id":99}`
	w := runBatchImportPost(t, r, body, idemKey)

	if w.Code != http.StatusOK {
		t.Fatalf("empty folder want 200, got %d: %s", w.Code, w.Body.String())
	}
	got, _ := idemStore.FindActiveByKey(1, idemKey, time.Now())
	if got != nil {
		t.Errorf("empty-folder response must not be cached; got %+v", got)
	}
}

func TestDriveBatchImport_IdempotencyKey_CrossTenant_DoesNotReplay(t *testing.T) {
	// SECURITY: attacker (JWT user 2) targets user 1's workspace
	// (workspace_id=1 in the body) while reusing user 1's
	// Idempotency-Key. Workspace ownership check fires FIRST and
	// blocks the request with 403 BEFORE the cache lookup runs.
	// If the handler skipped the ownership check, the cache lookup
	// would hit user 1's row and replay their entries — a
	// cross-tenant data leak.
	wsStore := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			// ws 1 owned by user 1, ws 2 owned by user 2.
			return &models.Workspace{ID: id, Name: "Mine", OwnerID: id}, nil
		},
	}
	files := []services.GoogleDriveFile{
		{ID: "f-1", Name: "a.mp4", MimeType: "video/mp4"},
	}
	lister := &mockDriveFolderLister{files: files}
	capRouter := services.NewCapabilityRouter()
	capRouter.Register("google-drive", lister) // not strictly needed (cross-tenant test 403s before the lister), but registered for completeness
	store := &mockUploadJobStore{}
	idemStore := newMockIdempotencyStore()
	userStore := &mockUserStore{
		// Body uses drive_account_id=99 + facebook_account_id=1.
		// Drive lookup must resolve to a google-drive account
		// owned by the JWT caller (user 1 on the first call); the
		// previous generic lookup returned Platform=Facebook
		// for id=99, which made the handler short-circuit on the
		// platform check with 404 "google drive account not
		// found". User 2's cross-tenant retry never reaches
		// this lookup — the workspace-ownership gate fires
		// first (ws 1 owner=1, JWT user 2 → 403).
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			if id == 99 {
				return &models.PlatformAccount{ID: 99, UserID: 1, Platform: "google-drive"}, nil
			}
			return &models.PlatformAccount{ID: id, UserID: id, Platform: models.PlatformFacebook}, nil
		},
		listFn: func(userID int64, _ string) ([]*models.PlatformAccount, error) {
			return nil, nil
		},
	}
	// WithCredentialVault is REQUIRED: after fix #1 (userStore
	// resolving drive_account_id=99 to google-drive + user 1),
	// the next failure point along the drive-batch import flow
	// is the vault check — handleDriveBatchImport returns 501
	// "credential vault not configured" when r.vault == nil.
	// fakeVault in fakevault_test.go implements
	// credentials.VaultAPI without hitting Postgres so the
	// existing driveAccessToken path returns a canned bearer
	// for the (idempotent, fully-cached) replay assertion below.
	r := mustNewRouterWithDefaults(
		capRouter,
		userStore,
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
		WithWorkspaceStore(wsStore),
		WithUploadJobStore(store),
		WithIdempotencyStore(idemStore),
		WithCredentialVault(&fakeVault{}),
		WithOneTimeCodeStore(NewInMemoryOneTimeCodeStore(60*time.Second)),
	)

	const idemKey = "cross-tenant-key"
	body := `{"folder_id":"x","workspace_id":1,"facebook_account_id":1, "drive_account_id":99}`

	// User 1 (JWT) targets workspace 1 (their own). Cache populates
	// under (workspace_id=1, idempotency_key=cross-tenant-key).
	w1 := mustServe(t, r, body, idemKey, 1)
	if w1.Code != http.StatusAccepted {
		t.Fatalf("user-1 first call want 202, got %d: %s", w1.Code, w1.Body.String())
	}
	// Sanity: cache exists for user 1.
	parent, _ := idemStore.FindActiveByKey(1, idemKey, time.Now())
	if parent == nil {
		t.Fatal("cache should exist for (1, cross-tenant-key) after user-1 first call")
	}

	// Attacker (JWT user 2) sends the SAME body + SAME key but their
	// own JWT. Workspace ownership check: ws 1 owner is user 1, JWT
	// caller is user 2 → 403. Cache lookup NEVER happens for user 2.
	w2 := mustServe(t, r, body, idemKey, 2)
	if w2.Code != http.StatusForbidden {
		t.Fatalf("user-2 retry want 403 (workspace ownership gate before cache lookup), got %d: %s",
			w2.Code, w2.Body.String())
	}
	// Also: no cross-tenant cache row under user 2's scope.
	if got, _ := idemStore.FindActiveByKey(2, idemKey, time.Now()); got != nil {
		t.Errorf("user 2 must not have a cache row for that key (would indicate cache leak)")
	}
}
