// Package services — YouTubeQuotaManager.
//
// YouTubeQuotaManager is the pre-call gate for the YouTube Data API v3
// under the Google 2026 quota model (effective 2026-06-01). Google
// split the API into three INDEPENDENT daily buckets:
//
//	video_uploads → videos.insert                 (default 100 calls/day)
//	searches      → search.list                   (default 100 calls/day)
//	general       → videos.update, videos.list,
//	                thumbnails.set, channels.list (default 10000 units/day)
//
// Each bucket resets on its own daily boundary and a quota_exceeded in
// one bucket does NOT block the others. The old single counter
// (YouTubeDailyQuotaLimit, default 300) is gone: it no longer matches
// Google's model, and 300 uploads/day is above Google's 100-call
// default for the video_uploads bucket — the scheduler could believe
// "used 102/300 OK" while Google answers quota_exceeded.
//
// The manager owns the bucket→limit mapping and the
// operation→(bucket, cost) table; the repository
// (internal/repository/youtube_quota_repo.go) owns the Postgres row
// locking that makes the gate strict across pods.
package services

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// YouTube quota bucket identifiers (re-exported from the repository so
// callers never need to import the repository layer just to name a
// bucket).
const (
	YouTubeQuotaBucketVideoUploads = repository.YouTubeQuotaBucketVideoUploads
	YouTubeQuotaBucketSearches     = repository.YouTubeQuotaBucketSearches
	YouTubeQuotaBucketGeneral      = repository.YouTubeQuotaBucketGeneral
)

// YouTube Data API v3 operations the quota manager knows how to charge.
// Add a constant here (plus a row in youtubeOperationSpecs) when a new
// endpoint starts burning quota.
const (
	YouTubeOpVideoInsert   = "videos.insert"
	YouTubeOpVideoUpdate   = "videos.update"
	YouTubeOpVideoList     = "videos.list"
	YouTubeOpThumbnailsSet = "thumbnails.set"
	YouTubeOpChannelsList  = "channels.list"
	YouTubeOpSearchList    = "search.list"
)

// youtubeOperationSpec is the (bucket, cost) pair for one API
// operation under the 2026 quota model.
type youtubeOperationSpec struct {
	Bucket string
	Cost   int
}

// youtubeOperationSpecs maps each known operation to its bucket and
// unit cost. Costs follow Google's quota table:
//
//	video_uploads: videos.insert costs 1 unit (bucket cap 100/day)
//	searches:      search.list  costs 1 unit (bucket cap 100/day)
//	general:       videos.update 50, thumbnails.set 50,
//	               videos.list 1, channels.list 1 (bucket cap 10000/day)
var youtubeOperationSpecs = map[string]youtubeOperationSpec{
	YouTubeOpVideoInsert:   {Bucket: YouTubeQuotaBucketVideoUploads, Cost: 1},
	YouTubeOpSearchList:    {Bucket: YouTubeQuotaBucketSearches, Cost: 1},
	YouTubeOpVideoUpdate:   {Bucket: YouTubeQuotaBucketGeneral, Cost: 50},
	YouTubeOpVideoList:     {Bucket: YouTubeQuotaBucketGeneral, Cost: 1},
	YouTubeOpThumbnailsSet: {Bucket: YouTubeQuotaBucketGeneral, Cost: 50},
	YouTubeOpChannelsList:  {Bucket: YouTubeQuotaBucketGeneral, Cost: 1},
}

// YouTubeQuotaLimits is the operator-tunable daily ceiling per bucket
// (Google 2026 defaults).
type YouTubeQuotaLimits struct {
	VideoUploads int
	Searches     int
	General      int
}

// DefaultYouTubeQuotaLimits returns the Google 2026 default ceilings:
// 100 videos.insert calls, 100 search.list calls, 10000 general units.
func DefaultYouTubeQuotaLimits() YouTubeQuotaLimits {
	return YouTubeQuotaLimits{VideoUploads: 100, Searches: 100, General: 10000}
}

// forBucket resolves a bucket identifier to its configured ceiling.
func (l YouTubeQuotaLimits) forBucket(bucket string) (int, error) {
	switch bucket {
	case YouTubeQuotaBucketVideoUploads:
		return l.VideoUploads, nil
	case YouTubeQuotaBucketSearches:
		return l.Searches, nil
	case YouTubeQuotaBucketGeneral:
		return l.General, nil
	default:
		return 0, fmt.Errorf("youtube quota: unknown bucket %q (known: %s, %s, %s)",
			bucket, YouTubeQuotaBucketVideoUploads, YouTubeQuotaBucketSearches, YouTubeQuotaBucketGeneral)
	}
}

// YouTubeQuotaSnapshot is the operator-facing view of one bucket for
// today: units used, informational error count, the effective ceiling
// and the last reset timestamp.
type YouTubeQuotaSnapshot struct {
	Bucket      string
	Calls       int
	Errors      int
	Limit       int
	LastResetAt time.Time
}

// YouTubeQuotaManager is the pre-call gate. Call Reserve just before a
// YouTube Data API call: it refuses (with a retry-after hint) when the
// bucket for the day is exhausted, and records the charge only when the
// call is about to be made.
type YouTubeQuotaManager struct {
	repo   *repository.YouTubeDailyQuotaRepository
	limits YouTubeQuotaLimits
}

// NewYouTubeQuotaManager builds the manager around the Postgres-backed
// repository and the operator-tunable bucket ceilings. A nil repo is
// tolerated at construction; Reserve/RecordError/Snapshot then return
// an explicit error instead of panicking. Zero-value limits fall back
// to the Google 2026 defaults so a partially-constructed config cannot
// silently hard-block every bucket.
func NewYouTubeQuotaManager(repo *repository.YouTubeDailyQuotaRepository, limits YouTubeQuotaLimits) *YouTubeQuotaManager {
	if limits.VideoUploads == 0 {
		limits.VideoUploads = 100
	}
	if limits.Searches == 0 {
		limits.Searches = 100
	}
	if limits.General == 0 {
		limits.General = 10000
	}
	return &YouTubeQuotaManager{repo: repo, limits: limits}
}

// Limits returns a copy of the configured bucket ceilings.
func (m *YouTubeQuotaManager) Limits() YouTubeQuotaLimits {
	return m.limits
}

// Reserve charges `cost` units against `bucket` for today, atomically.
// It returns:
//
//	allowed=true            → the call may proceed (units were charged).
//	allowed=false, retry>0  → bucket exhausted; retry is seconds until
//	                          the next daily reset.
//	err != nil              → the gate itself failed (DB down etc.).
//	                          Callers should fail closed: do NOT make the
//	                          API call when the gate cannot decide.
func (m *YouTubeQuotaManager) Reserve(ctx context.Context, bucket string, cost int) (allowed bool, retryAfterSeconds int, err error) {
	limit, err := m.limits.forBucket(bucket)
	if err != nil {
		return false, 0, err
	}
	return m.repo.ReserveQuota(ctx, bucket, cost, limit)
}

// ReserveOperation is the convenience wrapper over Reserve for callers
// that hold an operation name (e.g. YouTubeOpVideoInsert): it resolves
// the operation to its (bucket, cost) spec and charges it. Unknown
// operations return an error — a typo must fail before the API call,
// never after burning quota in the wrong bucket.
func (m *YouTubeQuotaManager) ReserveOperation(ctx context.Context, operation string) (allowed bool, retryAfterSeconds int, err error) {
	spec, ok := youtubeOperationSpecs[operation]
	if !ok {
		return false, 0, fmt.Errorf("youtube quota: unknown operation %q", operation)
	}
	return m.Reserve(ctx, spec.Bucket, spec.Cost)
}

// OperationSpec exposes the (bucket, cost) for an operation, useful for
// instrumentation and for computing a day's projected burn before
// scheduling.
func (m *YouTubeQuotaManager) OperationSpec(operation string) (bucket string, cost int, err error) {
	spec, ok := youtubeOperationSpecs[operation]
	if !ok {
		return "", 0, fmt.Errorf("youtube quota: unknown operation %q", operation)
	}
	return spec.Bucket, spec.Cost, nil
}

// RecordError bumps the informational errors counter for a bucket after
// a real API failure (5xx / transport / validation). Distinct from the
// Reserve path: this is "we tried, Google said no", not "we decided not
// to try".
func (m *YouTubeQuotaManager) RecordError(ctx context.Context, bucket string) error {
	limit, err := m.limits.forBucket(bucket)
	if err != nil {
		return err
	}
	return m.repo.RecordError(ctx, bucket, limit)
}

// Snapshot returns today's usage for a bucket. When no row exists yet
// (nothing charged today) the snapshot reports zero calls with the
// configured ceiling, so operators always see the effective limit.
func (m *YouTubeQuotaManager) Snapshot(ctx context.Context, bucket string) (YouTubeQuotaSnapshot, error) {
	limit, err := m.limits.forBucket(bucket)
	if err != nil {
		return YouTubeQuotaSnapshot{}, err
	}
	calls, errCount, storedLimit, lastResetAt, err := m.repo.GetSnapshot(ctx, bucket)
	if err != nil {
		return YouTubeQuotaSnapshot{}, err
	}
	if storedLimit == 0 {
		storedLimit = limit
	}
	return YouTubeQuotaSnapshot{
		Bucket:      bucket,
		Calls:       calls,
		Errors:      errCount,
		Limit:       storedLimit,
		LastResetAt: lastResetAt,
	}, nil
}
