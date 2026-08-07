package sampler

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"time"
)

const (
	minRandomDurationSeconds = -int64(time.Duration(1<<63-1) / time.Second)
	maxRandomDurationSeconds = int64(time.Duration(1<<63-1) / time.Second)
)

// RandomDurationInRange returns a cryptographically random whole-second
// duration in the inclusive range [minSeconds, maxSeconds]. It is intended
// for scheduling jitter, not retry policy: callers own their defaults and
// validation semantics while this helper owns unbiased sampling.
func RandomDurationInRange(minSeconds, maxSeconds int) (time.Duration, error) {
	if minSeconds > maxSeconds {
		return 0, fmt.Errorf("random duration: min (%d) > max (%d)", minSeconds, maxSeconds)
	}

	min := int64(minSeconds)
	max := int64(maxSeconds)
	if min < minRandomDurationSeconds || max > maxRandomDurationSeconds {
		return 0, fmt.Errorf(
			"random duration: bounds [%d, %d] seconds exceed time.Duration range [%d, %d]",
			minSeconds,
			maxSeconds,
			minRandomDurationSeconds,
			maxRandomDurationSeconds,
		)
	}

	span := max - min
	n, err := rand.Int(rand.Reader, big.NewInt(span+1))
	if err != nil {
		return 0, fmt.Errorf("random duration: crypto/rand: %w", err)
	}
	return time.Duration(min+n.Int64()) * time.Second, nil
}
