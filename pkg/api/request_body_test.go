package api

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type trackingReadCloser struct {
	io.Reader
	closed bool
}

func (r *trackingReadCloser) Close() error {
	r.closed = true
	return nil
}

func TestIdempotencyReadBodyBoundsAndClosesOversizedBody(t *testing.T) {
	tracking := &trackingReadCloser{Reader: strings.NewReader(strings.Repeat("x", int(maxIdempotencyBodyBytes)+1))}
	req := httptest.NewRequest(http.MethodPost, "/", tracking)
	w := httptest.NewRecorder()

	_, err := idempotencyReadBody(w, req)
	if err == nil {
		t.Fatal("idempotencyReadBody: expected size error")
	}
	var maxErr *http.MaxBytesError
	if !errors.As(err, &maxErr) {
		t.Fatalf("idempotencyReadBody error: got %v, want MaxBytesError", err)
	}
	if !tracking.closed {
		t.Fatal("request body was not closed after bounded read failure")
	}
}

func TestWriteRequestBodyErrorMapsMaxBytesTo413(t *testing.T) {
	w := httptest.NewRecorder()
	writeRequestBodyError(w, &http.MaxBytesError{Limit: maxIdempotencyBodyBytes})
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status: got %d, want 413", w.Code)
	}
	if !strings.Contains(w.Body.String(), "request body exceeds") {
		t.Fatalf("body: got %q, want size-limit message", w.Body.String())
	}
}

func TestIdempotencyReadBodyRewindsBoundedBody(t *testing.T) {
	const payload = `{"workspace_id":1,"targets":[{"platform_account_id":2}]}`
	tracking := &trackingReadCloser{Reader: strings.NewReader(payload)}
	req := httptest.NewRequest(http.MethodPost, "/", tracking)
	w := httptest.NewRecorder()

	got, err := idempotencyReadBody(w, req)
	if err != nil {
		t.Fatalf("idempotencyReadBody: %v", err)
	}
	if string(got) != payload {
		t.Fatalf("bytes: got %q, want %q", got, payload)
	}
	rewound, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("rewound body: %v", err)
	}
	if string(rewound) != payload {
		t.Fatalf("rewound bytes: got %q, want %q", rewound, payload)
	}
	if !tracking.closed {
		t.Fatal("original request body was not closed")
	}
}
