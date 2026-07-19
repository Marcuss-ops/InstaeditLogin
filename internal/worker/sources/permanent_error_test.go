package sources

import (
	"errors"
	"testing"
)

// TestPermanentError_ErrorFormat pins the "{Code}: {Message}"
// format used in slog.Error log lines. classifyUploadError greps
// for the Code prefix to populate upload_jobs.error_code, so
// changing this format would break the dashboard filter pipeline.
func TestPermanentError_ErrorFormat(t *testing.T) {
	e := PermanentError{
		Code:    CodeArtifactSizeMismatch,
		Message: "expected 100 bytes, got 0",
	}
	want := "ARTIFACT_SIZE_MISMATCH: expected 100 bytes, got 0"
	if got := e.Error(); got != want {
		t.Errorf("Error(): want %q, got %q", want, got)
	}
}

// TestPermanentError_IsDispatch verifies errors.Is(err, ErrPermanent)
// returns true for any PermanentError regardless of Code variant.
// The worker's failure routing uses:
//
//	if errors.Is(err, sources.ErrPermanent) {
//	    // route to MarkDeadLetter
//	}
//
// without enumerating codes.
func TestPermanentError_IsDispatch(t *testing.T) {
	cases := []PermanentError{
		{Code: CodeArtifactSizeMismatch, Message: "size"},
		{Code: CodeArtifactSHA256Mismatch, Message: "sha"},
		{Code: CodeArtifactMIMEMismatch, Message: "mime"},
		{Code: "ARTIFACT_OTHER_THING", Message: "future code"},
	}
	for _, pe := range cases {
		if !errors.Is(pe, ErrPermanent) {
			t.Errorf("errors.Is(%q, ErrPermanent): want true, got false", pe.Error())
		}
	}
}

// TestPermanentError_NonDispatch verifies a non-PermanentError does
// NOT match ErrPermanent. Otherwise the worker would route transient
// errors to the dead-letter bucket instead of the retry bucket.
func TestPermanentError_NonDispatch(t *testing.T) {
	plain := errors.New("transient network blip")
	if errors.Is(plain, ErrPermanent) {
		t.Errorf("plain error must NOT match ErrPermanent")
	}
	// PermanentError wrapped in fmt.Errorf("wrap: %w", pe) should
	// STILL match (errors.Is walks the chain).
	wrapped := errorsWrap("wrap", PermanentError{Code: CodeArtifactSizeMismatch, Message: "size"})
	if !errors.Is(wrapped, ErrPermanent) {
		t.Errorf("wrapped PermanentError should still match ErrPermanent")
	}
}

// TestPermanentError_CodeAccessor confirms the Code field is set
// correctly — used by classifyUploadError to grep for the prefix
// in the error.Error() string.
func TestPermanentError_CodeAccessor(t *testing.T) {
	e := PermanentError{Code: CodeArtifactSHA256Mismatch, Message: "diff"}
	if e.Code != CodeArtifactSHA256Mismatch {
		t.Errorf("Code accessor: want %q, got %q", CodeArtifactSHA256Mismatch, e.Code)
	}
}

// TestPermanentError_AsTypeAssertion verifies errors.As can extract
// the typed PermanentError from a wrapped chain (operators examining
// a high-level error can introspect Code without re-parsing the
// message string).
func TestPermanentError_AsTypeAssertion(t *testing.T) {
	wrapped := errorsWrap("first wrap",
		errorsWrap("second wrap",
			PermanentError{Code: CodeArtifactMIMEMismatch, Message: "video/mp4 not in allowlist"},
		),
	)
	var got PermanentError
	if !errors.As(wrapped, &got) {
		t.Fatal("errors.As should unwrap to PermanentError")
	}
	if got.Code != CodeArtifactMIMEMismatch {
		t.Errorf("Code: want %q, got %q", CodeArtifactMIMEMismatch, got.Code)
	}
}

// errorsWrap is the local stand-in for fmt.Errorf("...%w...", err)
// — kept inline so we don't drag fmt into the test imports (the
// test file is a leaf; nothing else needs it).
func errorsWrap(msg string, err error) error {
	return &wrapped{msg: msg, inner: err}
}

type wrapped struct {
	msg   string
	inner error
}

func (w *wrapped) Error() string { return w.msg + ": " + w.inner.Error() }
func (w *wrapped) Unwrap() error { return w.inner }
