package worker

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// sha256HexOf returns the lowercase hex SHA-256 of data, used to
// synthesise expected SHA values across these tests.
func sha256HexOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// nopBody is the in-test io.ReadCloser fed to NewArtifactVerifyReader
// so each test owns its streaming source. Reads return all bytes on
// the first call; subsequent calls return io.EOF.
type nopBody struct {
	data []byte
	pos  int
	done bool
}

func (b *nopBody) Read(p []byte) (int, error) {
	if b.done || b.pos >= len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.pos:])
	b.pos += n
	if b.pos >= len(b.data) {
		b.done = true
	}
	return n, nil
}

func (b *nopBody) Close() error { return nil }

// happyPolicy is a convenience: a passing ArtifactVerificationPolicy
// matching the supplied payload — used by the happy-path tests.
func happyPolicy(payload []byte, requireSHA bool) models.ArtifactVerificationPolicy {
	return models.ArtifactVerificationPolicy{
		ExpectedSize:   int64(len(payload)),
		ExpectedSHA256: sha256HexOf(payload),
		ExpectedMIME:   "video/mp4",
		RequireSHA:     requireSHA,
	}
}

// TestArtifactVerify_Happy_SHAAndSizeMatch — both checks fire
// (RequireSHA=true). This is the prior veloxVerifyReader happy
// path; the new API uses the policy struct instead of Verify() args.
func TestArtifactVerify_Happy_SHAAndSizeMatch(t *testing.T) {
	payload := []byte("hello-authentic-artifact")
	body := &nopBody{data: payload}
	vr, err := NewArtifactVerifyReader(body, happyPolicy(payload, true))
	if err != nil {
		t.Fatalf("NewArtifactVerifyReader: %v", err)
	}
	defer vr.Close()
	got, err := io.ReadAll(vr)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("payload = %q; want %q", string(got), string(payload))
	}
	if err := vr.Verify(); err != nil {
		t.Fatalf("Verify(happy): %v", err)
	}
	if vr.ActualSHA256Hex() != sha256HexOf(payload) {
		t.Errorf("ActualSHA256Hex = %q; want %q", vr.ActualSHA256Hex(), sha256HexOf(payload))
	}
	if vr.ActualSize() != int64(len(payload)) {
		t.Errorf("ActualSize = %d; want %d", vr.ActualSize(), len(payload))
	}
}

// TestArtifactVerify_OverSize — body longer than ExpectedSize+1;
// Read returns io.EOF on the over-flow Read + Verify surfaces
// ARTIFACT_SIZE_MISMATCH permanent error.
func TestArtifactVerify_OverSize(t *testing.T) {
	payload := []byte(strings.Repeat("a", 1024))
	body := &nopBody{data: payload}
	vr, err := NewArtifactVerifyReader(body, models.ArtifactVerificationPolicy{
		ExpectedSize: 100, // cap 100, payload 1024
	})
	if err != nil {
		t.Fatalf("NewArtifactVerifyReader: %v", err)
	}
	defer vr.Close()
	got, _ := io.ReadAll(vr)
	if int64(len(got)) > 101 {
		t.Fatalf("over-size stream yielded >limit+1 bytes; got %d", len(got))
	}
	if err := vr.Verify(); err == nil {
		t.Fatal("Verify should fail on over-size drain")
	} else if !errors.Is(err, ErrPermanent) {
		t.Fatalf("expected ErrPermanent; got %v", err)
	}
}

// TestArtifactVerify_ShortBody — body shorter than ExpectedSize;
// drains fully then Verify detects size mismatch.
func TestArtifactVerify_ShortBody(t *testing.T) {
	payload := []byte("short")
	body := &nopBody{data: payload}
	vr, err := NewArtifactVerifyReader(body, models.ArtifactVerificationPolicy{
		ExpectedSize:   1024, // expected longer than actual
		ExpectedSHA256: sha256HexOf(payload),
		RequireSHA:     true,
	})
	if err != nil {
		t.Fatalf("NewArtifactVerifyReader: %v", err)
	}
	_, _ = io.ReadAll(vr)
	if err := vr.Verify(); err == nil {
		t.Fatal("Verify should fail on short body")
	} else if !errors.Is(err, ErrPermanent) {
		t.Fatalf("expected ErrPermanent; got %v", err)
	}
}

// TestArtifactVerify_WrongSHA — body matches size but SHA wrong;
// RequireSHA=true so the verifier rejects. This is the FAIL path
// the user's spec named: a Drive file whose streamed bytes don't
// match the upstream-declared sha256Checksum; the media_asset
// MUST fail and never reach ready.
func TestArtifactVerify_WrongSHA(t *testing.T) {
	payload := []byte("hello-artifact")
	body := &nopBody{data: payload}
	vr, err := NewArtifactVerifyReader(body, models.ArtifactVerificationPolicy{
		ExpectedSize:   int64(len(payload)),
		ExpectedSHA256: strings.Repeat("0", 64), // wrong SHA
		RequireSHA:     true,
	})
	if err != nil {
		t.Fatalf("NewArtifactVerifyReader: %v", err)
	}
	_, _ = io.ReadAll(vr)
	if err := vr.Verify(); err == nil {
		t.Fatal("Verify should fail on wrong SHA")
	} else if !errors.Is(err, ErrPermanent) {
		t.Fatalf("expected ErrPermanent; got %v", err)
	}
}

// TestArtifactVerify_RequireSHAFalse_NoSHAStillAccepts — Task 4/10
// acceptance bar: Drive file without sha256Checksum in metadata
// (RequireSHA=false) MUST still pass when size matches. The
// verifier computes + persists the local SHA via ActualSHA256Hex().
func TestArtifactVerify_RequireSHAFalse_NoSHAStillAccepts(t *testing.T) {
	payload := []byte("drive-file-without-declared-sha")
	body := &nopBody{data: payload}
	vr, err := NewArtifactVerifyReader(body, models.ArtifactVerificationPolicy{
		ExpectedSize: int64(len(payload)),
		ExpectedMIME: "video/mp4",
		RequireSHA:   false, // Drive didn't surface sha256Checksum
	})
	if err != nil {
		t.Fatalf("NewArtifactVerifyReader: %v", err)
	}
	defer vr.Close()
	got, err := io.ReadAll(vr)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("payload = %q; want %q", string(got), string(payload))
	}
	if err := vr.Verify(); err != nil {
		t.Fatalf("Verify(RequireSHA=false, size matches): %v", err)
	}
	// Crucial: local SHA MUST be persisted downstream per the
	// acceptance bar.
	if vr.ActualSHA256Hex() != sha256HexOf(payload) {
		t.Errorf("local SHA must still be computed for persistence; got %q, want %q",
			vr.ActualSHA256Hex(), sha256HexOf(payload))
	}
}

// TestArtifactVerify_RequireSHAFalse_DriveSizeMismatchStillBlocked —
// Task 4/10 acceptance bar: even when SHA comparison is skipped
// (RequireSHA=false), the SIZE comparison still fires. A Drive
// file whose streamed bytes < declared Size must fail loud.
func TestArtifactVerify_RequireSHAFalse_DriveSizeMismatchStillBlocked(t *testing.T) {
	payload := []byte("tiny")
	body := &nopBody{data: payload}
	vr, err := NewArtifactVerifyReader(body, models.ArtifactVerificationPolicy{
		ExpectedSize: int64(len(payload)) + 100, // declared bigger than actual
		ExpectedMIME: "video/mp4",
		RequireSHA:   false,
	})
	if err != nil {
		t.Fatalf("NewArtifactVerifyReader: %v", err)
	}
	_, _ = io.ReadAll(vr)
	if err := vr.Verify(); err == nil {
		t.Fatal("Verify must fail on size mismatch even with RequireSHA=false")
	} else if !errors.Is(err, ErrPermanent) {
		t.Fatalf("expected ErrPermanent; got %v", err)
	}
}

// TestArtifactVerify_DriveWithDeclaredSHA_MismatchHardFails — the
// canonical "Drive verification is no longer a follow-up" test.
// Task 4/10 acceptance bar: Drive with declared sha256Checksum
// feeds ExpectedSHA256 + RequireSHA=true into the policy. If the
// streamed bytes don't match, Verify MUST return PermanentError so
// the caller MarkReady-fails + never publishes.
func TestArtifactVerify_DriveWithDeclaredSHA_MismatchHardFails(t *testing.T) {
	payload := []byte("the-real-bytes-from-Drive")
	declaredDriveSHA := sha256HexOf([]byte("what-Drive-said-but-actually-was-different"))
	body := &nopBody{payload, 0, false}
	vr, err := NewArtifactVerifyReader(body, models.ArtifactVerificationPolicy{
		ExpectedSize:   int64(len(payload)),
		ExpectedSHA256: declaredDriveSHA, // doesn't match actually-stored bytes
		ExpectedMIME:   "video/mp4",
		RequireSHA:     true, // Drive has sha256Checksum, so Verify must enforce
	})
	if err != nil {
		t.Fatalf("NewArtifactVerifyReader: %v", err)
	}
	_, _ = io.ReadAll(vr)
	err = vr.Verify()
	if err == nil {
		t.Fatal("Verify must fail when Drive-declared SHA mismatches streamed bytes")
	}
	if !errors.Is(err, ErrPermanent) {
		t.Fatalf("err must wrap ErrPermanent; got %v", err)
	}
	// Local SHA still computed → must not be lost.
	if vr.ActualSHA256Hex() != sha256HexOf(payload) {
		t.Errorf("local SHA must still be computed for forensics; got %q, want %q",
			vr.ActualSHA256Hex(), sha256HexOf(payload))
	}
}

// TestArtifactVerify_SizeUnknown — ExpectedSize=0 disables the cap;
// SHA still flows. Mirrors the prior veloxVerifyReader_SizeUnknown
// for parametric continuity; the underlying semantics are
// equivalent to calling Verify() against a zero-size policy.
func TestArtifactVerify_SizeUnknown(t *testing.T) {
	payload := []byte("unknown-size-artifact")
	expectedSHA := sha256HexOf(payload)
	body := &nopBody{data: payload}
	vr, err := NewArtifactVerifyReader(body, models.ArtifactVerificationPolicy{
		ExpectedSize:   0, // unknown size
		ExpectedSHA256: expectedSHA,
		RequireSHA:     true,
	})
	if err != nil {
		t.Fatalf("NewArtifactVerifyReader: %v", err)
	}
	defer vr.Close()
	got, err := io.ReadAll(vr)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("payload = %q; want %q", string(got), string(payload))
	}
	if err := vr.Verify(); err != nil {
		t.Fatalf("Verify(unknown-size with matching SHA): %v", err)
	}
}

// TestArtifactVerify_CloseIdempotent — defensive double-close (worker
// + defer) is safe; bytes are not re-counted. Mirrors the prior
// veloxVerifyReader_CloseIdempotent — same invariant.
func TestArtifactVerify_CloseIdempotent(t *testing.T) {
	payload := []byte("once-and-done")
	body := &nopBody{data: payload}
	vr, err := NewArtifactVerifyReader(body, happyPolicy(payload, true))
	if err != nil {
		t.Fatalf("NewArtifactVerifyReader: %v", err)
	}
	if _, err := io.ReadAll(vr); err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if err := vr.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := vr.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestArtifactVerify_NilBodyRejected — fail-loud constructor
// assertion: nil body returns an error so a misconfigured caller
// can't silently fall through to a zero-hash Verify.
func TestArtifactVerify_NilBodyRejected(t *testing.T) {
	_, err := NewArtifactVerifyReader(nil, models.ArtifactVerificationPolicy{})
	if err == nil {
		t.Fatal("NewArtifactVerifyReader(nil) must return an error")
	}
}

// TestIsDeliveryVerificationSkipErr — same dispatch as the prior
// revision; preserved for Velox-only peek-ordering + legacy-row
// skip-or-fail routing in upload_worker.
func TestIsDeliveryVerificationSkipErr(t *testing.T) {
	if IsDeliveryVerificationSkipErr(nil) {
		t.Fatal("nil should not be a skip")
	}
	if IsDeliveryVerificationSkipErr(errors.New("transient db")) {
		t.Fatal("generic error should not be a skip")
	}
	if !IsDeliveryVerificationSkipErr(repository.ErrExternalDeliveryNotLinked) {
		t.Fatal("ErrExternalDeliveryNotLinked should be a skip")
	}
	if !IsDeliveryVerificationSkipErr(repository.ErrExternalDeliveryNoExpectedTriple) {
		t.Fatal("ErrExternalDeliveryNoExpectedTriple should be a skip")
	}
	wrappedNotLinked := wrapSkip(repository.ErrExternalDeliveryNotLinked)
	if !IsDeliveryVerificationSkipErr(wrappedNotLinked) {
		t.Fatal("wrapped ErrExternalDeliveryNotLinked should be a skip")
	}
	wrappedNoTriple := wrapSkip(repository.ErrExternalDeliveryNoExpectedTriple)
	if !IsDeliveryVerificationSkipErr(wrappedNoTriple) {
		t.Fatal("wrapped ErrExternalDeliveryNoExpectedTriple should be a skip")
	}
}

// wrapSkip wraps via errors.New + Join so errors.Is walks the chain.
func wrapSkip(err error) error {
	return errors.Join(errors.New("outer"), err)
}
