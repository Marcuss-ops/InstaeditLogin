package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/agenttools"
	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
)

// AgentRunRecoveryWorker polls durable agent runs independently of agent
// clients. Each tick uses the same recovery handler as the public API, so
// progress snapshots, final artifact ingestion and calendar post creation
// follow one implementation after restarts.
type AgentRunRecoveryWorker struct {
	module   *AgentRunsModule
	interval time.Duration
	logger   *slog.Logger
}

func (r *Router) NewAgentRunRecoveryWorker(interval time.Duration) *AgentRunRecoveryWorker {
	if r == nil || r.agentRunStore == nil || r.jobMasterClient == nil {
		return nil
	}
	module := &AgentRunsModule{deps: AgentRunsModuleDeps{
		Store:          r.agentRunStore,
		Catalog:        agenttoolsCatalog(),
		JobMaster:      r.jobMasterClient,
		VideoPublisher: newAgentVideoPublisher(r.mediaStore, r.storageProvider, r.postStore, r.workspaceStore, r.teamStore, r.idempotencyStore, r.maxUploadBytes, r.publishHorizonDays()),
	}}
	if interval <= 0 {
		interval = 3 * time.Second
	}
	return &AgentRunRecoveryWorker{module: module, interval: interval, logger: slog.Default()}
}

// agenttoolsCatalog is kept in a tiny helper so the recovery worker doesn't
// expose the implementation catalog as mutable runtime state.
func agenttoolsCatalog() agenttools.Catalog { return agenttools.NewCatalog() }

func (w *AgentRunRecoveryWorker) RunOnce(ctx context.Context) error {
	if w == nil || w.module == nil {
		return nil
	}
	runs, err := w.module.deps.Store.ListRecoverableRuns(ctx, 100)
	if err != nil {
		return err
	}
	for _, run := range runs {
		if run.ActorUserID <= 0 {
			w.logger.Warn("agent recovery skipped run without actor user", "run_id", run.ID)
			continue
		}
		identity := auth.NewUserIdentity(run.ActorUserID, run.WorkspaceID, 0)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/runs/"+url.PathEscape(run.ID)+"/recovery", nil)
		req = req.WithContext(auth.WithIdentity(ctx, identity))
		response := httptest.NewRecorder()
		w.module.handleRecovery(response, req)
		if response.Code >= http.StatusInternalServerError {
			w.logger.Warn("agent run recovery poll failed", "run_id", run.ID, "status", response.Code)
		}
	}
	return nil
}

func (w *AgentRunRecoveryWorker) Run(ctx context.Context) error {
	if w == nil {
		return nil
	}
	if err := w.RunOnce(ctx); err != nil {
		w.logger.Error("initial agent recovery tick failed", "error", err)
	}
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := w.RunOnce(ctx); err != nil {
				w.logger.Error("agent recovery tick failed", "error", err)
			}
		}
	}
}
