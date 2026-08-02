package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
)

// UploadJobCountDTO is the wire shape for one entry in the dashboard
// "Programmati" widget's per-account aggregate. The dashboard hits
// this instead of fetching 200+ rows and bucketing them in JS.
type UploadJobCountDTO struct {
	AccountID     int64      `json:"account_id"`
	Count         int        `json:"count"`
	NextPublishAt *time.Time `json:"next_publish_at,omitempty"`
}

// handleUploadCounts backs GET /api/v1/uploads/counts. Returns the
// per-target rollup (count + earliest scheduled_at) driven by
// PendingCountsByAccount's single GROUP BY query. The dashboard
// widget renders THIS payload — no client-side N^2 bucketing, no
// 200-row cap hiding rows past the calendar view's limit.
//
// Authn is the JWT (no workspace scope; the WHERE is by user_id).
// When the user has no pending uploads at all, the handler returns
// an empty array so the SPA's iteration is unconditional.
func (r *Router) handleUploadCounts(w http.ResponseWriter, req *http.Request) {
	if r.uploadJobStore == nil {
		writeError(w, http.StatusNotImplemented, "upload jobs not configured on this server")
		return
	}
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}

	counts, err := r.uploadJobStore.PendingCountsByAccount(identity.UserID())
	if err != nil {
		slog.Warn("uploads counts failed", "user_id", identity.UserID(), "error", err)
		writeError(w, http.StatusInternalServerError, "could not aggregate uploads")
		return
	}
	items := make([]UploadJobCountDTO, 0, len(counts))
	for _, c := range counts {
		items = append(items, UploadJobCountDTO{
			AccountID:     c.AccountID,
			Count:         c.Count,
			NextPublishAt: c.NextPublishAt,
		})
	}
	// total_uploads is the DISTINCT row count so multi-target uploads
	// (e.g. one drive_batch row targeting FB+IG) count once on the
	// dashboard's "Pending uploads" stat instead of twice. SUM over
	// per-target counts would over-count by a factor of len(targets).
	totalUploads, err := r.uploadJobStore.PendingDistinctCount(identity.UserID())
	if err != nil {
		slog.Warn("uploads distinct count failed", "user_id", identity.UserID(), "error", err)
		writeError(w, http.StatusInternalServerError, "could not aggregate uploads")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"counts":        items,
		"total_uploads": totalUploads,
		"total_targets": len(items),
	})
}
