package bootstrap

import (
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/worker"
)

func TestWorkerSpecs_PreserveLifecycleContract(t *testing.T) {
	app := &App{}
	specs := app.workerSpecs()

	wantNames := []string{
		"publish",
		"reconcile",
		"outbox",
		"webhook",
		"metrics",
		"sessions_cleanup",
		"asset_cleanup",
		"velox_downloader",
		"upload",
		"drive_batch_crawler",
		"youtube_processing_reconciler",
		"token_refresh_sweep",
		"snapshot_refresh_sweep",
	}
	if len(specs) != len(wantNames) {
		t.Fatalf("worker count = %d, want %d", len(specs), len(wantNames))
	}
	for i, spec := range specs {
		if spec.Name != wantNames[i] {
			t.Errorf("worker %d = %q, want %q", i+1, spec.Name, wantNames[i])
		}
		if spec.Run == nil {
			t.Errorf("worker %q has nil Run factory", spec.Name)
		}
	}

	for _, name := range []string{"asset_cleanup", "token_refresh_sweep", "snapshot_refresh_sweep"} {
		for _, spec := range specs {
			if spec.Name == name && spec.Critical {
				t.Errorf("maintenance worker %q must remain non-critical", name)
			}
		}
	}
	for _, spec := range specs {
		if spec.Name != "asset_cleanup" && spec.Name != "token_refresh_sweep" && spec.Name != "snapshot_refresh_sweep" && !spec.Critical {
			t.Errorf("pipeline worker %q unexpectedly became non-critical", spec.Name)
		}
	}
}

func TestRegisterWorkerSpecs_WiresRegistryWithoutStartingWorkers(t *testing.T) {
	registry := worker.NewRegistry()
	app := &App{WorkerRegistry: registry}

	specs := app.registerWorkerSpecs()
	statuses := registry.GetStatus()
	if len(statuses) != len(specs) {
		t.Fatalf("registered worker count = %d, want %d", len(statuses), len(specs))
	}

	byName := make(map[string]worker.WorkerStatus, len(statuses))
	for _, status := range statuses {
		byName[status.Name] = status
	}
	for _, spec := range specs {
		status, ok := byName[spec.Name]
		if !ok {
			t.Fatalf("worker %q was not registered", spec.Name)
		}
		if status.Critical != spec.Critical {
			t.Errorf("worker %q criticality = %v, want %v", spec.Name, status.Critical, spec.Critical)
		}
	}
}
