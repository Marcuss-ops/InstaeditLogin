package sources

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// fakeSource is the in-memory ArtifactSource fake used by registry
// tests. Name() returns whatever was passed in. Inspect/Open are
// no-op implementations that return ErrInspectNotImplemented
// and a trivial closed stream — registry tests don't exercise
// the inspect/open surface (those live in velox_source_test.go).
type fakeSource struct {
	name models.UploadJobSource
}

func (f *fakeSource) Name() models.UploadJobSource { return f.name }

func (f *fakeSource) Inspect(_ context.Context, _ *models.UploadJob) (*SourceMetadata, error) {
	return nil, ErrInspectNotImplemented
}

func (f *fakeSource) Open(_ context.Context, _ *models.UploadJob, _ int64, _ string) (ArtifactStream, error) {
	return &emptyArtifactStream{}, nil
}

// emptyArtifactStream is the no-op ArtifactStream that Open
// returns in fakeSource. Result returns a sentinel err to confirm
// tests didn't accidentally exercise the SourceMetadata path
// through fakeSource.
type emptyArtifactStream struct{}

func (e *emptyArtifactStream) Read(p []byte) (int, error) { return 0, io.EOF }
func (e *emptyArtifactStream) Close() error                { return nil }
func (e *emptyArtifactStream) Result() (int64, string, error) {
	return 0, "", errors.New("emptyArtifactStream: fake — do not drain")
}

// TestArtifactSourceRegistry_Register_Happy pins the boot-time
// wiring sequence: name + source → returns nil.
func TestArtifactSourceRegistry_Register_Happy(t *testing.T) {
	r := NewArtifactSourceRegistry()
	if err := r.Register(&fakeSource{name: models.UploadJobSourceVeloxArtifact}); err != nil {
		t.Fatalf("Register happy: %v", err)
	}
	src, ok := r.Resolve(models.UploadJobSourceVeloxArtifact)
	if !ok || src == nil {
		t.Fatalf("Resolve after Register: got (%v, ok=%v)", src, ok)
	}
	if src.Name() != models.UploadJobSourceVeloxArtifact {
		t.Errorf("Name match: want %s, got %s", models.UploadJobSourceVeloxArtifact, src.Name())
	}
}

// TestArtifactSourceRegistry_Register_NilSourceRejected covers
// the caller-bug path: passing nil instead of a constructed
// source. Fail-loud at boot rather than silent nil at resolve time.
func TestArtifactSourceRegistry_Register_NilSourceRejected(t *testing.T) {
	r := NewArtifactSourceRegistry()
	err := r.Register(nil)
	if err == nil {
		t.Fatal("Register(nil): want error, got nil")
	}
}

// TestArtifactSourceRegistry_Register_DuplicateNameRejected pins
// the cross-source isolation invariant: two sources claiming the
// SAME name would silently shadow. Boot MUST fail-loud.
func TestArtifactSourceRegistry_Register_DuplicateNameRejected(t *testing.T) {
	r := NewArtifactSourceRegistry()
	if err := r.Register(&fakeSource{name: models.UploadJobSourceVeloxArtifact}); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	err := r.Register(&fakeSource{name: models.UploadJobSourceVeloxArtifact})
	if err == nil {
		t.Fatal("duplicate Register: want error, got nil")
	}
}

// TestArtifactSourceRegistry_Resolve_UnknownReturnsFalse pins the
// graceful no-op fallback. The worker's legacy switch (Drive
// public/auth) calls Resolve and uses ok=false to fall through.
func TestArtifactSourceRegistry_Resolve_UnknownReturnsFalse(t *testing.T) {
	r := NewArtifactSourceRegistry()
	src, ok := r.Resolve(models.UploadJobSourceAuthenticatedDrive)
	if ok {
		t.Errorf("Resolve(unknown): want ok=false, got true")
	}
	if src != nil {
		t.Errorf("Resolve(unknown) must return nil source, got %v", src)
	}
}

// TestArtifactSourceRegistry_Resolve_ConcurrentSafety runs N
// goroutines simultaneously resolving the same source. The
// sync.RWMutex on Registry.make it safe; the test fails if
// there's a data race (run with `go test -race` in CI).
func TestArtifactSourceRegistry_Resolve_ConcurrentSafety(t *testing.T) {
	r := NewArtifactSourceRegistry()
	if err := r.Register(&fakeSource{name: models.UploadJobSourceVeloxArtifact}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	const goroutines = 32
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				src, ok := r.Resolve(models.UploadJobSourceVeloxArtifact)
				if !ok || src == nil {
					t.Errorf("Resolve: got (%v, ok=%v)", src, ok)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// TestArtifactSourceRegistry_Names_DeterministicOrder pins the
// operator-friendly Names() ordering. We insert in mixed order
// and verify sorted lexicographic output.
func TestArtifactSourceRegistry_Names_DeterministicOrder(t *testing.T) {
	r := NewArtifactSourceRegistry()
	for _, name := range []models.UploadJobSource{
		models.UploadJobSourceVeloxArtifact, // "velox_artifact"
		models.UploadJobSourceAuthenticatedDrive, // "authenticated_drive"
		models.UploadJobSourcePublicDrive, // "public_drive"
	} {
		if err := r.Register(&fakeSource{name: name}); err != nil {
			t.Fatalf("Register(%s): %v", name, err)
		}
	}
	got := r.Names()
	want := []models.UploadJobSource{
		models.UploadJobSourceAuthenticatedDrive,
		models.UploadJobSourcePublicDrive,
		models.UploadJobSourceVeloxArtifact,
	}
	if len(got) != len(want) {
		t.Fatalf("Names len: want %d, got %d (%v)", len(want), len(got), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("Names[%d]: want %s, got %s", i, want[i], got[i])
		}
	}
}
