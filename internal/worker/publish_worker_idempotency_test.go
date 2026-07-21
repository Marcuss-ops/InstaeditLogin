package worker

import (
	"testing"
)

// ------------------------------------------------------------------
// computeProviderIdempotencyKey unit tests
// ------------------------------------------------------------------

// TestComputeProviderIdempotencyKey_Deterministic covers the
// Taglio 4.7 LEVEL 2 invariant: same (post_id, platform_account_id)
// → same hex prefix, every time. Retries reuse the same key.
func TestComputeProviderIdempotencyKey_Deterministic(t *testing.T) {
	k1 := computeProviderIdempotencyKey(100, 10)
	k2 := computeProviderIdempotencyKey(100, 10)
	if k1 != k2 {
		t.Errorf("not deterministic: %q vs %q", k1, k2)
	}
	if len(k1) != providerIdempotencyKeyLen {
		t.Errorf("len: want %d, got %d (%q)", providerIdempotencyKeyLen, len(k1), k1)
	}
}

// TestComputeProviderIdempotencyKey_DifferentInputs covers the
// security invariant: different (post_id, platform_account_id)
// tuples yield DIFFERENT keys (otherwise cross-account collisions
// would slip past the partial UNIQUE INDEX).
func TestComputeProviderIdempotencyKey_DifferentInputs(t *testing.T) {
	postA := computeProviderIdempotencyKey(100, 10)
	postB := computeProviderIdempotencyKey(101, 10) // different post
	acctA := computeProviderIdempotencyKey(100, 10)
	acctB := computeProviderIdempotencyKey(100, 11) // different account
	if postA == postB {
		t.Errorf("different post_ids collided: %q == %q", postA, postB)
	}
	if acctA == acctB {
		t.Errorf("different platform_account_ids collided: %q == %q", acctA, acctB)
	}
	if postA != acctA {
		t.Error("(100, 10) should be self-consistent")
	}
}
