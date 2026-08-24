package veloxcontract

import (
	"strings"
	"testing"
)

func schedulingAsset(id string, size int64) AssemblyAsset {
	return AssemblyAsset{AssetID: id, Kind: AssemblyAssetSourceClip, SHA256: strings.Repeat(id[:1], 64), SizeBytes: size, Required: true}
}

func TestSelectWorkerPrefersCacheLocality(t *testing.T) {
	required := []AssemblyAsset{schedulingAsset("a", 100), schedulingAsset("b", 100)}
	workers := []WorkerSchedulingInfo{
		{WorkerID: "worker-a", Status: "ready", SupportedProfiles: []AssemblyProfileID{AssemblyProfileVeloxH264CopyV1}, CachedSHA256: []string{required[0].SHA256}, FreeDiskBytes: 1000, TotalDiskBytes: 2000, ExecutionSlots: 2, NetworkMbps: 500},
		{WorkerID: "worker-b", Status: "ready", SupportedProfiles: []AssemblyProfileID{AssemblyProfileVeloxH264CopyV1}, CachedSHA256: []string{required[0].SHA256, required[1].SHA256}, FreeDiskBytes: 1000, TotalDiskBytes: 2000, ExecutionSlots: 2, NetworkMbps: 500, EstimatedStartSeconds: 10},
	}
	worker, score, err := SelectWorker(workers, required, AssemblyProfileVeloxH264CopyV1, SchedulingWeights{})
	if err != nil {
		t.Fatal(err)
	}
	if worker.WorkerID != "worker-b" || score.CacheLocality != 1 {
		t.Fatalf("selected %q with score %+v, want worker-b with full cache", worker.WorkerID, score)
	}
}

func TestScoreWorkerRejectsCapacityProfileDiskAndOffline(t *testing.T) {
	asset := schedulingAsset("a", 900)
	base := WorkerSchedulingInfo{WorkerID: "worker", Status: "ready", SupportedProfiles: []AssemblyProfileID{AssemblyProfileVeloxH264CopyV1}, FreeDiskBytes: 1000, TotalDiskBytes: 2000, ExecutionSlots: 1}
	cases := []struct {
		name, reason string
		mutate       func(*WorkerSchedulingInfo)
	}{
		{"capacity", "no execution slot available", func(w *WorkerSchedulingInfo) { w.ActiveExecutions = 1 }},
		{"profile", "profile is not supported", func(w *WorkerSchedulingInfo) { w.SupportedProfiles = nil }},
		{"disk", "insufficient free disk", func(w *WorkerSchedulingInfo) { w.FreeDiskBytes = 100 }},
		{"offline", "worker is not available", func(w *WorkerSchedulingInfo) { w.Status = "offline" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := base
			tc.mutate(&w)
			got := ScoreWorker(w, []AssemblyAsset{asset}, AssemblyProfileVeloxH264CopyV1, SchedulingWeights{})
			if got.Eligible || got.Reason != tc.reason {
				t.Fatalf("score=%+v", got)
			}
		})
	}
}

func TestSelectWorkerUsesWorkerIDAsDeterministicTieBreaker(t *testing.T) {
	workers := []WorkerSchedulingInfo{
		{WorkerID: "worker-z", Status: "ready", FreeDiskBytes: 1000, TotalDiskBytes: 2000, ExecutionSlots: 1},
		{WorkerID: "worker-a", Status: "ready", FreeDiskBytes: 1000, TotalDiskBytes: 2000, ExecutionSlots: 1},
	}
	worker, _, err := SelectWorker(workers, nil, "", SchedulingWeights{})
	if err != nil {
		t.Fatal(err)
	}
	if worker.WorkerID != "worker-a" {
		t.Fatalf("worker=%q, want worker-a", worker.WorkerID)
	}
}
