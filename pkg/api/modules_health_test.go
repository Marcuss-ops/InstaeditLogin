package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestHealthModule_MountsPublicHealthAndReadyRoutes(t *testing.T) {
	mux := chi.NewRouter()
	module := NewHealthModule(HealthModuleDeps{
		Health: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		Ready: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusAccepted)
		},
	})
	module.Register(mux)

	tests := []struct {
		name string
		path string
		want int
	}{
		{name: "health", path: "/api/v1/health", want: http.StatusNoContent},
		{name: "ready", path: "/ready", want: http.StatusAccepted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if rec.Code != tt.want {
				t.Fatalf("GET %s: want %d, got %d", tt.path, tt.want, rec.Code)
			}
		})
	}
}

func TestHealthModuleDoesNotMountNonGetMethods(t *testing.T) {
	mux := chi.NewRouter()
	module := NewHealthModule(HealthModuleDeps{
		Health: func(http.ResponseWriter, *http.Request) {},
		Ready:  func(http.ResponseWriter, *http.Request) {},
	})
	module.Register(mux)

	for _, path := range []string{"/api/v1/health", "/ready"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s: want %d, got %d", path, http.StatusMethodNotAllowed, rec.Code)
		}
	}
}
