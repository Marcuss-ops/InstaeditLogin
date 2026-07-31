package analytics

import (
	"errors"
	"fmt"
	"reflect"
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
	clock Clock

	// Now is retained for source compatibility with older consumers that
	// constructed Resolver{Now: func() time.Time { ... }} directly.
	// New code should inject a Clock with WithClock.
	//
	// Deprecated: use WithClock(Clock) instead.
	Now func() time.Time
}

// NewResolver returns a Resolver using RealClock in production.
func NewResolver() *Resolver {
	return &Resolver{clock: RealClock{}, Now: RealClock{}.Now}
}

// DefaultResolver is the process-wide resolver. Production code
// SHOULD call DefaultResolver.Resolve; tests SHOULD construct their
// own Resolver via WithClock to keep boundaries deterministic.
var DefaultResolver = NewResolver()

// WithClock returns a copy of r using clock. The original receiver is
// untouched, so concurrent readers of r continue to see the production clock.
// New code should pass RealClock{} or NewFixedClock(...).
func (r *Resolver) WithClock(clock Clock) *Resolver {
	cp := Resolver{clock: RealClock{}, Now: RealClock{}.Now}
	if r != nil {
		cp = *r
	}
	if isNilClock(clock) {
		cp.clock = RealClock{}
		cp.Now = RealClock{}.Now
		return &cp
	}
	cp.clock = clock
	cp.Now = nil
	return &cp
}

// WithLegacyClock adapts the pre-Clock function-shaped injection API.
//
// Deprecated: use WithClock(Clock) instead.
func (r *Resolver) WithLegacyClock(now func() time.Time) *Resolver {
	cp := Resolver{clock: RealClock{}, Now: RealClock{}.Now}
	if r != nil {
		cp = *r
	}
	if now == nil {
		cp.clock = RealClock{}
		cp.Now = RealClock{}.Now
		return &cp
	}
	cp.clock = clockFunc(now)
	cp.Now = nil
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

// clockFunc adapts the legacy function-shaped clock to Clock.
type clockFunc func() time.Time

func (f clockFunc) Now() time.Time { return f() }

func isNilClock(clock Clock) bool {
	if clock == nil {
		return true
	}
	value := reflect.ValueOf(clock)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// normalisedNow returns the injected instant as UTC. Resolve itself drops
// time-of-day via time.Date so any clock value is fine here.
func (r *Resolver) normalisedNow() time.Time {
	if r == nil {
		return RealClock{}.Now()
	}
	if !isNilClock(r.clock) {
		return r.clock.Now().UTC()
	}
	if r.Now != nil {
		return r.Now().UTC()
	}
	return RealClock{}.Now()
}

// Resolve is shorthand for DefaultResolver.Resolve(days int). It is
// the entry point production callers SHOULD use; tests SHOULD build
// a Resolver.WithClock so boundaries are deterministic.
func Resolve(days int) (Period, error) {
	return DefaultResolver.Resolve(days)
}
