package services

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// fakeDeliveryProvider is the canonical 2-provider fixture for
// the dispatch-by-name acceptance test. Two providers registered
// under different Names; Deliver records the call AND returns
// a DeliveryResult whose ProviderName echo + the asset ID echo
// make per-provider test assertions trivial.
type fakeDeliveryProvider struct {
	pName           string
	deliverCalls    int
	lastAssetID     string
	lastDestRemote  string
	lastIdemKey     string
	returnStatus    string
	returnErr       error
	lastDeliverCall bool
}

func (f *fakeDeliveryProvider) Name() string { return f.pName }

func (f *fakeDeliveryProvider) Deliver(ctx context.Context, asset *models.MediaAsset, dest *models.DeliveryDestination, idempotencyKey string) (*models.DeliveryResult, error) {
	f.deliverCalls++
	f.lastDeliverCall = true
	if asset != nil {
		f.lastAssetID = asset.ID
	}
	if dest != nil {
		f.lastDestRemote = dest.RemoteID
	}
	f.lastIdemKey = idempotencyKey
	if f.returnErr != nil {
		return nil, f.returnErr
	}
	status := f.returnStatus
	if status == "" {
		status = "published"
	}
	return &models.DeliveryResult{
		ProviderName: f.pName,
		Status:       status,
		RemoteID:     dest.RemoteID,
		Metadata:     map[string]string{"fake_provider_id": f.pName, "idempotency_key": idempotencyKey},
	}, nil
}

// TestDeliveryRegistry_Register_Get_DispatchByName is the canonical
// acceptance test for Task 7/10: register 2 fake providers, then
// dispatch each by Name() and assert the right fake was called
// and returned the right result. Locks the public contract:
//   - Register(p) with non-empty unique names stores p.
//   - Get(name) returns the registered provider, no error.
//   - Deliver routes to the registered provider (not a global
//     fallback or last-writer-wins).
func TestDeliveryRegistry_Register_Get_DispatchByName(t *testing.T) {
	r := NewDeliveryRegistry()
	youtube := &fakeDeliveryProvider{pName: "youtube", returnStatus: "published"}
	drive := &fakeDeliveryProvider{pName: "google_drive", returnStatus: "processing"}

	if err := r.Register(youtube); err != nil {
		t.Fatalf("Register youtube: %v", err)
	}
	if err := r.Register(drive); err != nil {
		t.Fatalf("Register google_drive: %v", err)
	}

	asset := &models.MediaAsset{ID: "asset-1"}
	destYT := &models.DeliveryDestination{Provider: "youtube", RemoteID: "UC-test-channel"}
	destDR := &models.DeliveryDestination{Provider: "google_drive", RemoteID: "1ABC-folder"}

	gotYT, err := r.Get("youtube")
	if err != nil {
		t.Fatalf("Get youtube: %v", err)
	}
	resYT, err := gotYT.Deliver(context.Background(), asset, destYT, "idem-yt-1")
	if err != nil {
		t.Fatalf("Deliver youtube: %v", err)
	}
	if resYT.ProviderName != "youtube" {
		t.Errorf("resYT.ProviderName: want %q, got %q", "youtube", resYT.ProviderName)
	}
	if resYT.Status != "published" {
		t.Errorf("resYT.Status: want %q, got %q", "published", resYT.Status)
	}
	if youtube.deliverCalls != 1 {
		t.Errorf("youtube.deliverCalls: want 1, got %d (registry dispatched to wrong adapter?)", youtube.deliverCalls)
	}
	if drive.deliverCalls != 0 {
		t.Errorf("drive.deliverCalls: want 0 (cross-dispatch), got %d", drive.deliverCalls)
	}

	gotDR, err := r.Get("google_drive")
	if err != nil {
		t.Fatalf("Get google_drive: %v", err)
	}
	resDR, err := gotDR.Deliver(context.Background(), asset, destDR, "idem-dr-1")
	if err != nil {
		t.Fatalf("Deliver google_drive: %v", err)
	}
	if resDR.ProviderName != "google_drive" {
		t.Errorf("resDR.ProviderName: want %q, got %q", "google_drive", resDR.ProviderName)
	}
	if resDR.Status != "processing" {
		t.Errorf("resDR.Status: want %q, got %q", "processing", resDR.Status)
	}
	if drive.deliverCalls != 1 {
		t.Errorf("drive.deliverCalls: want 1, got %d", drive.deliverCalls)
	}
	if youtube.deliverCalls != 1 {
		t.Errorf("youtube.deliverCalls: want still 1 (no cross-dispatch), got %d", youtube.deliverCalls)
	}

	// Args preserved end-to-end.
	if youtube.lastIdemKey != "idem-yt-1" {
		t.Errorf("youtube lastIdemKey: want %q, got %q", "idem-yt-1", youtube.lastIdemKey)
	}
	if drive.lastIdemKey != "idem-dr-1" {
		t.Errorf("drive lastIdemKey: want %q, got %q", "idem-dr-1", drive.lastIdemKey)
	}
	if youtube.lastDestRemote != "UC-test-channel" {
		t.Errorf("youtube lastDestRemote: want %q, got %q", "UC-test-channel", youtube.lastDestRemote)
	}
	if drive.lastDestRemote != "1ABC-folder" {
		t.Errorf("drive lastDestRemote: want %q, got %q", "1ABC-folder", drive.lastDestRemote)
	}
}

// TestDeliveryRegistry_Register_DuplicateName_Errors verifies the
// fail-fast path: a future refactor introducing two adapters that
// both Name()==X surfaces ErrDeliveryProviderAlreadyRegistered at
// startup, NOT at dispatch time. Locked invariant: Register
// refuses to silently overwrite the first registration.
func TestDeliveryRegistry_Register_DuplicateName_Errors(t *testing.T) {
	r := NewDeliveryRegistry()
	first := &fakeDeliveryProvider{pName: "youtube"}
	second := &fakeDeliveryProvider{pName: "youtube"}
	if err := r.Register(first); err != nil {
		t.Fatalf("Register first youtube: %v", err)
	}
	err := r.Register(second)
	if err == nil {
		t.Fatalf("Register duplicate youtube: want non-nil error, got nil")
	}
	if !errors.Is(err, ErrDeliveryProviderAlreadyRegistered) {
		t.Errorf("Register duplicate: want wrapped ErrDeliveryProviderAlreadyRegistered, got %v", err)
	}
	// First registration must still be active (no silent overwrite).
	got, err := r.Get("youtube")
	if err != nil {
		t.Fatalf("Get youtube after duplicate register: %v", err)
	}
	// Pointer identity: must be the first fake, not the second.
	if got != DeliveryProvider(first) {
		t.Errorf("registry returned a different provider after duplicate register; want the first, got the second (silent overwrite bug)")
	}
}

// TestDeliveryRegistry_Get_UnknownName_NotFound verifies the
// canonical lookup-miss case is surfaced via the typed sentinel
// so the publish_worker can errors.Is and skip with a warn log
// (NOT panic, NOT a 5xx).
func TestDeliveryRegistry_Get_UnknownName_NotFound(t *testing.T) {
	r := NewDeliveryRegistry()
	_, err := r.Get("nonexistent_provider")
	if err == nil {
		t.Fatal("Get nonexistent: want non-nil error, got nil")
	}
	if !errors.Is(err, ErrDeliveryProviderNotFound) {
		t.Errorf("Get nonexistent: want wrapped ErrDeliveryProviderNotFound, got %v", err)
	}
	if !strings.Contains(err.Error(), "nonexistent_provider") {
		t.Errorf("error message must mention the requested name (operator-dashboard remediation); got %v", err)
	}
	// Names() remediation hint must list the known providers.
	if !strings.Contains(err.Error(), "(known providers:") {
		t.Errorf("error message must include known-providers list; got %v", err)
	}
}

// TestDeliveryRegistry_Names_Len verifies the bootstrap-time
// sanity helpers correctly report registered-provider counts.
// Useful for the bootstrap log line that gates "did startup
// register all expected providers?".
func TestDeliveryRegistry_Names_Len(t *testing.T) {
	r := NewDeliveryRegistry()
	if r.Len() != 0 {
		t.Errorf("empty registry Len: want 0, got %d", r.Len())
	}
	if names := r.Names(); len(names) != 0 {
		t.Errorf("empty registry Names: want [], got %v", names)
	}
	_ = r.Register(&fakeDeliveryProvider{pName: "a"})
	_ = r.Register(&fakeDeliveryProvider{pName: "b"})
	if r.Len() != 2 {
		t.Errorf("Len after 2 registers: want 2, got %d", r.Len())
	}
	names := r.Names()
	if len(names) != 2 {
		t.Fatalf("Names: want 2 entries, got %d (%v)", len(names), names)
	}
	gotSet := map[string]bool{}
	for _, n := range names {
		gotSet[n] = true
	}
	if !gotSet["a"] || !gotSet["b"] {
		t.Errorf("Names: want {a, b}, got %v", names)
	}
}

// TestDeliveryRegistry_NilAndEmptyName_Guards verifies the
// input-validation branches: nil adapter and empty name both
// surface the typed sentinel (NOT a panic on the dispatch path).
func TestDeliveryRegistry_NilAndEmptyName_Guards(t *testing.T) {
	r := NewDeliveryRegistry()
	if err := r.Register(nil); err == nil {
		t.Errorf("Register nil: want non-nil error, got nil")
	}
	empty := &fakeDeliveryProvider{pName: ""}
	if err := r.Register(empty); err == nil {
		t.Errorf("Register empty-name: want non-nil error, got nil")
	}
	if _, err := r.Get(""); err == nil {
		t.Errorf("Get empty-name: want non-nil error, got nil")
	}
}

// TestYouTubeDeliveryAdapter_Name verifies the canonical registry
// key. Locks the contract that bootstrap registrations can rely
// on ("my platform == "youtube" matches the registered adapter").
func TestYouTubeDeliveryAdapter_Name(t *testing.T) {
	a := NewYouTubeDeliveryAdapter(nil) // nil publisher tolerated because Deliver is what panics; Name is cheap
	if got := a.Name(); got != "youtube" {
		t.Errorf("Name: want %q, got %q", "youtube", got)
	}
}

// TestYouTubeDeliveryAdapter_Deliver_HappyPath verifies the
// post-completion no-op forward: Deliver returns a published
// DeliveryResult without re-calling Publisher.Publish (the
// existing pre-publish tick already did the actual upload;
// a re-publish here would double the YouTube upload slot).
func TestYouTubeDeliveryAdapter_Deliver_HappyPath(t *testing.T) {
	// nil publisher: the adapter's Deliver does NOT touch the
	// wrapped Publisher (the body is "noop forward" by design),
	// so nil is safe — locked by this test so future refactors
	// that DO try to call Publish must wire a publisher.
	a := NewYouTubeDeliveryAdapter(nil)
	// MediaAsset fields available: ID, UserID, UploadKey,
	// ContentType, SizeBytes, Status, SHA256, ErrorMessage,
	// ExpiresAt, CreatedAt, UpdatedAt (Title lives on Post,
	// not MediaAsset — see internal/models/asset.go).
	asset := &models.MediaAsset{ID: "asset-1"}
	dest := &models.DeliveryDestination{Provider: "youtube", RemoteID: "UC-channel-1"}
	res, err := a.Deliver(context.Background(), asset, dest, "idem-1")
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if res.ProviderName != "youtube" {
		t.Errorf("ProviderName: want %q, got %q", "youtube", res.ProviderName)
	}
	if res.Status != "published" {
		t.Errorf("Status: want %q (no-op forward), got %q", "published", res.Status)
	}
	if res.RemoteID != "UC-channel-1" {
		t.Errorf("RemoteID: want %q, got %q", "UC-channel-1", res.RemoteID)
	}
	if metadata := res.Metadata["post_completion"]; metadata != "true" {
		t.Errorf("Metadata.post_completion: want %q, got %q", "true", metadata)
	}
}

// TestYouTubeDeliveryAdapter_Deliver_NilInputs verifies the
// defensive null-guards surface the typed sentinel so a caller
// can't crash the dispatch loop with a programming-error panic.
func TestYouTubeDeliveryAdapter_Deliver_NilInputs(t *testing.T) {
	a := NewYouTubeDeliveryAdapter(nil)
	asset := &models.MediaAsset{ID: "a"}
	dest := &models.DeliveryDestination{RemoteID: "UC-1"}
	if _, err := a.Deliver(context.Background(), nil, dest, "k"); !errors.Is(err, ErrDeliveryProviderNotImplemented) {
		t.Errorf("nil asset: want ErrDeliveryProviderNotImplemented, got %v", err)
	}
	if _, err := a.Deliver(context.Background(), asset, nil, "k"); !errors.Is(err, ErrDeliveryProviderNotImplemented) {
		t.Errorf("nil dest: want ErrDeliveryProviderNotImplemented, got %v", err)
	}
	if _, err := a.Deliver(context.Background(), asset, &models.DeliveryDestination{Provider: "youtube"}, "k"); !errors.Is(err, ErrDeliveryProviderNotImplemented) {
		t.Errorf("empty RemoteID: want ErrDeliveryProviderNotImplemented, got %v", err)
	}
}

// TestGoogleDriveDeliveryAdapter_NameAndDispatch verifies the
// Task 8/10 contract: the adapter's Name() returns
// models.PlatformGoogleDrive ("google-drive") and dispatches
// Deliver calls to the underlying *GoogleDriveDestination. The
// Task 7/10 stub (returning ErrDeliveryProviderNotImplemented)
// has been replaced by the real implementation; per-deliver
// failure modes now surface as ErrDriveConfig via the underlying
// destination (covered in delivery_drive_destination_test.go).
func TestGoogleDriveDeliveryAdapter_NameAndDispatch(t *testing.T) {
	// Construct a destination with nil dependencies — GoogleDriveDestination
	// accepts nil deps at construction (each Deliver does its own
	// nil-check + ErrDriveConfig return).
	dest := &GoogleDriveDestination{chunkSizeBytes: 256 * 1024}
	a, err := NewGoogleDriveDeliveryAdapter(dest)
	if err != nil {
		t.Fatalf("NewGoogleDriveDeliveryAdapter: %v", err)
	}
	if got := a.Name(); got != models.PlatformGoogleDrive {
		t.Errorf("Name: want %q, got %q", models.PlatformGoogleDrive, got)
	}
	asset := &models.MediaAsset{ID: "a", SizeBytes: 1024, ContentType: "video/mp4"}
	// Deliberately OMIT drive_account_id so the destination's
	// pre-flight config gate returns (nil, wrapped-ErrDriveConfig)
	// BEFORE any sqlmock lookup. The chunk-loop semantics for the
	// dedupe path are exercised by the destination's own test
	// file (TestGoogleDriveDestination_Deliver_AppPropertyDedupeHitSkipsUpload).
	destStruct := &models.DeliveryDestination{Provider: "google-drive", RemoteID: "1ABC-folder"}
	// The underlying destination's Deliver will return ErrDriveConfig
	// because dest.Config["drive_account_id"] is empty — the adapter
	// forwards the call to the destination's error path cleanly.
	_, deliverErr := a.Deliver(context.Background(), asset, destStruct, "k")
	if deliverErr == nil {
		t.Errorf("Deliver with missing drive_account_id must propagate ErrDriveConfig, but got nil")
	}
	if !errors.Is(deliverErr, ErrDriveConfig) {
		t.Errorf("Deliver: want wrapped ErrDriveConfig, got %v", deliverErr)
	}
}

// TestVeloxCallbackDeliveryAdapter_StubReturnsProcessing verifies
// the post-completion stub: enabled=false returns a no-op
// "processing" result. The HTTP fire is delegated to the existing
// internal_velox_callback_dispatcher.go wiring; this method is
// only the registry entry point.
func TestVeloxCallbackDeliveryAdapter_StubReturnsProcessing(t *testing.T) {
	a := NewVeloxCallbackDeliveryAdapter(false)
	if got := a.Name(); got != "velox_callback" {
		t.Errorf("Name: want %q, got %q", "velox_callback", got)
	}
	asset := &models.MediaAsset{ID: "a"}
	dest := &models.DeliveryDestination{Provider: "velox_callback", RemoteURL: "https://velox.example/cb"}
	res, err := a.Deliver(context.Background(), asset, dest, "k")
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if res.ProviderName != "velox_callback" {
		t.Errorf("ProviderName: want %q, got %q", "velox_callback", res.ProviderName)
	}
	if res.Status != "processing" {
		t.Errorf("Status: want %q (stub acknowledgement), got %q", "processing", res.Status)
	}
	if res.RemoteURL != "https://velox.example/cb" {
		t.Errorf("RemoteURL: want %q, got %q", "https://velox.example/cb", res.RemoteURL)
	}
}

// TestVeloxCallbackDeliveryAdapter_StubEmptyURL_Guards verifies
// the input-validation: a velox_callback destination with no
// RemoteURL surfaces the typed sentinel rather than silently
// no-op'ing (which would hide the operator's config bug).
func TestVeloxCallbackDeliveryAdapter_StubEmptyURL_Guards(t *testing.T) {
	a := NewVeloxCallbackDeliveryAdapter(false)
	asset := &models.MediaAsset{ID: "a"}
	if _, err := a.Deliver(context.Background(), asset, &models.DeliveryDestination{Provider: "velox_callback"}, "k"); !errors.Is(err, ErrDeliveryProviderNotImplemented) {
		t.Errorf("empty RemoteURL: want ErrDeliveryProviderNotImplemented, got %v", err)
	}
}
