package api

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

func TestRouterSetup_ConcurrentCallsAreSafe(t *testing.T) {
	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
	)

	const callers = 16
	handlers := make(chan http.Handler, callers)
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			handlers <- r.Setup()
		}()
	}
	wg.Wait()
	close(handlers)

	for handler := range handlers {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("concurrent Setup handler: GET /api/v1/health status=%d, want %d", w.Code, http.StatusOK)
		}
	}
}
