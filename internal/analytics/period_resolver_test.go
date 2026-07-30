package analytics

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// fixedClock returns a clock function that always yields the same
// instant. Tests use it to anchor boundary math (month-end, leap day,
// year-end) so day arithmetic is deterministic regardless of when
// "now" actually runs.
func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// subName formats a period-days value as a stable, readable subtest
// name. The helper exists only to keep call sites short; behaviour
// is just strconv.Itoa wrapped with a sign-aware prefix.
func subName(d int) string {
	return fmt.Sprintf("d_%d", d)
}

// TestResolver_ValidPeriods asserts the canonical {7, 14, 28} set
// yields the expected window arithmetic against a fixed clock in
// the middle of a normal month.
func TestResolver_ValidPeriods(t *testing.T) {
	clock := fixedClock(time.Date(2026, 7, 30, 14, 37, 12, 555, time.UTC))
	r := NewResolver().WithClock(clock)

	cases := []struct {
		days        int
		wantStart   time.Time
		wantEnd     time.Time
		wantPrev    time.Time
		wantPrevEnd time.Time
	}{
		{
			days:        7,
			wantStart:   time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC),
			wantEnd:     time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC),
			wantPrev:    time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC),
			wantPrevEnd: time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC),
		},
		{
			days:        14,
			wantStart:   time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC),
			wantEnd:     time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC),
			wantPrev:    time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC),
			wantPrevEnd: time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC),
		},
		{
			days:        28,
			wantStart:   time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC),
			wantEnd:     time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC),
			wantPrev:    time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC),
			wantPrevEnd: time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(subName(tc.days), func(t *testing.T) {
			got, err := r.Resolve(tc.days)
			if err != nil {
				t.Fatalf("Resolve(%d): unexpected error %v", tc.days, err)
			}
			if got.Days != tc.days {
				t.Errorf("Days: want %d, got %d", tc.days, got.Days)
			}
			if !got.StartDate.Equal(tc.wantStart) {
				t.Errorf("StartDate: want %v, got %v", tc.wantStart, got.StartDate)
			}
			if !got.EndDate.Equal(tc.wantEnd) {
				t.Errorf("EndDate: want %v, got %v", tc.wantEnd, got.EndDate)
			}
			if !got.PreviousStartDate.Equal(tc.wantPrev) {
				t.Errorf("PreviousStartDate: want %v, got %v", tc.wantPrev, got.PreviousStartDate)
			}
			if !got.PreviousEndDate.Equal(tc.wantPrevEnd) {
				t.Errorf("PreviousEndDate: want %v, got %v", tc.wantPrevEnd, got.PreviousEndDate)
			}
			if got.Timezone != PeriodUTC {
				t.Errorf("Timezone: want %q, got %q", PeriodUTC, got.Timezone)
			}
		})
	}
}

// TestResolver_InvalidPeriods asserts every documented invalid value
// returns ErrInvalidPeriod (errors.Is matchable) and a zero Period.
// Mixing windows (e.g. 30, 90) MUST be rejected by the resolver —
// Step 2's handler MUST NOT pre-validate this without first going
// through the resolver.
func TestResolver_InvalidPeriods(t *testing.T) {
	r := NewResolver().WithClock(fixedClock(time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)))

	invalid := []int{
		0, 1, 6, 8, 13, 15, 21, 27, 29, 30, 60, 90, 180, 365,
		-1, -7, -28,
		// int extremes — math.MinInt32 / MaxInt32 used to have
		// triggered a panic in handlers that did AddDate(0, 0, days-1)
		// without first validating the upper / lower bound.
		-2147483648, 2147483647,
	}
	for _, d := range invalid {
		d := d
		t.Run(fmt.Sprintf("d_%d", d), func(t *testing.T) {
			got, err := r.Resolve(d)
			if !errors.Is(err, ErrInvalidPeriod) {
				t.Errorf("Resolve(%d): want ErrInvalidPeriod, got %v", d, err)
			}
			if err == nil {
				t.Fatalf("Resolve(%d): want error, got nil", d)
			}
			if got != (Period{}) {
				t.Errorf("Resolve(%d) on invalid input MUST return zero Period, got %+v", d, got)
			}
		})
	}
}

// TestResolver_MonthEndBoundary covers the trickiest case for naive
// day arithmetic: when end_date is the last day of a month, the
// previous window crosses a month boundary. AddDate handles this
// correctly; this test pins the behaviour so a refactor that swaps
// to Duration arithmetic does not regress.
func TestResolver_MonthEndBoundary(t *testing.T) {
	// 31 March 2026 → 7-day current window ends on 31 Mar.
	clock := fixedClock(time.Date(2026, 3, 31, 9, 0, 0, 0, time.UTC))
	r := NewResolver().WithClock(clock)
	got, err := r.Resolve(7)
	if err != nil {
		t.Fatalf("Resolve(7): %v", err)
	}
	want := Period{
		Days:              7,
		StartDate:         time.Date(2026, 3, 25, 0, 0, 0, 0, time.UTC),
		EndDate:           time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC),
		PreviousStartDate: time.Date(2026, 3, 18, 0, 0, 0, 0, time.UTC),
		PreviousEndDate:   time.Date(2026, 3, 24, 0, 0, 0, 0, time.UTC),
		Timezone:          PeriodUTC,
	}
	if !periodsEqual(got, want) {
		t.Errorf("March month-end (7d) mismatch:\n want: %+v\n got:  %+v", want, got)
	}

	// 28-day window ending on 31 March: previous window MUST end on
	// 3 March (28 days earlier) and start on 4 February.
	got28, err := r.Resolve(28)
	if err != nil {
		t.Fatalf("Resolve(28): %v", err)
	}
	if !got28.PreviousEndDate.Equal(time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("28d previous-end (March→Feb): want 2026-03-03, got %v", got28.PreviousEndDate)
	}
	if !got28.PreviousStartDate.Equal(time.Date(2026, 2, 4, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("28d previous-start (March→Feb): want 2026-02-04, got %v", got28.PreviousStartDate)
	}
}

// TestResolver_YearEndBoundary locks behaviour across December →
// January, where a 7d window starting on 31 Dec must end on 6 Jan of
// the NEXT year (not loop back to the same year).
func TestResolver_YearEndBoundary(t *testing.T) {
	clock := fixedClock(time.Date(2026, 12, 31, 23, 59, 59, 999_999_999, time.UTC))
	r := NewResolver().WithClock(clock)
	got, err := r.Resolve(7)
	if err != nil {
		t.Fatalf("Resolve(7) year-end: %v", err)
	}
	want := Period{
		Days:              7,
		StartDate:         time.Date(2026, 12, 25, 0, 0, 0, 0, time.UTC),
		EndDate:           time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
		PreviousStartDate: time.Date(2026, 12, 18, 0, 0, 0, 0, time.UTC),
		PreviousEndDate:   time.Date(2026, 12, 24, 0, 0, 0, 0, time.UTC),
		Timezone:          PeriodUTC,
	}
	if !periodsEqual(got, want) {
		t.Errorf("year-end 7d mismatch:\n want: %+v\n got:  %+v", want, got)
	}

	// 14d window ending on 31 Dec 2026 → previous window ends on
	// 17 Dec 2026; both windows stay in 2026 because 14 < 18.
	got14, err := r.Resolve(14)
	if err != nil {
		t.Fatalf("Resolve(14) year-end: %v", err)
	}
	if got14.PreviousEndDate.Year() != 2026 {
		t.Errorf("14d year-end previous-end: want year 2026, got %v", got14.PreviousEndDate)
	}
	// 28d window ending on 31 Dec 2026 → previous window ends on
	// 3 Dec 2026 and starts on 6 Nov 2026.
	got28, err := r.Resolve(28)
	if err != nil {
		t.Fatalf("Resolve(28) year-end: %v", err)
	}
	if !got28.PreviousEndDate.Equal(time.Date(2026, 12, 3, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("28d year-end previous-end: want 2026-12-03, got %v", got28.PreviousEndDate)
	}
	if !got28.PreviousStartDate.Equal(time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("28d year-end previous-start: want 2026-11-06, got %v", got28.PreviousStartDate)
	}

	// 7d window ending on 7 Jan 2027 → previous window spans end of
	// 2026 into early 2027 (Dec 25-31). AddDate MUST NOT loop the
	// year back to the same slot.
	crossR := NewResolver().WithClock(fixedClock(time.Date(2027, 1, 7, 12, 0, 0, 0, time.UTC)))
	gotCross, err := crossR.Resolve(7)
	if err != nil {
		t.Fatalf("Resolve(7) cross-year: %v", err)
	}
	if !gotCross.StartDate.Equal(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("cross-year 7d start: want 2027-01-01, got %v", gotCross.StartDate)
	}
	if !gotCross.PreviousEndDate.Equal(time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("cross-year 7d previous-end: want 2026-12-31, got %v", gotCross.PreviousEndDate)
	}
}

// TestResolver_LeapDayBoundary keeps the 29 Feb case honest. The
// previous window crossing into/leaving February MUST handle the
// 28→29-day transition without panicking or shifting dates by an
// off-by-one.
func TestResolver_LeapDayBoundary(t *testing.T) {
	// 2024-03-01 → 7d current window ends on 2024-03-01, starts on
	// 2024-02-24; previous window spans 2024-02-17..2024-02-23.
	clock := fixedClock(time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC))
	r := NewResolver().WithClock(clock)
	got, err := r.Resolve(7)
	if err != nil {
		t.Fatalf("Resolve(7) leap: %v", err)
	}
	if !got.EndDate.Equal(time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("leap 7d end: want 2024-03-01, got %v", got.EndDate)
	}
	if !got.PreviousEndDate.Equal(time.Date(2024, 2, 23, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("leap 7d previous-end: want 2024-02-23, got %v", got.PreviousEndDate)
	}
	if !got.PreviousStartDate.Equal(time.Date(2024, 2, 17, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("leap 7d previous-start: want 2024-02-17, got %v", got.PreviousStartDate)
	}

	// 28d window ending on 2024-02-29 → previous window ends on
	// 2024-02-01 and starts on 2024-01-05. This is the strongest
	// test of the leap-day handling.
	clockEnd := fixedClock(time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC))
	rEnd := NewResolver().WithClock(clockEnd)
	gotEnd, err := rEnd.Resolve(28)
	if err != nil {
		t.Fatalf("Resolve(28) leap-day end: %v", err)
	}
	if !gotEnd.PreviousEndDate.Equal(time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("leap 28d previous-end: want 2024-02-01, got %v", gotEnd.PreviousEndDate)
	}
	if !gotEnd.PreviousStartDate.Equal(time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("leap 28d previous-start: want 2024-01-05, got %v", gotEnd.PreviousStartDate)
	}
}

// TestResolver_PeriodCoverage asserts the structural invariants the
// service and the SPA rely on: each window covers exactly `days`
// days inclusive, and the boundary between current and previous is
// gap-free (no overlap, no missing day).
func TestResolver_PeriodCoverage(t *testing.T) {
	clock := fixedClock(time.Date(2026, 7, 30, 23, 0, 0, 0, time.UTC))
	r := NewResolver().WithClock(clock)
	for _, days := range []int{7, 14, 28} {
		days := days
		t.Run(subName(days), func(t *testing.T) {
			got, err := r.Resolve(days)
			if err != nil {
				t.Fatalf("Resolve(%d): %v", days, err)
			}

			// [StartDate, EndDate] inclusive → Δ in days == days - 1
			// (the "+1" converts the gap to a length).
			currentSpan := daysBetweenInclusive(got.StartDate, got.EndDate)
			if currentSpan != days {
				t.Errorf("current window span: want %d, got %d (start=%v end=%v)",
					days, currentSpan, got.StartDate, got.EndDate)
			}
			// [PreviousStartDate, PreviousEndDate] inclusive → Δ == days - 1 (+1 = days).
			previousSpan := daysBetweenInclusive(got.PreviousStartDate, got.PreviousEndDate)
			if previousSpan != days {
				t.Errorf("previous window span: want %d, got %d (start=%v end=%v)",
					days, previousSpan, got.PreviousStartDate, got.PreviousEndDate)
			}
			// No gap, no overlap: previous-end + 1 day == current-start.
			gapDay := got.PreviousEndDate.AddDate(0, 0, 1)
			if !gapDay.Equal(got.StartDate) {
				t.Errorf("current/previous boundary: want previous-end+1 == start, got previous-end=%v start=%v",
					got.PreviousEndDate, got.StartDate)
			}
		})
	}
}

// TestResolver_TimezoneIndependence forces r.Now() to return
// non-UTC time-zones; the resolver MUST normalise every output
// field to UTC so two clients in different zones see identical
// period boundaries.
func TestResolver_TimezoneIndependence(t *testing.T) {
	cases := []struct {
		name string
		tz   *time.Location
		at   time.Time
	}{
		{"Rome summer", time.FixedZone("Rome", 2*3600), time.Date(2026, 7, 30, 1, 0, 0, 0, time.FixedZone("Rome", 2*3600))},
		{"LA winter", time.FixedZone("LA", -8*3600), time.Date(2026, 1, 15, 23, 0, 0, 0, time.FixedZone("LA", -8*3600))},
		{"Tokyo odd", time.FixedZone("Tokyo", 9*3600), time.Date(2026, 7, 30, 8, 15, 0, 0, time.FixedZone("Tokyo", 9*3600))},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			r := NewResolver().WithClock(fixedClock(tc.at))
			got, err := r.Resolve(7)
			if err != nil {
				t.Fatalf("Resolve(7): %v", err)
			}
			// EndDate MUST be the UTC calendar day that "now" belongs
			// to, regardless of where the input clock sits.
			inUTC := tc.at.UTC()
			wantEnd := time.Date(inUTC.Year(), inUTC.Month(), inUTC.Day(), 0, 0, 0, 0, time.UTC)
			if !got.EndDate.Equal(wantEnd) {
				t.Errorf("EndDate for %s clock: want %v, got %v", tc.name, wantEnd, got.EndDate)
			}
			if got.EndDate.Location() != time.UTC {
				t.Errorf("EndDate location: want UTC, got %v", got.EndDate.Location())
			}
			if got.StartDate.Location() != time.UTC {
				t.Errorf("StartDate location: want UTC, got %v", got.StartDate.Location())
			}
			if got.PreviousStartDate.Location() != time.UTC {
				t.Errorf("PreviousStartDate location: want UTC, got %v", got.PreviousStartDate.Location())
			}
			if got.PreviousEndDate.Location() != time.UTC {
				t.Errorf("PreviousEndDate location: want UTC, got %v", got.PreviousEndDate.Location())
			}
		})
	}
}

// TestResolver_MidnightTruncation verifies that no time-of-day
// component survives Resolve — the wire shape carries pure dates
// and a stray hour/minute/second would break the SPA's date parser.
func TestResolver_MidnightTruncation(t *testing.T) {
	clock := fixedClock(time.Date(2026, 7, 30, 14, 37, 12, 555, time.UTC))
	r := NewResolver().WithClock(clock)
	got, err := r.Resolve(7)
	if err != nil {
		t.Fatalf("Resolve(7): %v", err)
	}
	for _, name := range []struct {
		field string
		d     time.Time
	}{
		{"StartDate", got.StartDate},
		{"EndDate", got.EndDate},
		{"PreviousStartDate", got.PreviousStartDate},
		{"PreviousEndDate", got.PreviousEndDate},
	} {
		t.Run(name.field, func(t *testing.T) {
			if name.d.Hour() != 0 || name.d.Minute() != 0 || name.d.Second() != 0 || name.d.Nanosecond() != 0 {
				t.Errorf("%s MUST be UTC midnight, got %v", name.field, name.d)
			}
		})
	}
}

// TestResolver_NilSafe verifies a nil Resolver (or one with nil
// Now) does not panic and falls back to time.Now().UTC(). This is
// defence-in-depth against a future regression in handler wiring.
func TestResolver_NilSafe(t *testing.T) {
	var r *Resolver
	defer func() {
		if rec := recover(); rec != nil {
			t.Errorf("nil Resolver panicked: %v", rec)
		}
	}()
	got, err := r.Resolve(7)
	if err != nil {
		t.Fatalf("nil Resolver.Resolve(7): %v", err)
	}
	if got.Days != 7 {
		t.Errorf("nil Resolver.Resolve(7) Days: want 7, got %d", got.Days)
	}

	emptyNow := &Resolver{}
	got, err = emptyNow.Resolve(14)
	if err != nil {
		t.Fatalf("Resolver{nil Now}.Resolve(14): %v", err)
	}
	if got.Days != 14 {
		t.Errorf("empty Resolver.Resolve(14) Days: want 14, got %d", got.Days)
	}
}

// TestPackageResolve_Shorthand locks the convenience method
// exported at package level. It builds a local NewResolver() rather
// than touching the process-wide singleton so the test cannot race
// on a future mutation (the singleton is read-only today, but the
// helper exists to make the sigil obvious).
func TestPackageResolve_Shorthand(t *testing.T) {
	got, err := Resolve(7)
	if err != nil {
		t.Fatalf("Resolve(7): %v", err)
	}
	if got.Days != 7 {
		t.Errorf("Resolve(7).Days: want 7, got %d", got.Days)
	}
	if _, err := Resolve(30); !errors.Is(err, ErrInvalidPeriod) {
		t.Errorf("Resolve(30): want ErrInvalidPeriod, got %v", err)
	}
	if DefaultResolver == nil {
		t.Errorf("DefaultResolver must be initialised at package load")
	}
}

// TestErrInvalidPeriod_Is guards the sentinel so a future refactor
// that switches fmt.Errorf → errors.New does not silently break
// the errors.Is matchers downstream (handler.go, service.go).
func TestErrInvalidPeriod_Is(t *testing.T) {
	_, err := DefaultResolver.Resolve(99)
	if !errors.Is(err, ErrInvalidPeriod) {
		t.Fatalf("DefaultResolver.Resolve(99): want ErrInvalidPeriod via errors.Is, got %v", err)
	}
	if err.Error() == "" {
		t.Errorf("err.Error() MUST be non-empty to aid log triage")
	}
}

// periodsEqual is a structural equality helper used by boundary
// tests. Time.Equal already handles zone-aware vs UTC comparisons
// correctly, but a full struct compare catches accidental drift in
// fields like Timezone.
func periodsEqual(a, b Period) bool {
	if a.Days != b.Days {
		return false
	}
	if !a.StartDate.Equal(b.StartDate) {
		return false
	}
	if !a.EndDate.Equal(b.EndDate) {
		return false
	}
	if !a.PreviousStartDate.Equal(b.PreviousStartDate) {
		return false
	}
	if !a.PreviousEndDate.Equal(b.PreviousEndDate) {
		return false
	}
	if a.Timezone != b.Timezone {
		return false
	}
	return true
}

// daysBetweenInclusive returns the inclusive day span between two
// UTC-midnight instants. Both endpoints MUST be normalised to
// 00:00:00.000000000 UTC — the resolver contract guarantees this,
// so a simple Sub + Duration division returns the right answer
// without any loop.
func daysBetweenInclusive(from, to time.Time) int {
	diff := to.Sub(from)
	if diff < 0 {
		return -daysBetweenInclusive(to, from)
	}
	return int(diff/(24*time.Hour)) + 1
}
