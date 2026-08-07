package sampler

import (
	"testing"
	"time"
)

func TestUniformDuration_SemiOpenRange(t *testing.T) {
	min := 100 * time.Millisecond
	max := 500 * time.Millisecond
	values := []int64{0, 1, int64(max - min - 1)}
	for _, offset := range values {
		got := UniformDuration(min, max, func(n int64) int64 {
			if n != int64(max-min) {
				t.Fatalf("source bound = %d, want %d", n, max-min)
			}
			return offset
		})
		if got < min || got >= max {
			t.Fatalf("offset %d: got %s outside [%s, %s)", offset, got, min, max)
		}
	}
}

func TestUniformDuration_UsesEveryBucket(t *testing.T) {
	min := time.Second
	max := 5 * time.Second
	counts := make([]int, 4)
	for bucket := int64(0); bucket < 4; bucket++ {
		offset := bucket * int64(time.Second)
		got := UniformDuration(min, max, func(n int64) int64 {
			if n != 4*int64(time.Second) {
				t.Fatalf("source bound = %d, want %d", n, 4*int64(time.Second))
			}
			return offset
		})
		bucketIndex := int((got - min) / time.Second)
		counts[bucketIndex]++
	}
	for i, count := range counts {
		if count != 1 {
			t.Errorf("bucket %d count = %d, want 1", i, count)
		}
	}
}

func TestUniformDuration_NonPositiveSpanReturnsLowerBound(t *testing.T) {
	for _, tc := range []struct {
		name string
		min  time.Duration
		max  time.Duration
		want time.Duration
	}{
		{name: "equal", min: 2 * time.Second, max: 2 * time.Second, want: 2 * time.Second},
		{name: "reversed", min: 3 * time.Second, max: 2 * time.Second, want: 3 * time.Second},
		{name: "negative lower bound", min: -2 * time.Second, max: time.Second, want: -2 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			got := UniformDuration(tc.min, tc.max, func(int64) int64 {
				called = true
				return 0
			})
			if got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
			if called {
				t.Fatal("random source called for non-positive span")
			}
		})
	}
}

func TestUniformDuration_ExtremeNonNegativeBoundsStayOrdered(t *testing.T) {
	got := UniformDuration(0, time.Duration(1<<63-1), func(n int64) int64 {
		if n != 1<<63-1 {
			t.Fatalf("source bound = %d, want %d", n, int64(1<<63-1))
		}
		return n - 1
	})
	if got != time.Duration(1<<63-2) {
		t.Fatalf("got %d, want %d", got, int64(1<<63-2))
	}
}
