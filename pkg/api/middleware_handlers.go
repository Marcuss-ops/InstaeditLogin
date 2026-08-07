package api

import (
	"bufio"
	"crypto/subtle"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
)

// ----------------------------------------------------------------------- Handlers

func (r *Router) handleHealth(w http.ResponseWriter, req *http.Request) {
	// Blocco #2 P0 — surface the publish horizon + retention buffer
	// to the SPA via /api/v1/health. The frontend's calendar widget
	// renders the unique-of-both knobs as "max horizon: 30 days" /
	// "asset retention: 7 days past publish", and a single
	// GET /api/v1/health on dashboard mount is enough (cheap, public,
	// already hit by the SPA's status ping). The `limits` envelope
	// is nested so future knobs (max upload bytes, chunk size, etc.)
	// can sit alongside without changing the SPA's wire parsing.
	//
	// The endpoint stays "liveness"-shaped (status, platforms)
	// — the new `limits` block is operator-facing config, NOT a
	// readiness signal. /ready remains the canonical readiness
	// surface (DB ping + migrations).
	publishHorizonDays := r.scheduleLimits.PublishHorizonDays
	if publishHorizonDays <= 0 {
		publishHorizonDays = 30
	}
	retentionBufferDays := r.scheduleLimits.VideoRetentionBufferDays
	if retentionBufferDays <= 0 {
		retentionBufferDays = 7
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":    "ok",
		"service":   "InstaEditLogin",
		"version":   "2.0.0",
		"platforms": r.capabilities.Names(),
		"limits": map[string]int{
			"publish_horizon_days":        publishHorizonDays,
			"video_retention_buffer_days": retentionBufferDays,
		},
	})
}

// ----------------------------------------------------------------------- Middleware

func (r *Router) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		slog.Info("HTTP request", "method", req.Method, "path", req.URL.Path, "remote_addr", req.RemoteAddr)
		next.ServeHTTP(w, req)
	})
}

// metricsMiddleware measures the complete request, including SQL work
// performed by repositories. Route labels use chi's matched pattern rather
// than the raw URL so IDs cannot create unbounded Prometheus series.
func (r *Router) metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		stats := metrics.NewRequestStats()
		start := time.Now()
		rw := &measurementResponseWriter{ResponseWriter: w}
		req = req.WithContext(metrics.WithRequestStats(req.Context(), stats))
		defer func() {
			recovered := recover()
			if recovered != nil {
				rw.status = http.StatusInternalServerError
			}
			route := "unknown"
			if pattern := chiRoutePattern(req); pattern != "" {
				route = pattern
			}
			status := rw.status
			if status == 0 {
				status = http.StatusOK
			}
			metrics.ObserveHTTPRequestDetails(route, req.Method, strconv.Itoa(status), time.Since(start).Seconds(), int64(rw.bytes), stats)
			if recovered != nil {
				panic(recovered)
			}
		}()
		next.ServeHTTP(rw, req)
	})
}

type measurementResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *measurementResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *measurementResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += n
	return n, err
}

func (w *measurementResponseWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *measurementResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

func (w *measurementResponseWriter) Push(target string, opts *http.PushOptions) error {
	if pusher, ok := w.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, opts)
	}
	return http.ErrNotSupported
}

func (w *measurementResponseWriter) ReadFrom(src io.Reader) (int64, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if readerFrom, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		n, err := readerFrom.ReadFrom(src)
		w.bytes += int(n)
		return n, err
	}
	n, err := io.Copy(w.ResponseWriter, src)
	w.bytes += int(n)
	return n, err
}

func (w *measurementResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func chiRoutePattern(req *http.Request) string {
	if req == nil {
		return ""
	}
	if ctx := chi.RouteContext(req.Context()); ctx != nil {
		if pattern := ctx.RoutePattern(); pattern != "" {
			return pattern
		}
	}
	return "unknown"
}

// MetricsHandler returns the /metrics HTTP handler gated by the
// supplied basic-auth credentials. If either credential is empty the
// handler is fail-closed and returns 503 Service Unavailable, so a
// misconfigured process never exposes metrics publicly. Invalid
// credentials return 401 as usual. Exported so the optional internal
// metrics listener can reuse the same handler.
func MetricsHandler(user, pass string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Fail-closed: missing or incomplete credentials mean the
		// operator has not configured metrics auth. Serving metrics
		// publicly would leak operational data; return 503 so the
		// scraper alerts and the endpoint cannot be accidentally probed.
		if user == "" || pass == "" {
			w.Header().Set("WWW-Authenticate", `Basic realm="metrics", charset="UTF-8"`)
			http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
			return
		}
		u, p, ok := req.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(u), []byte(user)) != 1 || subtle.ConstantTimeCompare([]byte(p), []byte(pass)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="metrics", charset="UTF-8"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		metrics.Handler().ServeHTTP(w, req)
	})
}

func (r *Router) handleMetrics(w http.ResponseWriter, req *http.Request) {
	MetricsHandler(r.metricsUser, r.metricsPass).ServeHTTP(w, req)
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
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Cookie, X-CSRF-Token, Idempotency-Key")
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
//     Caddy / Render / Cloudflare all set the X-Forwarded-Proto=https
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
		if r.isTLSRequest(req) {
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		next.ServeHTTP(w, req)
	})
}

// isTLSRequest reports whether the request reached the server over an
// encrypted transport. Falls back to X-Forwarded-Proto/X-Forwarded-Ssl
// only when the immediate peer is a configured trusted proxy. Direct
// clients can therefore not spoof HTTPS by sending forwarded headers.
func (r *Router) isTLSRequest(req *http.Request) bool {
	if req.TLS != nil {
		return true
	}

	peer := peerHost(req.RemoteAddr)
	if !isTrustedProxy(peer, r.trustedProxies) {
		return false
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
