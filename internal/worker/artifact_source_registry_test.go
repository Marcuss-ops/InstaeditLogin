package worker

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// fakeSource is the in-package ArtifactSource fake shared across
// the registry tests. Compile-time assertion: it satisfies
// ArtifactSource.
type fakeSource struct {
	name models.UploadJobSource
}

func (f *fakeSource) Name() models.UploadJobSource { return f.name }
func (f *fakeSource) Inspect(ctx context.Context, job *models.UploadJob) (*SourceMetadata, error) {
	return nil, ErrInspectNotImplemented
}
func (f *fakeSource) Open(ctx context.Context, job *models.UploadJob) (io.ReadCloser, error) {
	return nil, nil
}

var _ ArtifactSource = (*fakeSource)(nil)

// TestArtifactSourceRegistry_Register_RegistersAndResolves — happy
// path. Register adds via Name; Resolve returns the same pointer.
func TestArtifactSourceRegistry_Register_RegistersAndResolves(t *testing.T) {
	r := NewArtifactSourceRegistry()
	src := &fakeSource{name: models.UploadJobSourceVeloxArtifact}
	if err := r.Register(src); err != nil {
		t.Fatalf("Register should succeed: %v", err)
	}
	got, ok := r.Resolve(models.UploadJobSourceVeloxArtifact)
	if !ok {
		t.Fatal("Resolve should find the registered source")
	}
	if got != src {
		t.Fatal("Resolve returned a different pointer than Register inserted")
	}
}

// TestArtifactSourceRegistry_Register_RejectsDuplicate — calling
// Register twice with the same Name returns an error; the original
// source is preserved (no silent shadow).
func TestArtifactSourceRegistry_Register_RejectsDuplicate(t *testing.T) {
	r := NewArtifactSourceRegistry()
	first := &fakeSource{name: models.UploadJobSourceAuthenticatedDrive}
	second := &fakeSource{name: models.UploadJobSourceAuthenticatedDrive}
	if err := r.Register(first); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	err := r.Register(second)
	if err == nil {
		t.Fatal("second Register with same Name should error")
	}
	got, ok := r.Resolve(models.UploadJobSourceAuthenticatedDrive)
	if !ok || got != first {
		t.Fatal("original source must remain after duplicate rejection")
	}
}

// TestArtifactSourceRegistry_Register_RejectsNil — passing nil
// fails loud at boot so a half-wired registry doesn't silently
// resolve to nothing.
func TestArtifactSourceRegistry_Register_RejectsNil(t *testing.T) {
	r := NewArtifactSourceRegistry()
	err := r.Register(nil)
	if err == nil {
		t.Fatal("Register(nil) should fail")
	}
}

// TestArtifactSourceRegistry_Register_RejectsEmptyName — a fake
// whose Name() returns "" can't be resolved meaningfully; reject
// at Register time.
func TestArtifactSourceRegistry_Register_RejectsEmptyName(t *testing.T) {
	r := NewArtifactSourceRegistry()
	err := r.Register(&fakeSource{name: ""})
	if err == nil {
		t.Fatal("Register with empty Name should fail")
	}
}

// TestArtifactSourceRegistry_Resolve_Unknown — unknown SourceType
// returns (nil, false); the worker treats this as
// "unsupported source type".
func TestArtifactSourceRegistry_Resolve_Unknown(t *testing.T) {
	r := NewArtifactSourceRegistry()
	got, ok := r.Resolve("not-registered")
	if ok {
		t.Fatal("Resolve for unregistered source should return ok=false")
	}
	if got != nil {
		t.Fatal("Resolve for unregistered source should return nil")
	}
}

// TestArtifactSourceRegistry_Names_Sorted — assert deterministic
// order across multiple registrations; operators rely on this for
// the boot-time "registered sources" log line.
func TestArtifactSourceRegistry_Names_Sorted(t *testing.T) {
	r := NewArtifactSourceRegistry()
	// Insert in non-alphabetical order so a non-sorting impl
	// would surface in the test.
	order := []models.UploadJobSource{
		models.UploadJobSourceVeloxArtifact,
		models.UploadJobSourceAuthenticatedDrive,
		models.UploadJobSourcePublicDrive,
	}
	for _, n := range order {
		if err := r.Register(&fakeSource{name: n}); err != nil {
			t.Fatalf("Register %s: %v", n, err)
		}
	}
	names := r.Names()
	want := []string{
		string(models.UploadJobSourceAuthenticatedDrive),
		string(models.UploadJobSourcePublicDrive),
		string(models.UploadJobSourceVeloxArtifact),
	}
	got := make([]string, len(names))
	for i, n := range names {
		got[i] = string(n)
	}
	if !stringSliceEqual(got, want) {
		t.Fatalf("Names() order = %v; want %v", got, want)
	}
}

// TestArtifactSourceRegistry_ConcurrentResolve — saturate Resolve
// with concurrent readers from N goroutines; assert the map is
// safe under sync.RWMutex's read lock and returns the same source.
// Uses atomic.Int32 to count "not the expected source" anomalies
// without needing a sentinel error helper.
func TestArtifactSourceRegistry_ConcurrentResolve(t *testing.T) {
	r := NewArtifactSourceRegistry()
	src := &fakeSource{name: models.UploadJobSourceVeloxArtifact}
	if err := r.Register(src); err != nil {
		t.Fatalf("Register: %v", err)
	}
	const goroutines = 64
	var wg sync.WaitGroup
	var anomalies atomic.Int32
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, ok := r.Resolve(models.UploadJobSourceVeloxArtifact)
			if !ok || got != src {
				anomalies.Add(1)
			}
		}()
	}
	wg.Wait()
	if n := anomalies.Load(); n != 0 {
		t.Fatalf("concurrent Resolve produced %d anomalies (want 0)", n)
	}
}

// stringSliceEqual is an ordered comparison helper. Not in stdlib.
func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
