package api

import (
	"net/http"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// AdminHealthResponse is the JSON body for GET /admin/health.
// Includes YouTube quota estimate (D2.b derived), 1h + 24h
// per-channel error rate (D5.a), and headline queue + retry counts.
type AdminHealthResponse struct {
	YouTubeQuota repository.AdminYouTubeQuota   `json:"youtube_quota_estimate"`
	ErrorRate1h  []repository.AdminErrorRateRow `json:"error_rate_1h"`
	ErrorRate24h []repository.AdminErrorRateRow `json:"error_rate_24h"`
	QueueCounts  repository.AdminQueueCounts    `json:"queue_counts"`
	Generated    int64                          `json:"generated_at_unix"`
}

// handleAdminHealth (GET /admin/health) returns the cross-cutting
// health surface. Three queries: YouTube quota (D2.b derived via
// post_targets audit trail), 1h error rate per channel (D5.a
// short-window), 24h error rate per channel (D5.a chronic-window).
// Plus the existing queue counts so the dashboard renders a single
// roundtrip's worth of gauges.
func (r *Router) handleAdminHealth(w http.ResponseWriter, req *http.Request) {
	if r.adminStore == nil {
		writeError(w, http.StatusNotImplemented, "admin store not configured")
		return
	}

	quota, err := r.adminStore.YouTubeQuotaApproximation(req.Context(), 24*time.Hour, 10000, 1)  // 2026 bucket model: 1 unit per videos.insert
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load youtube quota: "+err.Error())
		return
	}
	errRate1h, err := r.adminStore.ErrorRatePerChannel(req.Context(), "1 hours", "1h", 200)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load 1h error rate: "+err.Error())
		return
	}
	errRate24h, err := r.adminStore.ErrorRatePerChannel(req.Context(), "24 hours", "24h", 200)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load 24h error rate: "+err.Error())
		return
	}
	queueCounts, err := r.adminStore.QueueCounts(req.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load queue counts: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, AdminHealthResponse{
		YouTubeQuota: quota,
		ErrorRate1h:  errRate1h,
		ErrorRate24h: errRate24h,
		QueueCounts:  queueCounts,
		Generated:    nowUnix(),
	})
}

// handleAdminHealthCSV (GET /admin/health.csv) streams the
// 1h + 24h per-channel error rates side by side (union by
// platform_account_id). Operators opening this in a spreadsheet get
// one row per channel × window; the window label is the
// disambiguator. 400 row cap keeps the file bounded; the dashboard
// paginates beyond.
func (r *Router) handleAdminHealthCSV(w http.ResponseWriter, req *http.Request) {
	if r.adminStore == nil {
		writeError(w, http.StatusNotImplemented, "admin store not configured")
		return
	}
	errRate1h, err := r.adminStore.ErrorRatePerChannel(req.Context(), "1 hours", "1h", 200)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load 1h error rate: "+err.Error())
		return
	}
	errRate24h, err := r.adminStore.ErrorRatePerChannel(req.Context(), "24 hours", "24h", 200)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load 24h error rate: "+err.Error())
		return
	}

	_, csvw, flush, err := writeAdminCSV(w, "health-error-rate")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "csv writer init failed")
		return
	}
	_ = csvw.Write([]string{
		"platform_account_id", "platform", "username",
		"window", "total_count", "failed_count", "error_rate",
	})
	for _, r := range errRate1h {
		_ = csvw.Write([]string{
			itoa(r.PlatformAccountID),
			r.Platform,
			r.Username,
			r.WindowLabel,
			itoa(int64(r.TotalCount)),
			itoa(int64(r.FailedCount)),
			ftoa(r.Rate),
		})
	}
	for _, r := range errRate24h {
		_ = csvw.Write([]string{
			itoa(r.PlatformAccountID),
			r.Platform,
			r.Username,
			r.WindowLabel,
			itoa(int64(r.TotalCount)),
			itoa(int64(r.FailedCount)),
			ftoa(r.Rate),
		})
	}
	if err := flush(); err != nil {
		slogCSVStreamError("health-error-rate", err)
	}
}
