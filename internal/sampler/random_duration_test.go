package sampler

import (
	"strings"
	"testing"
	"time"
)

func TestRandomDurationInRange_InclusiveBounds(t *testing.T) {
	const minSeconds = 60
	const maxSeconds = 1800

	for i := 0; i < 500; i++ {
		got, err := RandomDurationInRange(minSeconds, maxSeconds)
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if got < minSeconds*time.Second || got > maxSeconds*time.Second {
			t.Fatalf("iteration %d: got %v outside [%s, %s]", i, got, minSeconds*time.Second, maxSeconds*time.Second)
		}
	}
}

func TestRandomDurationInRange_Singleton(t *testing.T) {
	got, err := RandomDurationInRange(3600, 3600)
	if err != nil {
		t.Fatalf("RandomDurationInRange: %v", err)
	}
	if got != time.Hour {
		t.Fatalf("got %v, want %v", got, time.Hour)
	}
}

func TestRandomDurationInRange_ReversedBounds(t *testing.T) {
	_, err := RandomDurationInRange(100, 10)
	if err == nil {
		t.Fatal("expected reversed bounds error")
	}
	if !strings.Contains(err.Error(), "min") || !strings.Contains(err.Error(), "max") {
		t.Fatalf("error = %q, want min/max details", err)
	}
}

func TestRandomDurationInRange_NegativeBounds(t *testing.T) {
	got, err := RandomDurationInRange(-10, -5)
	if err != nil {
		t.Fatalf("RandomDurationInRange: %v", err)
	}
	if got < -10*time.Second || got > -5*time.Second {
		t.Fatalf("got %v outside [-10s, -5s]", got)
	}
}

func TestRandomDurationInRange_RejectsDurationOverflow(t *testing.T) {
	for _, tc := range []struct {
		name string
		min  int
		max  int
	}{
		{name: "below minimum", min: int(minRandomDurationSeconds) - 1, max: int(minRandomDurationSeconds) - 1},
		{name: "above maximum", min: int(maxRandomDurationSeconds) + 1, max: int(maxRandomDurationSeconds) + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := RandomDurationInRange(tc.min, tc.max); err == nil {
				t.Fatalf("RandomDurationInRange(%d, %d) returned nil error", tc.min, tc.max)
			}
		})
	}
}

func TestRandomDurationInRange_AcceptsDurationLimits(t *testing.T) {
	for _, seconds := range []int{int(minRandomDurationSeconds), int(maxRandomDurationSeconds)} {
		got, err := RandomDurationInRange(seconds, seconds)
		if err != nil {
			t.Fatalf("RandomDurationInRange(%d, %d): %v", seconds, seconds, err)
		}
		want := time.Duration(seconds) * time.Second
		if got != want {
			t.Fatalf("got %d, want %d", got, want)
		}
	}
}
