package repository

import (
	"testing"
	"time"
)

func TestSnapshotFreshnessTTL_IsDeterministicAndBounded(t *testing.T) {
	base := 30 * time.Minute
	seen := make(map[time.Duration]struct{})
	for accountID := int64(1); accountID <= 500; accountID++ {
		got := SnapshotFreshnessTTL(accountID, base)
		if got < base || got > base+SnapshotRefreshTTLJitter {
			t.Fatalf("account %d: TTL %s outside [%s, %s]", accountID, got, base, base+SnapshotRefreshTTLJitter)
		}
		if got != SnapshotFreshnessTTL(accountID, base) {
			t.Fatalf("account %d: TTL is not deterministic", accountID)
		}
		seen[got] = struct{}{}
	}
	if len(seen) < 100 {
		t.Fatalf("jitter distribution too narrow: got %d distinct TTLs", len(seen))
	}
}

func TestSnapshotFreshnessTTL_InvalidInputsKeepBaseTTL(t *testing.T) {
	base := 10 * time.Minute
	if got := SnapshotFreshnessTTL(0, base); got != base {
		t.Fatalf("zero account id: got %s, want %s", got, base)
	}
	if got := SnapshotFreshnessTTL(-1, base); got != base {
		t.Fatalf("negative account id: got %s, want %s", got, base)
	}
	if got := SnapshotFreshnessTTL(42, 0); got != 0 {
		t.Fatalf("zero base TTL: got %s, want 0", got)
	}
}

func TestUniquePlatformAccountIDs_DeduplicatesAndDropsInvalidIDs(t *testing.T) {
	got := uniquePlatformAccountIDs([]int64{21, 0, 21, -4, 22, 21, 22})
	want := []int64{21, 22}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
