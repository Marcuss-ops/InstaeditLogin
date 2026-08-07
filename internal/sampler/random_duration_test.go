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
