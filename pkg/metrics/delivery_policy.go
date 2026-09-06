package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// DriveReinitiateLoops counts Google Drive delivery sessions that hit the
// re-initiate attempt cap: the session row kept dying (TTL expiry / chunk
// failure → delete + fresh initiate) past the budget, which indicates a
// permanently failing upload (dead source URL, poisoned destination folder,
// rejected payload) rather than a transient blip. Before the cap existed,
// such loops consumed Drive quota + a worker tick per cycle forever.
var DriveReinitiateLoops = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "delivery_drive_reinitiate_loops_total",
		Help: "Drive delivery sessions that exhausted the per-key re-initiate budget (permanently failing upload loop capped).",
	},
	[]string{"deliverable_type"},
)

func init() {
	prometheus.MustRegister(DriveReinitiateLoops)
}

// RecordDriveReinitiateLoop increments the loop-cap counter for the
// deliverable type that exhausted its budget. Unknown/empty types are
// bucketed under "unknown" so no unbounded label series can be created.
func RecordDriveReinitiateLoop(deliverableType string) {
	if deliverableType == "" {
		deliverableType = "unknown"
	}
	DriveReinitiateLoops.WithLabelValues(deliverableType).Inc()
}
