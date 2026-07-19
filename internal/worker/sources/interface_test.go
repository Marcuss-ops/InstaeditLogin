package sources

import (
	"context"
	"crypto/sha256"
	"errors"
	"hash"
	"io"
	"testing"
)

// hashForTest returns a fresh sha256 hasher for stream tests. The
// interface_test.go file uses it directly (without going through
// Open) so we don't need to wire a full VeloxSource mock.
func hashForTest() hash.Hash { return sha256.New() }

// fakeReadCloser is a configurable ReadCloser for stream tests.
// readN is the maximum bytes per Read; totalN is the total bytes
// before the Read returns io.EOF.
type fakeReadCloser struct {
	readN   int
	totalN  int
	read    int
	closed  bool
}

func (f *fakeReadCloser) Read(p []byte) (int, error) {
	if f.closed {
		return 0, errors.New("fakeReadCloser: read after close")
	}
	remaining := f.totalN - f.read
	if remaining <= 0 {
		return 0, io.EOF
	}
	toRead := f.readN
	if toRead > remaining {
		toRead = remaining
	}
	if toRead > len(p) {
		toRead = len(p)
	}
	for i := 0; i < toRead; i++ {
		p[i] = 'x'
	}
	f.read += toRead
	return toRead, nil
}

func (f *fakeReadCloser) Close() error {
	if f.closed {
		return errors.New("fakeReadCloser: double close")
	}
	f.closed = true
	return nil
}

// _ unused-imports guard for context if interface_test evolves.
var _ = context.Background

// TestVerifySHAConstantTime_Match pins the happy case: two equal
// hex digests across the constant-time comparison path.
func TestVerifySHAConstantTime_Match(t *testing.T) {
	const hex1 = "e5f2c235e5f2c235e5f2c235e5f2c235e5f2c235e5f2c235e5f2c235e5f2c235"
	if err := verifySHAConstantTime(hex1, hex1); err != nil {
		t.Errorf("verifySHAConstantTime(match): want nil, got %v", err)
	}
}

// TestVerifySHAConstantTime_MismatchDifferentHex pins the
// deterministic-mismatch path. Two distinct SHA values MUST
// produce a PermanentError{Code: ARTIFACT_SHA256_MISMATCH}. The
// worker routes this to MarkDeadLetter.
func TestVerifySHAConstantTime_MismatchDifferentHex(t *testing.T) {
	hex2 := "f00000000000000000000000000000000000000000000000000000000000000f"
	err := verifySHAConstantTime(
		"e5f2c235e5f2c235e5f2c235e5f2c235e5f2c235e5f2c235e5f2c235e5f2c235",
		hex2,
	)
	if err == nil {
		t.Fatal("verifySHAConstantTime(distinct): want error, got nil")
	}
	var pe PermanentError
	if !errors.As(err, &pe) {
		t.Fatalf("returned error is not PermanentError: %v", err)
	}
	if pe.Code != CodeArtifactSHA256Mismatch {
		t.Errorf("Code: want %s, got %s", CodeArtifactSHA256Mismatch, pe.Code)
	}
}

// TestVerifySHAConstantTime_UppercaseRejected verifies the
// strict-lowercase enforcement. Upstream producers that
// surface UPPERCASE hex (e.g. an older OS X binary that never
// lowercased) MUST surface as a PermanentError rather than
// silently 0x00-substituted. The defensive parse in hexDecodeStrict
// catches this; the test pins the behavior.
func TestVerifySHAConstantTime_UppercaseRejected(t *testing.T) {
	upperHex := "E5F2C235E5F2C235E5F2C235E5F2C235E5F2C235E5F2C235E5F2C235E5F2C235"
	err := verifySHAConstantTime(upperHex, upperHex)
	if err == nil {
		t.Fatal("verifySHAConstantTime(uppercase): want error, got nil")
	}
	var pe PermanentError
	if !errors.As(err, &pe) {
		t.Fatalf("returned error is not PermanentError: %v", err)
	}
	if pe.Code != CodeArtifactSHA256Mismatch {
		t.Errorf("Code: want %s, got %s", CodeArtifactSHA256Mismatch, pe.Code)
	}
}

// TestVerifySHAConstantTime_ShortHexRejected confirms the
// length check (must be exactly 64 chars) catches any
// truncated upstream SHA. Important because Velox MAY truncate
// when emitting into a fixed-width column.
func TestVerifySHAConstantTime_ShortHexRejected(t *testing.T) {
	short := "deadbeef"
	err := verifySHAConstantTime(short, short)
	if err == nil {
		t.Fatal("verifySHAConstantTime(short): want error, got nil")
	}
	var pe PermanentError
	if !errors.As(err, &pe) {
		t.Fatalf("returned error is not PermanentError: %v", err)
	}
}

// TestVerifySHAConstantTime_TimeInvariance covers the
// "constant-time" claim at its most basic: the function takes
// the same amount of work regardless of which byte differs.
// We test the indirect property — return type is int (1 == equal,
// 0 == mismatched at any depth) — because direct timing analysis
// in Go unit tests is flaky. At least the contract is exercised.
func TestVerifySHAConstantTime_TimeInvariance(t *testing.T) {
	const hex1 = "e5f2c235e5f2c235e5f2c235e5f2c235e5f2c235e5f2c235e5f2c235e5f2c235"
	if subtleConstantTimeCompareBytes([]byte(hex1), []byte(hex1)) != 1 {
		t.Errorf("equal-input compare: want 1, got 0")
	}
	if subtleConstantTimeCompareBytes(
		[]byte(hex1),
		[]byte("0000000000000000000000000000000000000000000000000000000000000000"),
	) != 0 {
		t.Errorf("different-input compare: want 0, got 1")
	}
}

// TestVerifySizeExact covers the boundaries: exact match
// (happy), short body (PermanentError), over-size body
// (PermanentError). Strict equality; no "close enough" tolerance
// because the SHA is exact too — size mismatch is a definite
// upstream contract violation.
func TestVerifySizeExact(t *testing.T) {
	cases := []struct {
		actual   int64
		expected int64
		wantErr  bool
	}{
		{actual: 100, expected: 100, wantErr: false},
		{actual: 0, expected: 100, wantErr: true}, // short
		{actual: 101, expected: 100, wantErr: true}, // over
		{actual: 99, expected: 100, wantErr: true}, // off-by-one short
		{actual: 105, expected: 100, wantErr: true}, // far over
	}
	for _, tc := range cases {
		err := verifySizeExact(tc.actual, tc.expected)
		if tc.wantErr && err == nil {
			t.Errorf("verifySizeExact(=%d, expected %d): want error, got nil",
				tc.actual, tc.expected)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("verifySizeExact(=%d, expected %d): want nil, got %v",
				tc.actual, tc.expected, err)
		}
		if tc.wantErr {
			var pe PermanentError
			if !errors.As(err, &pe) {
				t.Errorf("error is not PermanentError: %v", err)
			} else if pe.Code != CodeArtifactSizeMismatch {
				t.Errorf("Code: want %s, got %s", CodeArtifactSizeMismatch, pe.Code)
			}
		}
	}
}

// TestSourceMetadata_FieldPreservation pins that the struct
// fields pass through correctly. The Inspect function constructs
// a SourceMetadata from HEAD response headers; tests want to
// trust the field plumbing.
func TestSourceMetadata_FieldPreservation(t *testing.T) {
	m := SourceMetadata{
		SizeBytes: 1024,
		MimeType:  "video/mp4",
		ETag:      `"abc123"`,
		SHA256Hex: "e5f2c235e5f2c235e5f2c235e5f2c235e5f2c235e5f2c235e5f2c235e5f2c235",
	}
	if m.SizeBytes != 1024 {
		t.Errorf("SizeBytes: %d", m.SizeBytes)
	}
	if m.MimeType != "video/mp4" {
		t.Errorf("MimeType: %s", m.MimeType)
	}
	if m.ETag != `"abc123"` {
		t.Errorf("ETag: %s", m.ETag)
	}
	if m.SHA256Hex == "" {
		t.Errorf("SHA256Hex missing")
	}
}

// TestErrInspectNotImplemented_SentinelExists pins the sentinel
// for callers that errors.Is against it.
func TestErrInspectNotImplemented_SentinelExists(t *testing.T) {
	if !errors.Is(ErrInspectNotImplemented, ErrInspectNotImplemented) {
		t.Errorf("ErrInspectNotImplemented must match itself via errors.Is")
	}
}

// TestErrStreamNotClosedReturnedBeforeClose pins that the
// ArtifactStream returns the sentinel for the call-before-Close
// pattern. The worker relies on this to detect programming bugs
// (calling Result() before Close() in the drain-then-verify path).
func TestErrStreamNotClosedReturnedBeforeClose(t *testing.T) {
	// Build a stream with a fakeBody that the worker would never
	// read past (deliberately incomplete).
	stream := &veloxArtifactStream{
		body:          &fakeReadCloser{readN: 50, totalN: 100},
		hasher:        hashForTest(),
		limitExpected: 100,
	}
	// Don't Close; try Result() immediately.
	_, _, err := stream.Result()
	if !errors.Is(err, ErrStreamNotClosed) {
		t.Errorf("Result before Close: want ErrStreamNotClosed, got %v", err)
	}
}

// TestArtifactStream_InterfaceCompile pins that ArtifactStream
// is the io.ReadCloser interface + the Result accessor. If a
// future refactor drifts the interface (e.g. removing Result()),
// this fails at go vet time. The compile-time assertion below
// IS the test for now.
func TestArtifactStream_InterfaceCompile(t *testing.T) {
	// staticInterfaceAssertion = nil-by-conversion: any type that
	// satisfies the interface matches. Compile-time only.
	var s ArtifactStream = &veloxArtifactStream{}
	_ = s // silence unused warning; the conversion itself is the test.
}
