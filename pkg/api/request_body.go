package api

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// maxIdempotencyBodyBytes bounds JSON control envelopes that participate in
// idempotency hashing. Upload bytes never pass through this helper: media files
// use direct S3 PUTs or the Drive-to-S3 streaming path.
const maxIdempotencyBodyBytes int64 = 1 << 20

// idempotencyReadBody reads a bounded control envelope once, hashes it later,
// and rewinds the request for callers that still need a decoder. The original
// request body is always closed, including when MaxBytesReader rejects it.
func idempotencyReadBody(w http.ResponseWriter, req *http.Request) ([]byte, error) {
	if req.Body == nil {
		return nil, nil
	}
	originalBody := req.Body
	defer originalBody.Close()

	limited := http.MaxBytesReader(w, originalBody, maxIdempotencyBodyBytes)
	bodyBytes, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}
	req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	return bodyBytes, nil
}

// writeRequestBodyError maps an oversized bounded control envelope to 413 and
// keeps malformed/truncated request bodies as 400.
func writeRequestBodyError(w http.ResponseWriter, err error) {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		writeError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("request body exceeds %d bytes", maxIdempotencyBodyBytes))
		return
	}
	writeError(w, http.StatusBadRequest, "request body unreadable: "+err.Error())
}

// idempotencyHash computes the SHA-256 of a bounded request envelope.
func idempotencyHash(bodyBytes []byte) []byte {
	if len(bodyBytes) == 0 {
		return nil
	}
	hash := sha256.Sum256(bodyBytes)
	return hash[:]
}
