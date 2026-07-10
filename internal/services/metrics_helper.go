package services

import (
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
)

// RecordPublishMetrics is a defer helper for Publish() methods. It records
// publish latency, success, or error in a single call, replacing the 4-line
// defer block duplicated across all platform providers.
//
// Usage:
//
//	func (s *TikTokOAuthService) Publish(ctx context.Context, ...) (result *models.PublishResult, err error) {
//	    defer RecordPublishMetrics(models.PlatformTikTok, time.Now(), &err)
//	    ...
func RecordPublishMetrics(platform string, start time.Time, err *error) {
	metrics.ObservePublishLatency(platform, time.Since(start).Seconds())
	if *err != nil {
		metrics.RecordPublishError(platform, metrics.ErrorKind(*err))
	} else {
		metrics.RecordPublishSuccess(platform)
	}
}

// RecordTokenRefreshMetrics is a defer helper for RefreshOAuthToken() methods.
// It records token refresh success or error, replacing the 4-line defer block
// duplicated across all platform providers.
//
// Usage:
//
//	func (s *TikTokOAuthService) RefreshOAuthToken(ctx context.Context, ...) (result *models.TokenData, err error) {
//	    defer RecordTokenRefreshMetrics(models.PlatformTikTok, &err)
//	    ...
func RecordTokenRefreshMetrics(platform string, err *error) {
	if *err != nil {
		metrics.RecordTokenRefreshError(platform)
	} else {
		metrics.RecordTokenRefreshSuccess(platform)
	}
}
