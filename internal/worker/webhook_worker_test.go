package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

type webhookTestRepo struct {
	mu         sync.Mutex
	deliveries []repository.WebhookDelivery
	claimed    int
	claimLimit int
	claimTTL   time.Duration
	active     map[int64]string
	success    []int64
	heartbeats int
	maxActive  int
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
func (r *webhookTestRepo) MarkRetry(context.Context, int64, string, string, string, string, time.Time) error {
	return nil
}
func (r *webhookTestRepo) MarkDead(context.Context, int64, string, string, string, string) error {
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
