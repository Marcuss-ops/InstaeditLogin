package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCorsMiddleware_AllowMethodsIncludesPutPatchDelete(t *testing.T) {
	r := newCORSTestRouter([]string{"https://instaedit.org"})

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/workspaces/123", nil)
	req.Header.Set("Origin", "https://instaedit.org")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("preflight status: want 204, got %d", w.Code)
	}

	methods := w.Header().Get("Access-Control-Allow-Methods")
	for _, want := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
		if !strings.Contains(methods, want) {
			t.Errorf("Access-Control-Allow-Methods %q missing %q (browser preflight for %s will fail in production)", methods, want, want)
		}
	}
}

func TestCorsMiddleware_AllowHeadersIncludesCSRFToken(t *testing.T) {
	r := newCORSTestRouter([]string{"https://instaedit.org"})

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/workspaces/123", nil)
	req.Header.Set("Origin", "https://instaedit.org")
	req.Header.Set("Access-Control-Request-Headers", "content-type, x-csrf-token")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("preflight status: want 204, got %d", w.Code)
	}

	allowed := w.Header().Get("Access-Control-Allow-Headers")
	for _, want := range []string{"Content-Type", "X-CSRF-Token"} {
		if !strings.Contains(strings.ToLower(allowed), strings.ToLower(want)) {
			t.Errorf("Access-Control-Allow-Headers %q missing %q (browser preflight for mutative requests will fail in production)", allowed, want)
		}
	}
}
