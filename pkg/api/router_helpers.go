package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
)

// chain composes a list of middlewares around a final handler.
// chain(h, m1, m2) yields m1(m2(h)) — the first arg is the
// innermost handler, subsequent args wrap it in order. No-op
// identity when no middlewares are supplied.
func chain(handler http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	composed := handler
	// Apply in reverse so the first middleware in the slice is
	// the outermost wrapper at request time.
	for i := len(mws) - 1; i >= 0; i-- {
		composed = mws[i](composed)
	}
	return composed
}

// ----------------------------------------------------------------------- Helpers

func parsePathIDAsInt64(w http.ResponseWriter, req *http.Request, paramName string) (int64, bool) {
	s := req.PathValue(paramName)
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		writeError(w, http.StatusBadRequest, "invalid "+paramName+": "+s)
		return 0, false
	}
	return n, true
}

func requireUserID(w http.ResponseWriter, req *http.Request, r *Router) (int64, bool) {
	uid, ok := auth.UserIDFromContext(req.Context())
	if !ok || uid <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return 0, false
	}
	return uid, true
}

type requestIDCtxKey struct{}

func requestIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDCtxKey{}).(string); ok {
		return id
	}
	return ""
}

func withRequestID(req *http.Request, id string) *http.Request {
	ctx := context.WithValue(req.Context(), requestIDCtxKey{}, id)
	return req.WithContext(ctx)
}

func generateRequestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b)
}

// isValidRequestID validates that id is safe to propagate: printable
// ASCII without spaces, at most 64 characters. Any untrusted value
// that fails validation is ignored and a fresh id is generated.
func isValidRequestID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

// logAndError logs the full error with the per-request request_id and
// returns a generic 500 response to the client. The client message
// never contains err.Error() to avoid leaking internal details.
func logAndError(w http.ResponseWriter, req *http.Request, msg string, err error, kv ...any) {
	var reqID string
	if req != nil {
		reqID = requestIDFromContext(req.Context())
	}
	if reqID == "" {
		reqID = generateRequestID()
	}
	slog.Error(msg, append([]any{"error", err, "request_id", reqID}, kv...)...)
	writeJSON(w, http.StatusInternalServerError, map[string]any{
		"error":      "Internal Server Error",
		"request_id": reqID,
	})
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
