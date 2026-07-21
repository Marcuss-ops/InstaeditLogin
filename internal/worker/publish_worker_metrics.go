package worker

import (
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
)

// recordChannelMismatch records the operator-facing metric for a
// YouTube channel binding drift detected during publish. Keeping the
// metric call in its own file isolates the pkg/metrics dependency
// from the main orchestrator.
func (w *PublishWorker) recordChannelMismatch(platform string) {
	metrics.RecordYouTubePublishChannelMismatch(platform)
}
