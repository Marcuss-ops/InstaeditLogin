package analytics

import (
	"errors"
	"fmt"
	"time"
)

// ErrInvalidPeriod is returned when the endpoint receives a days
// value outside the closed set {7, 14, 28}. The handler maps it to
// HTTP 400. errors.Is is fully supported through fmt.Errorf wraps.
var ErrInvalidPeriod = errors.New("analytics: days must be one of 7, 14, 28")

// Resolver converts a request-scope day count (7, 14, 28) into the
// pair of equivalent-length [current, previous] windows the
// per-channel endpoint compares. The resolver is the SINGLE source of
// truth for these calculations — handlers, services and tests MUST
// go through Resolver.Resolve rather than recomputing the math
// locally, otherwise the "previous = same-length window just before
// current" invariant the contract promises will quietly drift.
//
// The Resolver holds no I/O dependencies; callers inject a clock so
// boundary tests can pin month-end, year-end and leap-day
// arithmetic. Production code uses DefaultResolver.
type Resolver struct {
	// Now returns the "current instant" the resolver anchors end_date
	// to. The resolver normalises whatever Now returns to UTC midnight,
	// so callers may pass any time-zone-aware time.Time and the wire
	// shape stays consistent.
	Now func() time.Time
}

// NewResolver returns a Resolver anchored to time.Now().UTC(). Use
// Resolver.WithClock in tests that need deterministic boundaries.
func NewResolver() *Resolver {
	return &Resolver{Now: func() time.Time { return time.Now().UTC() }}
}

// DefaultResolver is the process-wide resolver. Production code
// SHOULD call DefaultResolver.Resolve; tests SHOULD construct their
// own Resolver via WithClock to keep boundaries deterministic.
var DefaultResolver = NewResolver()

// WithClock returns a copy of r whose Now function is replaced.
// The original receiver is untouched, so concurrent readers of r
// continue to see the production clock.
func (r *Resolver) WithClock(now func() time.Time) *Resolver {
	cp := *r
	cp.Now = now
	return &cp
}

// Resolve returns the canonical Period for the requested day count.
//
// Invariants (all guarded by tests in period_resolver_test.go):
//   - Days MUST be one of 7, 14, 28; any other value returns
//     ErrInvalidPeriod wrapped with the supplied days.
//   - All four dates are normalised to UTC midnight so the wire shape
//     carries pure calendar dates, not timestamps.
//   - [StartDate, EndDate] is inclusive and covers exactly Days days.
//   - [PreviousStartDate, PreviousEndDate] is inclusive and covers
//     exactly Days days, NEVER overlapping with the current window.
//   - PreviousEndDate and StartDate are ABUTTING calendar days
//     (PreviousEndDate + 1 == StartDate) so there are no gaps and no
//     double-counted days in the comparison.
//   - DST has zero effect because UTC has no DST; month-end and
//     year-end transitions are handled by time.Date.AddDate.
func (r *Resolver) Resolve(days int) (Period, error) {
	if !IsValidPeriod(days) {
		return Period{}, fmt.Errorf("analytics: days=%d not in {7,14,28}: %w", days, ErrInvalidPeriod)
	}

	now := r.normalisedNow()
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	start := end.AddDate(0, 0, -(days - 1))
	previousEnd := start.AddDate(0, 0, -1)
	previousStart := previousEnd.AddDate(0, 0, -(days - 1))

	return Period{
		Days:              days,
		StartDate:         start,
		EndDate:           end,
		PreviousStartDate: previousStart,
		PreviousEndDate:   previousEnd,
		Timezone:          PeriodUTC,
	}, nil
}

// normalisedNow returns r.Now() interpreted as a UTC instant. It
// deliberately DOES NOT truncate to midnight: Resolve itself drops
// time-of-day via time.Date so any clock value is fine here. The
// truncated midnight is a downstream concern. This split keeps the
// "what does Resolve anchor to" question readable in tests.
func (r *Resolver) normalisedNow() time.Time {
	if r == nil || r.Now == nil {
		return time.Now().UTC()
	}
	return r.Now().UTC()
}

// Resolve is shorthand for DefaultResolver.Resolve(days int). It is
// the entry point production callers SHOULD use; tests SHOULD build
// a Resolver.WithClock so boundaries are deterministic.
func Resolve(days int) (Period, error) {
	return DefaultResolver.Resolve(days)
}
