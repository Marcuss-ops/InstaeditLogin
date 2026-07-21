package api

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
)

// ----------------------------------------------------------------------- Handlers

func (r *Router) handleHealth(w http.ResponseWriter, req *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":    "ok",
		"service":   "InstaEditLogin",
		"version":   "2.0.0",
		"platforms": r.capabilities.Names(),
	})
}

// ----------------------------------------------------------------------- Middleware

func (r *Router) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		slog.Info("HTTP request", "method", req.Method, "path", req.URL.Path, "remote_addr", req.RemoteAddr)
		next.ServeHTTP(w, req)
	})
}

func (r *Router) handleMetrics(w http.ResponseWriter, req *http.Request) {
	user := os.Getenv("METRICS_BASIC_AUTH_USER")
	pass := os.Getenv("METRICS_BASIC_AUTH_PASS")
	// Fail-closed: the endpoint is public only when NO credentials
	// are configured. If either env var is missing, require auth so
	// the metrics surface is never accidentally exposed.
	if user == "" || pass == "" {
		w.Header().Set("WWW-Authenticate", `Basic realm="metrics", charset="UTF-8"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	u, p, ok := req.BasicAuth()
	if !ok || subtle.ConstantTimeCompare([]byte(u), []byte(user)) != 1 || subtle.ConstantTimeCompare([]byte(p), []byte(pass)) != 1 {
		w.Header().Set("WWW-Authenticate", `Basic realm="metrics", charset="UTF-8"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	metrics.Handler().ServeHTTP(w, req)
}

func (r *Router) corsMiddleware(next http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(r.allowedOrigin))
	for _, o := range r.allowedOrigin {
		allowed[strings.TrimSpace(o)] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if origin := req.Header.Get("Origin"); origin != "" {
			if _, ok := allowed[origin]; ok {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				// Taglio 1.2: include Cookie so the browser is allowed to
				// send the HttpOnly session cookie. Access-Control-Allow-Credentials
				// is required when the browser uses credentials:'include'.
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Cookie, X-CSRF-Token")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Max-Age", "600")
			}
		}
		if req.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, req)
	})
}

// securityHeadersMiddleware applies the standard hardened HTTP response
// headers to every response (defence-in-depth on top of whatever the
// upstream proxy/CDN also sets). The choices:
//
//   - default-src 'none' on Content-Security-Policy is the strict
//     default for an API-only JSON server: it blocks scripts,
//     styles, images, fonts, media, frames from any source unless
//     explicitly allowed. It also forbids <form> submissions to
//     third parties (form-action 'none') and embeds (frame-ancestors).
//     The SPA's index.html is served from the static host (Vite dev
//     / Vercel in prod), NOT from this server, so the SPA's CSP is
//     NOT here — its index.html / vercel.json / Nginx header config is
//     what carries the SPA-relevant CSP. This server only needs CSP
//     because some endpoints return redirect responses (OAuth
//     callback → /auth/callback redirect) and a redirect from a
//     strict-CSP origin shouldn't become a script-execution vector.
//   - X-Content-Type-Options: nosniff blocks MIME-sniffing (mostly
//     cosmetic for a JSON server but it's a single header so apply).
//   - X-Frame-Options: DENY blocks iframe embedding of API routes
//     (defence vs clickjacking if a malicious 3p page tries to load
//     our JSON responses in an iframe to read cross-origin responses
//     via same-origin network errors).
//   - Referrer-Policy: strict-origin-when-cross-origin keeps the
//     Referer header trustworthy but doesn't leak full paths.
//   - Strict-Transport-Security is ONLY emitted when the request
//     arrived over HTTPS (TLS or via a known TLS-terminating proxy:
//     Fly / Render / Cloudflare all set the X-Forwarded-Proto=https
//     header). HSTS over plain HTTP would break the connection.
//
// Placed OUTSIDE CORS / rate-limit so the headers apply to every
// response regardless of those middleware short-circuits. Placed
// INSIDE recover so a panic during header-writing is still caught
// (the headers will be reset by writeJSON 500 below).
func (r *Router) securityHeadersMiddleware(next http.Handler) http.Handler {
	apiCSP := strings.Join([]string{
		"default-src 'none'",
		"frame-ancestors 'none'",
		"form-action 'none'",
		"base-uri 'none'",
	}, "; ")
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", apiCSP)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		if isTLSRequest(req) {
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		next.ServeHTTP(w, req)
	})
}

// isTLSRequest reports whether the request reached the server over an
// encrypted transport. Falls back to X-Forwarded-Proto when TLS is
// terminated upstream (every managed deploy we ship uses one). This
// is the gate for the HSTS header so a plain-HTTP sandbox doesn't
// advertise a permanent HTTPS-only contract to browsers.
func isTLSRequest(req *http.Request) bool {
	if req.TLS != nil {
		return true
	}
	if p := req.Header.Get("X-Forwarded-Proto"); p != "" {
		pp := strings.ToLower(strings.TrimSpace(p))
		if i := strings.Index(pp, ","); i > 0 {
			pp = strings.TrimSpace(pp[:i])
		}
		return pp == "https"
	}
	if strings.EqualFold(req.Header.Get("X-Forwarded-Ssl"), "on") {
		return true
	}
	return false
}
