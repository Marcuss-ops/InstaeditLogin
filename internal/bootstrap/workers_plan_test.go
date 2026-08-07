package bootstrap

import (
	"testing"
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
