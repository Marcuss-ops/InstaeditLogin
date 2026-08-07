package bootstrap

import (
	"context"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/worker"
)

func TestRunWorkersRejectsMissingRuntimeCapabilities(t *testing.T) {
	app := &App{WorkerRegistry: worker.NewRegistry()}

	err := app.RunWorkers(context.Background())
	if err == nil {
		t.Fatal("RunWorkers expected to reject an App without runtime capabilities")
	}
	if !strings.Contains(err.Error(), "capability validation failed") {
		t.Fatalf("RunWorkers error = %q, want capability validation failure", err)
	}
}
