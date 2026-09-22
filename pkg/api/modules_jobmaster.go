package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/jobmaster"
)

// JobMasterModuleDeps wires the provider-neutral execution-plane proxy. The
// browser only sees these authenticated BFF routes; the M2M secret remains in
// the API process.
type JobMasterModuleDeps struct {
	Client         jobmaster.API
	AuthMiddleware func(http.Handler) http.Handler
	CSRFMiddleware func(http.Handler) http.Handler
}

type jobMasterModule struct{ deps JobMasterModuleDeps }

func NewJobMasterModule(deps JobMasterModuleDeps) RouteModule {
	return &jobMasterModule{deps: deps}
}

func (m *jobMasterModule) Register(mux chi.Router) {
	if m.deps.Client == nil {
		return
	}
	wrap := func(next http.HandlerFunc) http.Handler {
		var handler http.Handler = next
		if m.deps.CSRFMiddleware != nil {
			handler = m.deps.CSRFMiddleware(handler)
		}
		if m.deps.AuthMiddleware != nil {
			handler = m.deps.AuthMiddleware(handler)
		}
		return handler
	}
	mux.Method(http.MethodGet, "/api/v1/automation/jobs/types", wrap(m.listTypes))
	mux.Method(http.MethodPost, "/api/v1/automation/jobs", wrap(m.submit))
	mux.Method(http.MethodGet, "/api/v1/automation/jobs/{id}", wrap(m.get))
}

func (m *jobMasterModule) listTypes(w http.ResponseWriter, req *http.Request) {
	if !requireJobMasterIdentity(w, req) {
		return
	}
	body, err := m.deps.Client.ListTypes(req.Context())
	if err != nil {
		writeJobMasterError(w, err)
		return
	}
	writeRawJSON(w, http.StatusOK, body)
}

func (m *jobMasterModule) submit(w http.ResponseWriter, req *http.Request) {
	if !requireJobMasterIdentity(w, req) {
		return
	}
	var input jobmaster.SubmitRequest
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid job request: " + err.Error()})
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid job request: multiple JSON values"})
		return
	}
	input.Type = strings.TrimSpace(input.Type)
	input.Project = strings.TrimSpace(input.Project)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.Type == "" || input.Project == "" || input.IdempotencyKey == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "type, project, and idempotency_key are required"})
		return
	}
	body, err := m.deps.Client.Submit(req.Context(), input)
	if err != nil {
		writeJobMasterError(w, err)
		return
	}
	writeRawJSON(w, http.StatusAccepted, body)
}

func (m *jobMasterModule) get(w http.ResponseWriter, req *http.Request) {
	if !requireJobMasterIdentity(w, req) {
		return
	}
	body, err := m.deps.Client.Get(req.Context(), chi.URLParam(req, "id"))
	if err != nil {
		writeJobMasterError(w, err)
		return
	}
	writeRawJSON(w, http.StatusOK, body)
}

func requireJobMasterIdentity(w http.ResponseWriter, req *http.Request) bool {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing identity"})
		return false
	}
	if identity.UserID() <= 0 || identity.WorkspaceID() <= 0 {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "missing workspace scope"})
		return false
	}
	return true
}

func writeRawJSON(w http.ResponseWriter, status int, body json.RawMessage) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func writeJobMasterError(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	switch {
	case errors.Is(err, jobmaster.ErrInvalidJobID):
		status = http.StatusBadRequest
	case errors.Is(err, jobmaster.ErrNotConfigured):
		status = http.StatusServiceUnavailable
	default:
		var upstream *jobmaster.HTTPError
		if errors.As(err, &upstream) {
			switch upstream.StatusCode {
			case http.StatusNotFound:
				status = http.StatusNotFound
			case http.StatusUnprocessableEntity:
				status = http.StatusUnprocessableEntity
			case http.StatusConflict:
				status = http.StatusConflict
			}
		}
	}
	writeJSON(w, status, map[string]string{"error": "job master request failed"})
}
