package veloxcontract

import (
	"strings"
	"testing"
	"time"
)

func TestPreparationAndExecutionLeaseHaveDifferentKinds(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	prep, err := NewPreparationLease("prep-1", "job-1", "worker-b", 10*time.Minute, now)
	if err != nil {
		t.Fatal(err)
	}
	exec, err := NewExecutionLease("exec-1", "job-1", "worker-b", 5*time.Minute, now)
	if err != nil {
		t.Fatal(err)
	}
	if prep.Kind != PreparationLeaseKind || exec.Kind != ExecutionLeaseKind {
		t.Fatalf("unexpected lease kinds: %q and %q", prep.Kind, exec.Kind)
	}
	if prep.ExpiresAt != now.Add(10*time.Minute) || exec.ExpiresAt != now.Add(5*time.Minute) {
		t.Fatal("lease TTL was not applied from acquisition time")
	}
}

func TestLeaseHeartbeatExtendsOnlyActiveOwnedLeaseState(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	lease, err := NewPreparationLease("prep-1", "job-1", "worker-b", 10*time.Minute, now)
	if err != nil {
		t.Fatal(err)
	}
	heartbeat := now.Add(2 * time.Minute)
	if err := lease.RenewHeartbeat(heartbeat, 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	if lease.HeartbeatAt != heartbeat || lease.ExpiresAt != heartbeat.Add(10*time.Minute) {
		t.Fatalf("heartbeat did not extend lease: %+v", lease)
	}
	if err := lease.Release(heartbeat.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := lease.RenewHeartbeat(heartbeat.Add(2*time.Minute), 10*time.Minute); err == nil {
		t.Fatal("released lease must not accept heartbeat")
	}
}

func TestLeaseExpiresAndCannotBeReleasedAfterExpiry(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	lease, err := NewExecutionLease("exec-1", "job-1", "worker-b", 5*time.Minute, now)
	if err != nil {
		t.Fatal(err)
	}
	expiredAt := now.Add(5 * time.Minute)
	if !lease.Expire(expiredAt) || lease.State != LeaseExpired {
		t.Fatalf("lease did not expire: %+v", lease)
	}
	if err := lease.Release(expiredAt); err == nil {
		t.Fatal("expired lease must not be released")
	}
}

func TestLeaseValidationAndHeartbeatInterval(t *testing.T) {
	if _, err := NewPreparationLease("", "job", "worker", time.Second, time.Now()); err == nil {
		t.Fatal("invalid lease should be rejected")
	}
	if _, err := NewExecutionLease("lease", "job", "worker", 2*time.Second, time.Now()); err == nil {
		t.Fatal("TTL below minimum should be rejected")
	}
	if got := HeartbeatInterval(9 * time.Minute); got != 3*time.Minute {
		t.Fatalf("heartbeat interval = %s, want 3m", got)
	}
	if err := validateAssemblySHA256(strings.Repeat("A", 64)); err == nil {
		t.Fatal("uppercase SHA256 must be rejected")
	}
}
