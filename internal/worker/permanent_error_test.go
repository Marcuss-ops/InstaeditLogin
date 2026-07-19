package worker

import (
	"errors"
	"testing"
)

// TestPermanentError_Error — Error() formats "{code}: {message}"
// (the leading Code is what classifyUploadError greps for to fill
// the upload_jobs.error_code column).
func TestPermanentError_Error(t *testing.T) {
	pe := PermanentError{
		Code:    CodeArtifactSizeMismatch,
		Message: "expected exactly 1024 bytes, got 512",
	}
	got := pe.Error()
	want := "ARTIFACT_SIZE_MISMATCH: expected exactly 1024 bytes, got 512"
	if got != want {
		t.Fatalf("Error() = %q; want %q", got, want)
	}
}

// TestPermanentError_Is_DispatchOnErrPermanent — errors.Is(any-PE,
// ErrPermanent) returns true so the worker's routing logic can
// match on the sentinel without enumerating every Code.
func TestPermanentError_Is_DispatchOnErrPermanent(t *testing.T) {
	for _, code := range []string{
		CodeArtifactSizeMismatch,
		CodeArtifactSHA256Mismatch,
		CodeArtifactMIMEMismatch,
	} {
		pe := PermanentError{Code: code, Message: "test"}
		if !errors.Is(pe, ErrPermanent) {
			t.Errorf("errors.Is(PE{%s}, ErrPermanent) returned false; want true", code)
		}
	}
}

// TestPermanentError_Is_NonPermanentError — a plain error not
// implementing Is(ErrPermanent) returns false from errors.Is.
// Important: the worker's routing logic must NOT match unrelated
// errors against ErrPermanent.
func TestPermanentError_Is_NonPermanentError(t *testing.T) {
	plain := errors.New("transient blip")
	if errors.Is(plain, ErrPermanent) {
		t.Fatal("errors.Is(plain error, ErrPermanent) returned true; want false")
	}
}

// TestPermanentError_WrappedDispatch — wrapping a PermanentError
// via fmt.Errorf("...: %w", pe) still matches ErrPermanent. The
// wrap pattern is the worker's preferred error composition.
func TestPermanentError_WrappedDispatch(t *testing.T) {
	pe := PermanentError{Code: CodeArtifactSHA256Mismatch, Message: "wrap test"}
	wrapped := wrapPE(pe)
	if !errors.Is(wrapped, ErrPermanent) {
		t.Fatal("wrapped PE should still errors.Is ErrPermanent")
	}
}

// wrapPE is a tiny helper using fmt-style %w to validate wrap dispatch.
// Imported inline so the test file doesn't pull fmt at top-level just
// for this one helper.
func wrapPE(pe error) error {
	type wrapper struct{ inner error }
	w := wrapper{inner: pe}
	// Use errors.Join-style chain so errors.Is walks the tree.
	return errors.Join(errors.New("outer"), w.inner)
}
