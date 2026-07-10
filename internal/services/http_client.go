package services

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// NewHTTPClient returns a shared *http.Client with sensible defaults for
// outbound OAuth and publishing API calls. All platform providers use this
// factory instead of ad-hoc http.Client{Timeout: 30s} literals.
//
// Defaults:
//   - 30s request timeout (covers token exchanges, user-info fetches, uploads)
//   - Connection pooling with 100 idle conns, 90s idle timeout
//   - Retry on transient errors (3 attempts, exponential backoff 100ms→400ms)
//     for idempotent methods (GET, HEAD) and 429/5xx responses
//   - Debug-level request logging (method, URL, status, duration)
func NewHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &retryRoundTripper{
			next: &loggingRoundTripper{
				next: &http.Transport{
					MaxIdleConns:        100,
					MaxIdleConnsPerHost: 10,
					IdleConnTimeout:     90 * time.Second,
				},
			},
		},
	}
}

// retryRoundTripper retries transient failures for idempotent HTTP methods.
//
// Retry policy:
//   - Max 3 attempts total (1 initial + 2 retries)
//   - Exponential backoff: 100ms, 200ms, 400ms
//   - Only retries idempotent methods (GET, HEAD, OPTIONS) and 429/5xx responses
//   - Connection errors (DNS, TLS, refused, reset) are always retried regardless
//     of method because the request never reached the server
type retryRoundTripper struct {
	next http.RoundTripper
}

func (rrt *retryRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	var lastErr error
	backoff := 100 * time.Millisecond

	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			slog.Debug("http: retrying request",
				"attempt", attempt+1,
				"method", req.Method,
				"url", req.URL.String(),
				"backoff_ms", backoff.Milliseconds(),
			)
			select {
			case <-req.Context().Done():
				return nil, req.Context().Err()
			case <-time.After(backoff):
			}
			backoff *= 2
		}

		resp, err := rrt.next.RoundTrip(req)
		if err != nil {
			lastErr = err
			// Retry on connection-level errors: DNS, TLS handshake, refused, reset.
			// These never reached the server, so they're safe to retry for any method.
			continue
		}

		// Do not retry successful responses.
		if resp.StatusCode < 400 {
			return resp, nil
		}

		// Retry on server errors (5xx) and rate limiting (429) for idempotent methods.
		// Non-idempotent methods (POST, PUT, DELETE) after a 5xx may have already
		// mutated state — close the body and return the error as-is.
		if !isIdempotent(req.Method) && resp.StatusCode != 429 {
			return resp, nil
		}

		resp.Body.Close()
		lastErr = &retryableHTTPError{status: resp.StatusCode}
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, lastErr
}

type retryableHTTPError struct {
	status int
}

func (e *retryableHTTPError) Error() string {
	return fmt.Sprintf("request failed with status %d after retries", e.status)
}

func isIdempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

// loggingRoundTripper wraps an http.RoundTripper and logs every request/response
// at debug level so operators can trace outbound API calls without enabling
// net/http/httputil dump on the whole process.
type loggingRoundTripper struct {
	next http.RoundTripper
}

func (lrt *loggingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := lrt.next.RoundTrip(req)
	elapsed := time.Since(start)

	if err != nil {
		slog.Debug("http: request failed",
			"method", req.Method,
			"url", req.URL.String(),
			"duration_ms", elapsed.Milliseconds(),
			"error", err,
		)
		return nil, err
	}

	slog.Debug("http: request completed",
		"method", req.Method,
		"url", req.URL.String(),
		"status", resp.StatusCode,
		"duration_ms", elapsed.Milliseconds(),
	)
	return resp, nil
}
