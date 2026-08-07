package services

import (
	"math/rand"
	"testing"
	"time"
)

func TestWebhookDispatcherNextAttempt_Bounds(t *testing.T) {
	d := NewWebhookDispatcher(nil)
	d.rand = rand.New(rand.NewSource(42))
	now := time.Unix(1_700_000_000, 0)
	const base = 60 * time.Second
	const capDelay = 12 * time.Hour

	for _, attempt := range []int{1, 2, 5, 10, 100} {
		got := d.NextAttempt(attempt, now)
		delay := got.Sub(now)
		if delay < base || delay > capDelay {
			t.Errorf("attempt %d: delay %s outside [%s, %s]", attempt, delay, base, capDelay)
		}
	}
}

func TestWebhookDispatcherNextAttempt_NonPositiveAttemptMatchesFirstAttemptPolicy(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	for _, attempt := range []int{0, -1} {
		legacy := NewWebhookDispatcher(nil)
		legacy.rand = rand.New(rand.NewSource(42))
		normalized := NewWebhookDispatcher(nil)
		normalized.rand = rand.New(rand.NewSource(42))
		if got, want := legacy.NextAttempt(attempt, now), normalized.NextAttempt(1, now); !got.Equal(want) {
			t.Errorf("attempt %d: got %s, want normalized attempt-1 policy %s", attempt, got, want)
		}
	}
}
