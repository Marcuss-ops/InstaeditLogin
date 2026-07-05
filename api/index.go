// Package api is the Vercel serverless entry point for the InstaEditLogin backend.
// Vercel detects Go in the project root and treats files in api/ as serverless
// function handlers. This file catches all incoming requests and routes them
// through the same HTTP handler used by the standalone server.
package api

import (
	"net/http"
	"sync"

	"github.com/Marcuss-ops/InstaeditLogin/internal/app"
)

var (
	handler http.Handler
	once    sync.Once
	initErr error
)

// Handler is the Vercel serverless function entry point.
// All HTTP requests to the deployment are dispatched here.
func Handler(w http.ResponseWriter, r *http.Request) {
	once.Do(func() {
		h, cleanup, err := app.InitHandler()
		if err != nil {
			initErr = err
			// cleanup may be nil on early failures
			_ = cleanup
			return
		}
		// Note: cleanup is intentionally never called for serverless.
		// Vercel terminates the process between invocations when idle.
		handler = h
	})

	if initErr != nil {
		http.Error(w, "Service initialization failed: "+initErr.Error(), http.StatusInternalServerError)
		return
	}

	handler.ServeHTTP(w, r)
}
