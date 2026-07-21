package api

import (
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

func logAndError(w http.ResponseWriter, msg string, err error, kv ...any) {
	slog.Error(msg, append([]any{"error", err}, kv...)...)
	writeError(w, http.StatusInternalServerError, msg+": "+err.Error())
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
