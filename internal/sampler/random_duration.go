package sampler

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"time"
)

// RandomDurationInRange returns a cryptographically random whole-second
// duration in the inclusive range [minSeconds, maxSeconds]. It is intended
// for scheduling jitter, not retry policy: callers own their defaults and
// validation semantics while this helper owns unbiased sampling.
func RandomDurationInRange(minSeconds, maxSeconds int) (time.Duration, error) {
	if minSeconds > maxSeconds {
		return 0, fmt.Errorf("random duration: min (%d) > max (%d)", minSeconds, maxSeconds)
	}

	span := int64(maxSeconds - minSeconds)
	n, err := rand.Int(rand.Reader, big.NewInt(span+1))
	if err != nil {
		return 0, fmt.Errorf("random duration: crypto/rand: %w", err)
	}
	return time.Duration(int64(minSeconds)+n.Int64()) * time.Second, nil
}
