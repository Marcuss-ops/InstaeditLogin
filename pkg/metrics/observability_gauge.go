package metrics

// zero values are valid (e.g. empty queue is depth=0, lag=0).
func SetQueueDepth(depth int)            { publishQueueDepth.Set(float64(depth)) }
func SetQueueLagSeconds(seconds float64) { publishQueueLagSeconds.Set(seconds) }
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
