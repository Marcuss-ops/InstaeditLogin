package veloxcontract

import (
	"errors"
	"math"
	"sort"
	"strings"
)

// WorkerSchedulingInfo is the scheduler snapshot used to rank a worker for
// preparation or execution. Cache and capacity values are worker-reported.
type WorkerSchedulingInfo struct {
	WorkerID              string              `json:"worker_id"`
	Status                string              `json:"status"`
	Capabilities          map[string]bool     `json:"capabilities,omitempty"`
	SupportedProfiles     []AssemblyProfileID `json:"supported_profiles,omitempty"`
	CachedSHA256          []string            `json:"cached_sha256,omitempty"`
	FreeDiskBytes         int64               `json:"free_disk_bytes"`
	TotalDiskBytes        int64               `json:"total_disk_bytes"`
	Load                  float64             `json:"load"`
	ActiveExecutions      int                 `json:"active_executions"`
	ExecutionSlots        int                 `json:"execution_slots"`
	EstimatedStartSeconds float64             `json:"estimated_start_seconds"`
	NetworkMbps           float64             `json:"network_mbps"`
}

// WorkerScore contains explainable normalized factors. Higher Score wins.
type WorkerScore struct {
	WorkerID              string  `json:"worker_id"`
	Score                 float64 `json:"score"`
	CacheLocality         float64 `json:"cache_locality"`
	Capability            float64 `json:"capability"`
	Availability          float64 `json:"availability"`
	LoadFactor            float64 `json:"load_factor"`
	DiskFactor            float64 `json:"disk_factor"`
	NetworkFactor         float64 `json:"network_factor"`
	EstimatedReadySeconds float64 `json:"estimated_ready_seconds"`
	Eligible              bool    `json:"eligible"`
	Reason                string  `json:"reason,omitempty"`
}

// SchedulingWeights are intentionally explicit so deployments can tune them
// without changing the contract. They are normalized internally.
type SchedulingWeights struct {
	Cache        float64
	Capability   float64
	Availability float64
	Load         float64
	Disk         float64
	Network      float64
	StartTime    float64
}

var DefaultSchedulingWeights = SchedulingWeights{
	Cache: 0.35, Capability: 0.20, Availability: 0.15,
	Load: 0.10, Disk: 0.08, Network: 0.05, StartTime: 0.07,
}

// ScoreWorker calculates a cache-aware score. Required assets are matched by
// SHA256; a worker is ineligible when it cannot support the required profile,
// lacks execution capacity, is offline, or has insufficient disk.
func ScoreWorker(worker WorkerSchedulingInfo, required []AssemblyAsset, profile AssemblyProfileID, weights SchedulingWeights) WorkerScore {
	result := WorkerScore{WorkerID: worker.WorkerID, Eligible: true}
	if strings.TrimSpace(worker.WorkerID) == "" {
		return ineligibleWorkerScore(result, "worker_id is required")
	}
	if strings.EqualFold(worker.Status, "offline") || strings.EqualFold(worker.Status, "unhealthy") {
		return ineligibleWorkerScore(result, "worker is not available")
	}
	if profile != "" && !containsProfile(worker.SupportedProfiles, profile) {
		return ineligibleWorkerScore(result, "profile is not supported")
	}
	if worker.ExecutionSlots > 0 && worker.ActiveExecutions >= worker.ExecutionSlots {
		return ineligibleWorkerScore(result, "no execution slot available")
	}
	var requiredBytes int64
	cached := make(map[string]struct{}, len(worker.CachedSHA256))
	for _, hash := range worker.CachedSHA256 {
		cached[hash] = struct{}{}
	}
	for _, asset := range required {
		if _, ok := cached[asset.SHA256]; ok {
			result.CacheLocality++
			continue
		}
		if asset.SizeBytes > 0 {
			requiredBytes += asset.SizeBytes
		}
	}
	if len(required) > 0 {
		result.CacheLocality /= float64(len(required))
	} else {
		result.CacheLocality = 1
	}
	if worker.TotalDiskBytes > 0 {
		available := worker.FreeDiskBytes - requiredBytes
		if available < 0 {
			return ineligibleWorkerScore(result, "insufficient free disk")
		}
		result.DiskFactor = clamp01(float64(available) / float64(worker.TotalDiskBytes))
	} else {
		result.DiskFactor = 0.5
	}
	result.Capability = 1
	result.Availability = 1 / (1 + positive(worker.EstimatedStartSeconds)/60)
	result.EstimatedReadySeconds = positive(worker.EstimatedStartSeconds)
	result.LoadFactor = clamp01(1 - clamp01(worker.Load))
	if worker.NetworkMbps > 0 {
		result.NetworkFactor = clamp01(worker.NetworkMbps / 1000)
	} else {
		result.NetworkFactor = 0.5
	}
	result.Score = weightedScore(result, weights)
	return result
}

func weightedScore(s WorkerScore, w SchedulingWeights) float64 {
	if w == (SchedulingWeights{}) {
		w = DefaultSchedulingWeights
	}
	total := w.Cache + w.Capability + w.Availability + w.Load + w.Disk + w.Network + w.StartTime
	if total <= 0 {
		return 0
	}
	start := 1 / (1 + s.EstimatedReadySeconds/60)
	return (w.Cache*s.CacheLocality + w.Capability*s.Capability + w.Availability*s.Availability + w.Load*s.LoadFactor + w.Disk*s.DiskFactor + w.Network*s.NetworkFactor + w.StartTime*start) / total
}

// SelectWorker returns the highest-ranked eligible worker. Ties are resolved
// by worker ID, making scheduler decisions reproducible and testable.
func SelectWorker(workers []WorkerSchedulingInfo, required []AssemblyAsset, profile AssemblyProfileID, weights SchedulingWeights) (WorkerSchedulingInfo, WorkerScore, error) {
	if len(workers) == 0 {
		return WorkerSchedulingInfo{}, WorkerScore{}, errors.New("no workers available")
	}
	type candidate struct {
		worker WorkerSchedulingInfo
		score  WorkerScore
	}
	candidates := make([]candidate, 0, len(workers))
	for _, worker := range workers {
		score := ScoreWorker(worker, required, profile, weights)
		if score.Eligible {
			candidates = append(candidates, candidate{worker, score})
		}
	}
	if len(candidates) == 0 {
		return WorkerSchedulingInfo{}, WorkerScore{}, errors.New("no eligible worker")
	}
	sort.Slice(candidates, func(i, j int) bool {
		if math.Abs(candidates[i].score.Score-candidates[j].score.Score) > 1e-9 {
			return candidates[i].score.Score > candidates[j].score.Score
		}
		return candidates[i].worker.WorkerID < candidates[j].worker.WorkerID
	})
	return candidates[0].worker, candidates[0].score, nil
}

func containsProfile(profiles []AssemblyProfileID, wanted AssemblyProfileID) bool {
	for _, profile := range profiles {
		if profile == wanted {
			return true
		}
	}
	return false
}

func ineligibleWorkerScore(score WorkerScore, reason string) WorkerScore {
	score.Eligible = false
	score.Reason = reason
	return score
}

func clamp01(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func positive(value float64) float64 {
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
}
