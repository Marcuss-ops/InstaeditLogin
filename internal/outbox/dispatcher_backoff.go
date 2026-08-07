package outbox

import (
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/sampler"
)

// computeBackoff returns the next-attempt delay using AWS-style
// decorrelated jitter (Marcuss architecture blog "Exponential Backoff
// and Jitter"):
//
//	temp = min(cap, prev * 3)
//	sleep = uniform(base..temp)
//
// where `prev` is reconstructed from the attempt count as
// `base * 2^(attempt-1)`. The bound ensures retries don't
// synchronise across replicas after a transient outage
// (the canonical thundering-herd problem).
//
// Decorrelated jitter's lower bound (uniform base..temp) is much
// more aggressive than full-jitter (uniform 0..temp) — it preserves
// a minimum retry cadence even at large attempt counts. The
// alternative "equal jitter" (temp/2 + uniform 0..temp/2) is more
// conservative but drives longer retry tails.
//
// Whether prev is exact or a heuristic doesn't matter for correctness
// (the cap is enforced first) but the heuristic version matches
// what the dispatcher's loop will see when its own MarkFailed calls
// stamp the next_attempt_at column.
func (d *Dispatcher) computeBackoff(attempt int) time.Duration {
	base := d.cfg.BaseDelay
	cap := d.cfg.CapDelay
	if attempt < 1 {
		attempt = 1
	}
	// prev = base * 2^(attempt-1), capped. Use float64 for the
	// multiplication because base << cap and float precision is
	// fine for retry timings.
	prev := float64(base) * pow2(attempt-1)
	if prev > float64(cap) {
		prev = float64(cap)
	}
	temp := prev * 3
	if temp > float64(cap) {
		temp = float64(cap)
	}
	// uniform_int63n returns [0, n). We want [base, temp]. Compute
	// delta = uniform_int63n(temp - base) then sleep = base + delta.
	return sampler.UniformDuration(base, time.Duration(temp), d.rand.Int63n)
}

// pow2 returns 2^n as a float64. Inlined helper to avoid pulling
// in math/bits or math.Pow for a simple doubling. Caller is bounds-
// aware (n in [0, ~30] practically).
func pow2(n int) float64 {
	if n <= 0 {
		return 1
	}
	r := 1.0
	for i := 0; i < n; i++ {
		r *= 2
	}
	return r
}
