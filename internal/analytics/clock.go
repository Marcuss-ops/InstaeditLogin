package analytics

import "time"

// Clock supplies the current instant to time-dependent analytics logic.
// Implementations must return an instant with a meaningful location; callers
// normalize it to UTC before deriving calendar dates.
type Clock interface {
	Now() time.Time
}

// RealClock is the production clock. It always returns UTC so analytics code
// never inherits the host process timezone.
type RealClock struct{}

func (RealClock) Now() time.Time {
	return time.Now().UTC()
}

// FixedClock is a deterministic clock for tests and simulations.
type FixedClock struct {
	At time.Time
}

func NewFixedClock(at time.Time) FixedClock {
	return FixedClock{At: at}
}

func (c FixedClock) Now() time.Time {
	return c.At
}
