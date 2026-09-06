package veloxcontract

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// LeaseKind separates non-blocking preparation reservations from execution
// ownership. A preparation lease must never consume an execution slot.
type LeaseKind string

const (
	PreparationLeaseKind LeaseKind = "preparation"
	ExecutionLeaseKind   LeaseKind = "execution"
)

const (
	DefaultPreparationLeaseTTL = 10 * time.Minute
	DefaultExecutionLeaseTTL   = 5 * time.Minute
	MinimumLeaseTTL            = 5 * time.Second
	MaximumLeaseTTL            = 24 * time.Hour
)

// LeaseState is the lifecycle of a lease record.
type LeaseState string

const (
	LeaseActive   LeaseState = "ACTIVE"
	LeaseReleased LeaseState = "RELEASED"
	LeaseExpired  LeaseState = "EXPIRED"
)

// AssemblyLease is the wire representation shared by scheduler and worker.
// All mutations must be owner-checked by the control plane (CAS semantics).
type AssemblyLease struct {
	LeaseID     string     `json:"lease_id"`
	JobID       string     `json:"job_id"`
	WorkerID    string     `json:"worker_id"`
	Kind        LeaseKind  `json:"kind"`
	State       LeaseState `json:"state"`
	TTLSeconds  int        `json:"ttl_seconds"`
	AcquiredAt  time.Time  `json:"acquired_at"`
	HeartbeatAt time.Time  `json:"heartbeat_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
}

type PreparationLease = AssemblyLease
type ExecutionLease = AssemblyLease

func ValidateLeaseTTL(ttl time.Duration) error {
	if ttl < MinimumLeaseTTL || ttl > MaximumLeaseTTL {
		return fmt.Errorf("lease TTL must be between %s and %s", MinimumLeaseTTL, MaximumLeaseTTL)
	}
	return nil
}

func (l AssemblyLease) Validate() error {
	if strings.TrimSpace(l.LeaseID) == "" || strings.TrimSpace(l.JobID) == "" || strings.TrimSpace(l.WorkerID) == "" {
		return errors.New("lease_id, job_id and worker_id are required")
	}
	if l.Kind != PreparationLeaseKind && l.Kind != ExecutionLeaseKind {
		return fmt.Errorf("unsupported lease kind %q", l.Kind)
	}
	if l.State != LeaseActive && l.State != LeaseReleased && l.State != LeaseExpired {
		return fmt.Errorf("unsupported lease state %q", l.State)
	}
	if l.TTLSeconds <= 0 {
		return errors.New("ttl_seconds must be positive")
	}
	if l.AcquiredAt.IsZero() || l.HeartbeatAt.IsZero() || l.ExpiresAt.IsZero() {
		return errors.New("acquired_at, heartbeat_at and expires_at are required")
	}
	if l.ExpiresAt.Before(l.HeartbeatAt) {
		return errors.New("expires_at must not precede heartbeat_at")
	}
	return nil
}

// NewPreparationLease creates a lease that reserves a preferred worker for
// prefetch only. The worker remains eligible for unrelated execution jobs.
func NewPreparationLease(leaseID, jobID, workerID string, ttl time.Duration, now time.Time) (PreparationLease, error) {
	return newAssemblyLease(PreparationLeaseKind, leaseID, jobID, workerID, ttl, now)
}

// NewExecutionLease creates the exclusive lease that authorizes final assembly.
func NewExecutionLease(leaseID, jobID, workerID string, ttl time.Duration, now time.Time) (ExecutionLease, error) {
	return newAssemblyLease(ExecutionLeaseKind, leaseID, jobID, workerID, ttl, now)
}

func newAssemblyLease(kind LeaseKind, leaseID, jobID, workerID string, ttl time.Duration, now time.Time) (AssemblyLease, error) {
	if err := ValidateLeaseTTL(ttl); err != nil {
		return AssemblyLease{}, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	lease := AssemblyLease{
		LeaseID: leaseID, JobID: jobID, WorkerID: workerID, Kind: kind,
		State: LeaseActive, TTLSeconds: int(ttl / time.Second),
		AcquiredAt: now, HeartbeatAt: now, ExpiresAt: now.Add(ttl),
	}
	if err := lease.Validate(); err != nil {
		return AssemblyLease{}, err
	}
	return lease, nil
}

// RenewHeartbeat performs the local state transition after the control plane
// has accepted an owner-and-unexpired CAS heartbeat. It is intentionally pure.
func (l *AssemblyLease) RenewHeartbeat(now time.Time, ttl time.Duration) error {
	if l == nil {
		return errors.New("lease is nil")
	}
	if l.State != LeaseActive {
		return fmt.Errorf("cannot heartbeat a %s lease", l.State)
	}
	if err := ValidateLeaseTTL(ttl); err != nil {
		return err
	}
	if l.IsExpired(now) {
		l.State = LeaseExpired
		return errors.New("lease has expired")
	}
	l.HeartbeatAt = now
	l.ExpiresAt = now.Add(ttl)
	l.TTLSeconds = int(ttl / time.Second)
	return nil
}

func (l *AssemblyLease) Release(now time.Time) error {
	if l == nil {
		return errors.New("lease is nil")
	}
	if l.State == LeaseReleased {
		return nil
	}
	if l.State == LeaseExpired {
		return errors.New("expired lease cannot be released")
	}
	l.State = LeaseReleased
	l.ExpiresAt = now
	return nil
}

func (l *AssemblyLease) Expire(now time.Time) bool {
	if l == nil || l.State != LeaseActive || !l.IsExpired(now) {
		return false
	}
	l.State = LeaseExpired
	return true
}

func (l AssemblyLease) IsExpired(now time.Time) bool {
	return !now.Before(l.ExpiresAt)
}

// HeartbeatInterval recommends TTL/3, matching existing worker lease policy.
func HeartbeatInterval(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return time.Second
	}
	interval := ttl / 3
	if interval < time.Second {
		return time.Second
	}
	return interval
}

// LeaseMutation is the CAS request sent by a worker to the control plane.
type LeaseMutation struct {
	LeaseID  string    `json:"lease_id"`
	JobID    string    `json:"job_id"`
	WorkerID string    `json:"worker_id"`
	At       time.Time `json:"at"`
}

// LeaseOperation names the control-plane actions. Implementations should
// reject stale lease IDs, wrong owners and expired active leases atomically.
type LeaseOperation string

const (
	LeaseAcquire   LeaseOperation = "acquire"
	LeaseHeartbeat LeaseOperation = "heartbeat"
	LeaseRelease   LeaseOperation = "release"
	LeaseExpire    LeaseOperation = "expire"
)
