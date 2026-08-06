package metrics

// zero values are valid (e.g. empty queue is depth=0, lag=0).
func SetQueueDepth(depth int)            { publishQueueDepth.Set(float64(depth)) }
func SetQueueLagSeconds(seconds float64) { publishQueueLagSeconds.Set(seconds) }
func SetUploadJobQueueDepth(depth int)   { uploadJobQueueDepth.Set(float64(depth)) }
func SetTargetsByStatus(status string, count int) {
	if status == "" {
		return
	}
	publishTargetsByStatus.WithLabelValues(status).Set(float64(count))
}
func SetDeadLetterCount(source string, count int) {
	if source == "" {
		return
	}
	deadLetterCount.WithLabelValues(source).Set(float64(count))
}
func SetDatabasePoolUsage(state string, count int) {
	if state == "" {
		return
	}
	databasePoolUsage.WithLabelValues(state).Set(float64(count))
}

// SetDatabasePoolWaitDuration records database/sql's cumulative pool wait
// duration. It is intentionally a gauge because DBStats exposes a cumulative
// process-local value rather than individual wait events.
func SetDatabasePoolWaitDuration(seconds float64) {
	if seconds < 0 {
		seconds = 0
	}
	databasePoolWaitDurationSeconds.Set(seconds)
}

// SetRefreshTokensNearExpiry writes the refresh_tokens_near_expiry
// gauge. Called by the periodic collector once per tick inside the
// single-flighted tx. Zero is a valid value (no grants at risk) and
// MUST be written every tick so the series always emits — the
// same pre-set-0 rationale as the other periodic gauges.
func SetRefreshTokensNearExpiry(count int) {
	refreshTokensNearExpiry.Set(float64(count))
}
