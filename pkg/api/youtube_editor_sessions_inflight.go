package api

import "time"

// DefaultPublishingInFlightTimeout is the default guard window used
// when a YouTube thumbnail publish session is already in-flight.
// The publish path treats a session stamped 'publishing' older than
// this window as stale (the worker that claimed it died mid-flight)
// and allows the next publish attempt to retry it. Wired into the
// Router via WithPublishingInFlightTimeout; see router_options.go.
const DefaultPublishingInFlightTimeout = 5 * time.Minute
