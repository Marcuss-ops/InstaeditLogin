package api

import (
	"context"
	"net/http"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

type YouTubeCopyrightAlertStore interface {
	ListCopyrightAlertsByWorkspace(ctx context.Context, workspaceIDs []int64) ([]models.YouTubeCopyrightAlert, error)
}

func (r *Router) handleListYouTubeCopyrightAlerts(w http.ResponseWriter, req *http.Request) {
	if r.youtubeCopyrightAlertStore == nil || r.workspaceStore == nil {
		writeError(w, http.StatusNotImplemented, "youtube copyright alerts are not configured")
		return
	}
	userID, ok := requireUserID(w, req, r)
	if !ok {
		return
	}
	workspaces, err := r.workspaceStore.ListByOwner(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list workspaces: "+err.Error())
		return
	}
	ids := make([]int64, 0, len(workspaces))
	for _, workspace := range workspaces {
		if workspace.ID > 0 {
			ids = append(ids, workspace.ID)
		}
	}
	alerts, err := r.youtubeCopyrightAlertStore.ListCopyrightAlertsByWorkspace(req.Context(), ids)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list youtube copyright alerts: "+err.Error())
		return
	}
	if alerts == nil {
		alerts = []models.YouTubeCopyrightAlert{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"alerts": alerts})
}
