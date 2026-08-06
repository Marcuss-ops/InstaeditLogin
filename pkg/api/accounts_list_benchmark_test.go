package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// benchmarkListAccounts measures the aggregated GET /api/v1/accounts
// handler path with N accounts, each joined with a snapshot row. The
// repository is a fake returning the rows directly, so the benchmark
// isolates the handler work (pagination bounds, per-account state
// classification, stale stamping, JSON encoding). The DB side of the
// same page load is exactly ONE indexed LEFT JOIN query — the handler
// cost is the only part measurable offline here. Numbers feed the
// N+1 DoD verification (docs/NPLUS1_DOD_VERIFICATION.md): 100 accounts
// must be served well under the 2s page-load budget, p95 target
// 300-500ms.
func benchmarkListAccounts(b *testing.B, n int) {
	now := time.Now()
	rows := make([]*repository.AccountWithSnapshot, 0, n)
	for i := 1; i <= n; i++ {
		rows = append(rows, &repository.AccountWithSnapshot{
			Account: &models.PlatformAccount{
				ID:             int64(i),
				UserID:         7,
				Platform:       models.PlatformYouTube,
				PlatformUserID: fmt.Sprintf("UC-%d", i),
				Username:       fmt.Sprintf("channel-%d", i),
				Status:         models.AccountStatusActive,
				CreatedAt:      now,
				UpdatedAt:      now,
			},
			// 5-minute-old snapshot: stale or fresh depending on the configured
			// max-age TTL (jittered per release) — harmless either way, since
			// the mock snapshot store no-ops the stale batch-mark path. The
			// point is measuring the list+encode cost, not the stale-marking.
			Snapshot: &repository.AccountResourceSnapshot{
				PlatformAccountID: int64(i),
				FetchedAt:         now.Add(-5 * time.Minute),
				Profile:           map[string]any{"avatar_url": fmt.Sprintf("https://avatars/%d", i)},
			},
		})
	}
	store := &mockUserStore{
		listWithSnapshotsFn: func(userID int64, platform string) ([]*repository.AccountWithSnapshot, error) {
			return rows, nil
		},
	}
	r := newTestRouter(
		&mockProvider{platform: models.PlatformYouTube},
		store,
		"",
		WithSnapshotStore(&mockSnapshotStore{}),
	)

	// Call handleListAccounts directly (identity pre-injected) so the
	// benchmark measures the handler, not the router's logging/rate-limit
	// middleware. Identity mirrors the JWT the protected middleware would
	// have attached in production.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
	req = req.WithContext(auth.WithIdentity(req.Context(), auth.NewUserIdentity(7, 1, 1)))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		r.handleListAccounts(w, req)
		if w.Code != http.StatusOK {
			b.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
	}
}

func BenchmarkHandleListAccounts_10(b *testing.B)  { benchmarkListAccounts(b, 10) }
func BenchmarkHandleListAccounts_50(b *testing.B)  { benchmarkListAccounts(b, 50) }
func BenchmarkHandleListAccounts_100(b *testing.B) { benchmarkListAccounts(b, 100) }
func BenchmarkHandleListAccounts_200(b *testing.B) { benchmarkListAccounts(b, 200) }
