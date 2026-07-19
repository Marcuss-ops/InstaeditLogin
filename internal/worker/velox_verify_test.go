package worker

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// sha256HexOf returns the lowercase hex SHA-256 of data, used to
// synthesise the expected SHA value in tests.
func sha256HexOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// nopBody is the in-test io.ReadCloser fed to NewVeloxVerifyReader
// so each test owns its streaming source. Reads return all bytes
// on the first call; subsequent calls return io.EOF.
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

// TestVeloxVerifyReader_Happy — N bytes in, N bytes out, SHA matches,
// size matches; Verify returns nil.
func TestVeloxVerifyReader_Happy(t *testing.T) {
	payload := []byte("hello-authentic-artifact")
	expectedSHA := sha256HexOf(payload)
	body := &nopBody{data: payload}
	vr := NewVeloxVerifyReader(body, int64(len(payload)))
	defer vr.Close()
	got, err := io.ReadAll(vr)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("payload = %q; want %q", string(got), string(payload))
	}
	if err := vr.Verify(int64(len(payload)), expectedSHA); err != nil {
		t.Fatalf("Verify(happy): %v", err)
	}
}

// TestVeloxVerifyReader_OverSize — body longer than expectedSize+1;
// Read returns io.EOF on the over-flow Read + Verify surfaces
// ARTIFACT_SIZE_MISMATCH permanent error.
func TestVeloxVerifyReader_OverSize(t *testing.T) {
	payload := []byte(strings.Repeat("a", 1024))
	body := &nopBody{data: payload}
	vr := NewVeloxVerifyReader(body, 100) // cap 100, payload 1024
	defer vr.Close()
	got, _ := io.ReadAll(vr)
	if len(got) > 101 {
		t.Fatalf("over-size stream yielded >limit+1 bytes; got %d", len(got))
	}
	if err := vr.Verify(100, sha256HexOf(payload[:100])); err == nil {
		t.Fatal("Verify should fail on over-size drain")
	} else if !errors.Is(err, ErrPermanent) {
		t.Fatalf("expected ErrPermanent; got %v", err)
	}
}

// TestVeloxVerifyReader_ShortBody — body shorter than expectedSize;
// drains fully then Verify detects size mismatch.
func TestVeloxVerifyReader_ShortBody(t *testing.T) {
	payload := []byte("short")
	body := &nopBody{data: payload}
	vr := NewVeloxVerifyReader(body, 1024) // expected longer than actual
	defer vr.Close()
	_, _ = io.ReadAll(vr)
	if err := vr.Verify(1024, sha256HexOf(payload)); err == nil {
		t.Fatal("Verify should fail on short body")
	} else if !errors.Is(err, ErrPermanent) {
		t.Fatalf("expected ErrPermanent; got %v", err)
	}
}

// TestVeloxVerifyReader_WrongSHA — body matches size but SHA wrong;
// Verify surfaces ARTIFACT_SHA256_MISMATCH.
func TestVeloxVerifyReader_WrongSHA(t *testing.T) {
	payload := []byte("hello-artifact")
	body := &nopBody{data: payload}
	vr := NewVeloxVerifyReader(body, int64(len(payload)))
	defer vr.Close()
	_, _ = io.ReadAll(vr)
	wrongSHA := strings.Repeat("0", 64)
	if err := vr.Verify(int64(len(payload)), wrongSHA); err == nil {
		t.Fatal("Verify should fail on wrong SHA")
	} else if !errors.Is(err, ErrPermanent) {
		t.Fatalf("expected ErrPermanent; got %v", err)
	}
}

// TestVeloxVerifyReader_SizeUnknown — limit=0 disables the cap;
// SHA still flows but size verification is a no-op downstream.
func TestVeloxVerifyReader_SizeUnknown(t *testing.T) {
	payload := []byte("unknown-size-artifact")
	expectedSHA := sha256HexOf(payload)
	body := &nopBody{data: payload}
	vr := NewVeloxVerifyReader(body, 0) // unknown size
	defer vr.Close()
	got, err := io.ReadAll(vr)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("payload = %q; want %q", string(got), string(payload))
	}
	if err := vr.Verify(0, expectedSHA); err != nil {
		t.Fatalf("Verify(unknown-size with matching SHA): %v", err)
	}
}

// TestVeloxVerifyReader_CloseIdempotent — defensive double-close
// (worker + defer) is safe; bytes are not re-counted.
func TestVeloxVerifyReader_CloseIdempotent(t *testing.T) {
	payload := []byte("once-and-done")
	body := &nopBody{data: payload}
	vr := NewVeloxVerifyReader(body, int64(len(payload)))
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

// TestIsDeliveryVerificationSkipErr — exercises the dispatch that
// processIngestJob uses to decide best-effort-no-verify.
//
// We import internal/repository directly so the sentinel identity
// is canonical; the test package already depends on repository
// transitively via UploadJobStore so the import is a one-liner.
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
	// Wrapped sentinels (e.g. via fmt.Errorf("...: %w", sentinel))
	// must also dispatch correctly thanks to errors.Is chain walking.
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
// (Stdlib errors.Join is Go 1.20+; the project go.mod already
// requires 1.21, so the usage is safe.)
func wrapSkip(err error) error {
	return errors.Join(errors.New("outer"), err)
}
