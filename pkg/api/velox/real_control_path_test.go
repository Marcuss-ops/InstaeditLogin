package velox

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/veloxclient"
)

// TestRealBFFControlPath exercises Browser → InstaEdit BFF → Velox's
// canonical /api/v1/instaedit/jobs boundary with the real HTTP client and
// signed control JWT. The upstream is intentionally a narrow HTTP fake: it
// proves the wire/auth ownership without requiring a running Velox process.
func TestRealBFFControlPath(t *testing.T) {
	const secret = "01234567890123456789012345678901"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/instaedit/jobs" {
			t.Fatalf("upstream path = %s %s", r.Method, r.URL.Path)
		}
		token := r.Header.Get("Authorization")
		if len(token) < len("Bearer ") || token[:len("Bearer ")] != "Bearer " {
			t.Fatal("missing control JWT")
		}
		claims := jwt.MapClaims{}
		parsed, err := jwt.ParseWithClaims(token[len("Bearer "):], claims, func(tok *jwt.Token) (any, error) {
			if tok.Method != jwt.SigningMethodHS256 {
				t.Fatalf("unexpected JWT method %v", tok.Method)
			}
			return []byte(secret), nil
		})
		if err != nil || !parsed.Valid {
			t.Fatalf("invalid control JWT: %v", err)
		}
		if claims["iss"] != "instaedit" || claims["aud"] != "velox" || claims["workspace_id"] != float64(testWSID) {
			t.Fatalf("unexpected identity claims: %#v", claims)
		}
		if scopes, ok := claims["scopes"].([]any); !ok || len(scopes) != 1 || scopes[0] != veloxclient.ScopeVeloxJobsWrite {
			t.Fatalf("unexpected job scopes: %#v", claims["scopes"])
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode upstream body: %v", err)
		}
		for _, forbidden := range []string{"workspace_id", "user_id"} {
			if _, present := body[forbidden]; present {
				t.Fatalf("%s must be JWT-only, body=%#v", forbidden, body)
			}
		}
		if body["contract_version"] != "velox.job.v1" || body["idempotency_key"] != "instaedit:workspace_42:request_abc" {
			t.Fatalf("canonical job identity missing: %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "job_123", "workspace_id": testWSID, "project_id": "project_123", "render_status": "PENDING",
		})
	}))
	defer upstream.Close()

	client := veloxclient.New(upstream.URL, secret)
	mux := chi.NewRouter()
	Register(mux, Deps{Client: client, AuthMiddleware: stubAuth})
	w := do(t, mux, http.MethodPost, "/api/v1/velox/jobs", `{"contract_version":"velox.job.v1","idempotency_key":"instaedit:workspace_42:request_abc","project_id":"project_123","render_spec":{"scenes":[{"text":"hello"}],"voiceover_paths":["velox-asset://audio"]},"delivery_plan":{"destinations":[{"external_destination_id":"extdst_abc","metadata":{"title":"Hello"}}]}}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("BFF status = %d, body=%s", w.Code, w.Body.String())
	}
}
