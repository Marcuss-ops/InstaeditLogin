package worker

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

type aggregateWorkerStore struct {
	mu         sync.Mutex
	targets    map[int64]models.PostTarget
	postStatus models.PostStatus
	updates    int
}

func newAggregateWorkerStore() *aggregateWorkerStore {
	return &aggregateWorkerStore{
		targets: map[int64]models.PostTarget{
			1: {ID: 1, PostID: 100, PlatformAccountID: 10, Status: models.PostStatusQueued},
			2: {ID: 2, PostID: 100, PlatformAccountID: 11, Status: models.PostStatusQueued},
		},
		postStatus: models.PostStatusQueued,
	}
}

func (s *aggregateWorkerStore) ListPending(time.Time) ([]models.PostTarget, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]models.PostTarget, 0, len(s.targets))
	for _, target := range s.targets {
		if target.Status == models.PostStatusQueued {
			out = append(out, target)
		}
	}
	return out, nil
}

func (s *aggregateWorkerStore) MarkRateLimitedRetry(int64, time.Time, string) error {
	return nil
}

func (s *aggregateWorkerStore) FindByID(int64) (*models.Post, error) {
	return &models.Post{ID: 100, Caption: "aggregate test", MediaURL: "https://cdn.example/video.mp4"}, nil
}

func (s *aggregateWorkerStore) ClaimQueuedTargetWithLease(id int64, _ string, _ time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	target, ok := s.targets[id]
	if !ok || target.Status != models.PostStatusQueued {
		return false, nil
	}
	target.Status = models.PostStatusPublishing
	s.targets[id] = target
	s.postStatus = s.resolveLocked()
	return true, nil
}

func (s *aggregateWorkerStore) UpdateStatus(target *models.PostTarget) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.targets[target.ID]
	if !ok {
		return repository.ErrPostTargetNotFound
	}
	if current.Status.IsTerminal() && current.Status != target.Status {
		return errors.Join(repository.ErrPostTargetTransitionStale, errors.New("terminal target cannot regress"))
	}
	current.Status = target.Status
	current.PlatformPostID = target.PlatformPostID
	current.PublishedAt = target.PublishedAt
	current.ErrorMessage = target.ErrorMessage
	s.targets[target.ID] = current
	s.postStatus = s.resolveLocked()
	s.updates++
	return nil
}

func (s *aggregateWorkerStore) SetProviderIdempotencyKey(int64, string) error { return nil }
func (s *aggregateWorkerStore) GetMetadata(int64) (json.RawMessage, error)    { return nil, nil }
func (s *aggregateWorkerStore) SetTargetCanaryVideoID(int64, string) error    { return nil }

func (s *aggregateWorkerStore) resolveLocked() models.PostStatus {
	targets := make([]models.PostTarget, 0, len(s.targets))
	for _, target := range s.targets {
		targets = append(targets, target)
	}
	status, err := models.NewPostAggregateStatusResolver().Resolve(targets)
	if err != nil {
		panic(err)
	}
	return status
}

func TestPublishWorkers_ConcurrentTransitionsPreserveTerminalAggregate(t *testing.T) {
	store := newAggregateWorkerStore()
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: id, Platform: "instagram", PlatformUserID: "account"}, nil
		},
	}
	provider := &mockProvider{
		baseMockProvider: baseMockProvider{platform: "instagram"},
		publishFn: func(context.Context, string, string, models.PublishPayload) (*models.PublishResult, error) {
			return &models.PublishResult{PlatformMediaID: "remote"}, nil
		},
	}
	newVault := func() *mockCredentialVault {
		return &mockCredentialVault{
			renewFn: func(context.Context, int64, string, credentials.TokenRefresher) (*models.OAuthToken, error) {
				return &models.OAuthToken{AccessToken: "token"}, nil
			},
		}
	}
	workerA := newTestWorkerWithoutThrottle(store, users, "instagram", provider, newVault())
	workerB := newTestWorkerWithoutThrottle(store, users, "instagram", provider, newVault())

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := workerA.publishTarget(context.Background(), &models.PostTarget{ID: 1, PostID: 100, PlatformAccountID: 10, Status: models.PostStatusQueued}); err != nil {
			t.Errorf("worker A publishTarget: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := workerB.publishTarget(context.Background(), &models.PostTarget{ID: 2, PostID: 100, PlatformAccountID: 11, Status: models.PostStatusQueued}); err != nil {
			t.Errorf("worker B publishTarget: %v", err)
		}
	}()
	wg.Wait()

	store.mu.Lock()
	postStatus := store.postStatus
	target1 := store.targets[1].Status
	target2 := store.targets[2].Status
	updates := store.updates
	store.mu.Unlock()
	if target1 != models.PostStatusPublished || target2 != models.PostStatusPublished {
		t.Fatalf("target statuses = (%q, %q), want both published", target1, target2)
	}
	if postStatus != models.PostStatusPublished {
		t.Fatalf("parent status = %q, want published", postStatus)
	}
	if updates != 2 {
		t.Fatalf("UpdateStatus calls = %d, want 2", updates)
	}

	// A stale worker completion must not regress a terminal target or its
	// already-terminal parent aggregate.
	err := store.UpdateStatus(&models.PostTarget{ID: 1, PostID: 100, Status: models.PostStatusPublishing})
	if !errors.Is(err, repository.ErrPostTargetTransitionStale) {
		t.Fatalf("stale transition error = %v, want ErrPostTargetTransitionStale", err)
	}
	store.mu.Lock()
	deferredStatus := store.postStatus
	store.mu.Unlock()
	if deferredStatus != models.PostStatusPublished {
		t.Fatalf("parent after stale transition = %q, want published", deferredStatus)
	}
}
