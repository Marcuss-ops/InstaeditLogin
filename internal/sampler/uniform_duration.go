package sampler

import "time"

// UniformDuration samples one non-negative duration from the semi-open
// interval [min, max) using the caller-provided Int63n source. The source
// is injected so each retry policy keeps ownership of its random stream
// and seeding. When max <= min, or when either bound is negative, min is
// returned without invoking the source. The non-negative bound contract
// keeps max-min and min+sample representable as time.Duration values.
//
// This is deliberately distinct from RandomDurationInRange: that helper
// uses crypto/rand and an inclusive whole-second range for scheduling,
// while this primitive is for non-security-sensitive retry jitter.
func UniformDuration(min, max time.Duration, int63n func(int64) int64) time.Duration {
	if min < 0 || max < 0 || max <= min {
		return min
	}
	return min + time.Duration(int63n(int64(max-min)))
}
