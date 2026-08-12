package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

type webhookTestRepo struct {
	mu          sync.Mutex
	deliveries  []repository.WebhookDelivery
	claimed     int
	claimLimit  int
	claimTTL    time.Duration
	active      map[int64]string
	success     []int64
	retries     int
	deadCount   int
	lastError   string
	nextAttempt time.Time
	heartbeats  int
	maxActive   int
}

func (r *webhookTestRepo) ClaimDueDeliveries(_ context.Context, limit int, ttl time.Duration) ([]repository.WebhookDelivery, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.claimLimit, r.claimTTL = limit, ttl
	var out []repository.WebhookDelivery
	for i := range r.deliveries {
		if r.claimed >= limit {
			break
		}
		if r.active[r.deliveries[i].ID] != "" {
			continue
		}
		d := r.deliveries[i]
		d.LeaseID = "lease-" + string(rune('a'+len(out)))
		d.Attempt = 1
		r.active[d.ID] = d.LeaseID
		r.claimed++
		out = append(out, d)
	}
	return out, nil
}
func (r *webhookTestRepo) HeartbeatLease(_ context.Context, id int64, leaseID string, _ time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active[id] != leaseID {
		return repository.ErrWebhookLeaseLost
	}
	r.heartbeats++
	return nil
}
func (r *webhookTestRepo) MarkSuccess(_ context.Context, id int64, leaseID, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active[id] != leaseID {
		return repository.ErrWebhookLeaseLost
	}
	delete(r.active, id)
	r.success = append(r.success, id)
	return nil
}
func (r *webhookTestRepo) MarkRetry(_ context.Context, _ int64, _ string, lastError, _ string, _ string, nextAttempt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.retries++
	r.lastError = lastError
	r.nextAttempt = nextAttempt
	return nil
}
func (r *webhookTestRepo) MarkDead(_ context.Context, _ int64, _ string, lastError, _ string, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deadCount++
	r.lastError = lastError
	return nil
}
func (r *webhookTestRepo) FindEventByID(context.Context, int64) (*repository.WebhookEvent, error) {
	return &repository.WebhookEvent{ID: 1, EventID: "evt-1", Payload: []byte(`{"ok":true}`)}, nil
}
func (r *webhookTestRepo) FindEndpointByID(context.Context, int64) (*repository.WebhookEndpoint, error) {
	return &repository.WebhookEndpoint{ID: 1, URL: testWebhookURL, Secret: "test-secret"}, nil
}

var testWebhookURL string

func TestWebhookWorker_ConfigurableConcurrencyAndNoDuplicateClaims(t *testing.T) {
	var requests int32
	var active int32
	var maxActive int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&requests, 1)
		n := atomic.AddInt32(&active, 1)
		for {
			old := atomic.LoadInt32(&maxActive)
			if n <= old || atomic.CompareAndSwapInt32(&maxActive, old, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&active, -1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	testWebhookURL = server.URL

	repo := &webhookTestRepo{active: make(map[int64]string)}
	for i := int64(1); i <= 8; i++ {
		repo.deliveries = append(repo.deliveries, repository.WebhookDelivery{ID: i, EventID: 1, EndpointID: 1})
	}
	w := NewWebhookWorkerWithOptions(repo, WebhookWorkerOptions{
		Concurrency: 3, BatchSize: 25, HTTPTimeout: time.Second,
		LeaseTTL: 30 * time.Second, HeartbeatInterval: time.Second,
		HTTPClient: &http.Client{Timeout: time.Second},
	})
	w.runOnce(context.Background())

	repo.mu.Lock()
	claimed, claimLimit, successes := repo.claimed, repo.claimLimit, append([]int64(nil), repo.success...)
	claimTTL := repo.claimTTL
	repo.mu.Unlock()
	if claimed != 3 || claimLimit != 3 || len(successes) != 3 {
		t.Fatalf("claimed=%d limit=%d successes=%d, want 3/3/3", claimed, claimLimit, len(successes))
	}
	if claimTTL != 30*time.Second {
		t.Fatalf("claim TTL=%v, want 30s", claimTTL)
	}
	if atomic.LoadInt32(&requests) != 3 {
		t.Fatalf("HTTP requests=%d, want 3", requests)
	}
	if got := atomic.LoadInt32(&maxActive); got > 3 {
		t.Fatalf("max concurrent HTTP requests=%d, want <=3", got)
	}
}

func TestWebhookWorker_UsesSharedErrorClassification(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		retryAfter string
		wantRetry  bool
		wantDead   bool
		wantCode   string
	}{
		{name: "429 rate limited", status: http.StatusTooManyRequests, retryAfter: "30", wantRetry: true, wantCode: "rate_limited"},
		{name: "503 transient", status: http.StatusServiceUnavailable, wantRetry: true, wantCode: "provider_unavailable"},
		{name: "401 auth", status: http.StatusUnauthorized, wantDead: true, wantCode: "authentication_error"},
		{name: "403 auth", status: http.StatusForbidden, wantDead: true, wantCode: "authentication_error"},
		{name: "400 permanent", status: http.StatusBadRequest, wantDead: true, wantCode: "validation_error"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.retryAfter != "" {
					w.Header().Set("Retry-After", tc.retryAfter)
				}
				w.WriteHeader(tc.status)
			}))
			defer server.Close()
			testWebhookURL = server.URL
			repo := &webhookTestRepo{
				deliveries: []repository.WebhookDelivery{{ID: 101, EventID: 1, EndpointID: 1}},
				active:     make(map[int64]string),
			}
			w := NewWebhookWorkerWithOptions(repo, WebhookWorkerOptions{
				Concurrency: 1, HTTPTimeout: time.Second, LeaseTTL: 30 * time.Second,
				HTTPClient: &http.Client{Timeout: time.Second},
			})
			w.runOnce(context.Background())

			repo.mu.Lock()
			retries, dead, code, next := repo.retries, repo.deadCount, repo.lastError, repo.nextAttempt
			repo.mu.Unlock()
			if retries != btoi(tc.wantRetry) || dead != btoi(tc.wantDead) {
				t.Fatalf("routing retries=%d dead=%d, want retries=%v dead=%v (error=%q)", retries, dead, tc.wantRetry, tc.wantDead, code)
			}
			if code == "" || !strings.Contains(code, tc.wantCode) {
				t.Errorf("normalized error code: got %q, want contains %q", code, tc.wantCode)
			}
			if tc.retryAfter != "" && !next.After(time.Now().Add(20*time.Second)) {
				t.Errorf("429 Retry-After was not honored: next_attempt=%v", next)
			}
		})
	}
}

func btoi(value bool) int {
	if value {
		return 1
	}
	return 0
}

func TestWebhookWorker_HeartbeatFencesSlowDelivery(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	testWebhookURL = server.URL

	repo := &webhookTestRepo{
		deliveries: []repository.WebhookDelivery{{ID: 11, EventID: 1, EndpointID: 1}},
		active:     make(map[int64]string),
	}
	w := NewWebhookWorkerWithOptions(repo, WebhookWorkerOptions{
		Concurrency: 1, HTTPTimeout: time.Second, LeaseTTL: 30 * time.Second,
		HeartbeatInterval: 5 * time.Millisecond, HTTPClient: &http.Client{Timeout: time.Second},
	})
	done := make(chan struct{})
	go func() { w.runOnce(context.Background()); close(done) }()
	<-started
	time.Sleep(25 * time.Millisecond)
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("slow delivery did not finish")
	}
	repo.mu.Lock()
	heartbeats, successes := repo.heartbeats, len(repo.success)
	repo.mu.Unlock()
	if heartbeats == 0 {
		t.Fatal("expected at least one lease heartbeat during slow delivery")
	}
	if successes != 1 {
		t.Fatalf("successes=%d, want 1", successes)
	}
}
