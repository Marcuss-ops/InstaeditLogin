package services

import (
	"context"
	"log/slog"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

// NewHTTPClient returns a shared *http.Client with sensible defaults for
// outbound OAuth and publishing API calls. All platform providers use this
// factory instead of ad-hoc http.Client{Timeout: 30s} literals.
//
// Defaults:
//   - 30s request timeout (covers token exchanges, user-info fetches, uploads)
//   - Connection pooling with 100 idle conns, 90s idle timeout
//   - Retry on transient errors (3 attempts, exponential backoff 100ms→400ms
//     with jitter) only for idempotent methods (GET, HEAD, OPTIONS, TRACE)
//   - 429/5xx retry limited to 408, 429, 500, 502, 503, 504
//   - POST/PUT retry only when the caller explicitly opts in via
//     WithRetryOptIn
//   - Retry-After header is honored (capped at 30s) and jitter is applied
//   - The final response (body + headers) is always returned, even when the
//     status code is in the retry set
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

// retryContextKey is the context key used to opt POST/PUT requests into the
// retry policy. Callers wrap the request context with WithRetryOptIn.
type retryContextKey struct{}

// WithRetryOptIn marks the request context so that POST/PUT methods are also
// considered idempotent for retry purposes. Use this only when the caller
// knows the request is safe to replay (e.g. an idempotency key, a conditional
// PUT, or a side-effect-free POST).
func WithRetryOptIn(ctx context.Context) context.Context {
	return context.WithValue(ctx, retryContextKey{}, true)
}

// retryRoundTripper retries a narrowly-defined set of transient failures for
// idempotent requests.
//
// Retry policy:
//   - Max 3 attempts total (1 initial + 2 retries)
//   - Exponential backoff with jitter, starting at 100ms and capped at 30s
//   - Only the status codes 408, 429, 500, 502, 503 and 504 are retried
//   - Idempotent methods (GET, HEAD, OPTIONS, TRACE) are retried automatically
//   - POST/PUT are retried only when the request context carries the opt-in
//     flag set by WithRetryOptIn
//   - Retry-After is honored when present and is capped by the same 30s maximum
//   - The request body is rebuilt using Request.GetBody so retries do not
//     consume or duplicate the original payload
//   - The final HTTP response is returned as-is; it is NOT converted into a
//     synthetic error, so the caller keeps the full body and headers
//   - Transport-level errors are retried only for idempotent requests
//   - Non-retryable status codes and transport errors for non-idempotent
//     requests are returned immediately
func (rrt *retryRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	var lastResp *http.Response
	var lastErr error
	backoff := initialBackoff

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			// Rebuild the request body for this retry. If GetBody is missing we
			// cannot safely replay the request, so bail out and return whatever
			// we already have.
			if req.Body != nil {
				if req.GetBody == nil {
					break
				}
				newBody, err := req.GetBody()
				if err != nil {
					break
				}
				req.Body = newBody
			}

			// Wait before the next attempt, honoring Retry-After from the
			// previous response and adding jitter.
			delay := rrt.delayForRetry(backoff, lastResp)
			select {
			case <-req.Context().Done():
				return nil, req.Context().Err()
			case <-time.After(delay):
			}
			backoff = rrt.capBackoff(backoff * 2)
		}

		resp, err := rrt.next.RoundTrip(req)
		if err != nil {
			lastErr = err
			lastResp = nil
			if !isIdempotent(req) || attempt == maxAttempts-1 {
				return nil, lastErr
			}
			continue
		}
		lastErr = nil
		lastResp = resp

		if !isRetryableStatus(resp.StatusCode) || !isIdempotent(req) {
			return resp, nil
		}

		// This is a retryable response. If it is also the last allowed
		// attempt, return it as-is so the caller sees the real body and
		// headers.
		if attempt == maxAttempts-1 {
			return resp, nil
		}

		// Discard the body of an intermediate response; it will not be seen
		// by the caller.
		resp.Body.Close()
	}

	return lastResp, lastErr
}

const (
	maxAttempts    = 3
	initialBackoff = 100 * time.Millisecond
	maxBackoff     = 30 * time.Second
)

type retryRoundTripper struct {
	next       http.RoundTripper
	maxBackoff time.Duration // test hook; zero means use the package default
}

// maxBackoffOrDefault returns the configured maximum backoff. If the
// test hook is unset, it returns the production default.
func (rrt *retryRoundTripper) maxBackoffOrDefault() time.Duration {
	if rrt.maxBackoff == 0 {
		return maxBackoff
	}
	return rrt.maxBackoff
}

// delayForRetry returns the wait duration before the next retry. It honors
// Retry-After when present, caps the result at maxBackoff, and adds jitter.
func (rrt *retryRoundTripper) delayForRetry(base time.Duration, resp *http.Response) time.Duration {
	delay := base
	if resp != nil {
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if seconds, err := strconv.Atoi(ra); err == nil && seconds >= 0 {
				delay = time.Duration(seconds) * time.Second
			}
			// TODO: support RFC 7231 HTTP-date format if a server sends it.
		}
	}
	if cap := rrt.maxBackoffOrDefault(); delay > cap {
		delay = cap
	}
	if delay <= 0 {
		delay = initialBackoff
	}

	// Add up to 25% jitter.
	jitter := time.Duration(rand.Int63n(int64(delay) / 4))
	return delay + jitter
}

func (rrt *retryRoundTripper) capBackoff(d time.Duration) time.Duration {
	cap := rrt.maxBackoffOrDefault()
	if d > cap {
		return cap
	}
	return d
}

func isIdempotent(req *http.Request) bool {
	if req == nil {
		return false
	}
	switch req.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	}
	optIn, _ := req.Context().Value(retryContextKey{}).(bool)
	return optIn
}

func isRetryableStatus(code int) bool {
	switch code {
	case http.StatusRequestTimeout,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	}
	return false
}

// loggingRoundTripper wraps an http.RoundTripper and logs every request/response
// at debug level so operators can trace outbound API calls without enabling
// net/http/httputil dump on the whole process.
type loggingRoundTripper struct {
	next http.RoundTripper
}

func (lrt *loggingRoundTripper) RoundTrip(req *http.Request) (resp *http.Response, err error) {
	start := time.Now()
	resp, err = lrt.next.RoundTrip(req)
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
