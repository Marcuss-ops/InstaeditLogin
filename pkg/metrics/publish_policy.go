package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// DriveRequiredViolations counts terminal drive_required policy
// violations: a delivery completed its platform publish while the
// required Drive upload terminally failed. The publish itself must
// still be marked failed/flagged by the caller — this counter exists
// so the violation class is alarmable in dashboards instead of being
// log-only (the historical gap: the worker logged a WARN but the
// metric "was not yet exported in pkg/metrics").
var DriveRequiredViolations = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "publish_drive_required_violations_total",
		Help: "Terminal drive_required policy violations: platform publish completed while the required Drive upload terminally failed.",
	},
	[]string{"platform"},
)

func init() {
	prometheus.MustRegister(DriveRequiredViolations)
}

// RecordDriveRequiredViolation increments the violation counter for the
// platform that completed its publish despite a terminally failed
// required Drive upload. Unknown/empty platforms are bucketed under
// "unknown" so no unbounded label series can be created.
func RecordDriveRequiredViolation(platform string) {
	if platform == "" {
		platform = "unknown"
	}
	DriveRequiredViolations.WithLabelValues(platform).Inc()
}
