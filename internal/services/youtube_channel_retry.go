package services

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"time"
)

type YouTubeAPIError struct {
	StatusCode int
	Category   string
	Message    string
}

// Error implements the error interface.
func (e *YouTubeAPIError) Error() string {
	return e.Message
}

// Transient reports whether the error is likely to resolve on retry.
// Network-level failures (category "network") and explicit rate-limit / 5xx
// HTTP responses are considered transient and safe to retry.
func (e *YouTubeAPIError) Transient() bool {
	if e.Category == "network" {
		return true
	}
	if e.StatusCode == http.StatusTooManyRequests {
		return true
	}
	if e.StatusCode >= 500 {
		return true
	}
	return false
}

// retryableError reports whether err is a transient error that should be
// retried. It returns true for YouTubeAPIError marked as transient (429,
// 5xx, network failures) and false for context cancellation/deadline errors
// and any other non-transient errors.
func retryableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var apiErr *YouTubeAPIError
	if errors.As(err, &apiErr) {
		return apiErr.Transient()
	}
	// Unknown errors are not retried by default to avoid masking application
	// bugs or repeatedly failing deterministic pre-conditions.
	return false
}

// doWithRetry runs fn up to maxAttempts with exponential backoff and
// bounded jitter. It only retries when fn returns a retryable error and
// honors context cancellation before each attempt and between attempts.
//
// The delay for attempt i is min(cap, base * 2^i) and then jittered
// between 50% and 100% of that value to prevent synchronized retries
// (thundering herd) when many goroutines retry simultaneously.
func doWithRetry(ctx context.Context, maxAttempts int, baseDelay time.Duration, fn func() error) error {
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := fn(); err == nil {
			return nil
		} else if !retryableError(err) {
			return err
		} else {
			lastErr = err
		}
		if attempt < maxAttempts-1 {
			delay := baseDelay * time.Duration(1<<uint(attempt))
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
			// Add bounded jitter to avoid thundering herd. The wait time is
			// uniformly distributed in [delay/2, delay].
			if delay > 0 {
				half := delay / 2
				jitter := time.Duration(rand.Int63n(int64(half) + 1))
				delay = half + jitter
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	if lastErr == nil {
		return fmt.Errorf("exceeded retry attempts")
	}
	return lastErr
}
