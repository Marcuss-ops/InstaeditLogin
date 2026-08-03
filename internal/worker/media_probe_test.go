package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestParseFFprobeOutput_FullProbe(t *testing.T) {
	raw := []byte(`{
		"format": {"duration": "840.320000"},
		"streams": [
			{"codec_type": "video", "codec_name": "h264", "width": 1920, "height": 1080, "avg_frame_rate": "30000/1001"},
			{"codec_type": "audio", "codec_name": "aac"}
		]
	}`)
	probe, err := parseFFprobeOutput(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if probe.DurationSeconds != 840.32 {
		t.Errorf("duration: got %v, want 840.32", probe.DurationSeconds)
	}
	if probe.Width != 1920 || probe.Height != 1080 {
		t.Errorf("resolution: got %dx%d, want 1920x1080", probe.Width, probe.Height)
	}
	if probe.FPS < 29.97-0.001 || probe.FPS > 29.97+0.001 {
		t.Errorf("fps: got %v, want ~29.97", probe.FPS)
	}
	if !probe.HasAudio {
		t.Error("has_audio: want true")
	}
	if probe.VideoCodec != "h264" || probe.AudioCodec != "aac" {
		t.Errorf("codecs: got %s/%s, want h264/aac", probe.VideoCodec, probe.AudioCodec)
	}
	if !probe.LiveCompatible() {
		t.Error("live_compatible: want true for 1080p30 h264/aac with audio")
	}
}

func TestParseFFprobeOutput_IntegerFrameRateFallback(t *testing.T) {
	raw := []byte(`{
		"format": {"duration": "60.000000"},
		"streams": [
			{"codec_type": "video", "codec_name": "h264", "width": 1280, "height": 720, "avg_frame_rate": "0/0", "r_frame_rate": "30/1"},
			{"codec_type": "audio", "codec_name": "aac"}
		]
	}`)
	probe, err := parseFFprobeOutput(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if probe.FPS != 30 {
		t.Errorf("fps: got %v, want 30 (r_frame_rate fallback)", probe.FPS)
	}
	if !probe.LiveCompatible() {
		t.Error("live_compatible: want true for 720p30")
	}
}

func TestParseFFprobeOutput_SilentFileIsNotLiveCompatible(t *testing.T) {
	raw := []byte(`{
		"format": {"duration": "120.0"},
		"streams": [
			{"codec_type": "video", "codec_name": "h264", "width": 1920, "height": 1080, "avg_frame_rate": "30/1"}
		]
	}`)
	probe, err := parseFFprobeOutput(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if probe.HasAudio {
		t.Error("has_audio: want false for a silent stream")
	}
	if probe.LiveCompatible() {
		t.Error("live_compatible: want false for a file without audio")
	}
}

func TestParseFFprobeOutput_MalformedDuration(t *testing.T) {
	raw := []byte(`{"format": {"duration": "n/a"}, "streams": []}`)
	probe, err := parseFFprobeOutput(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if probe.DurationSeconds != 0 {
		t.Errorf("duration: got %v, want 0 for malformed value", probe.DurationSeconds)
	}
	if probe.LiveCompatible() {
		t.Error("live_compatible: want false when duration is unknown")
	}
}

// fakeProber is a scriptable MediaProber used by the probe-wiring tests.
type fakeProber struct {
	probe *models.MediaProbe
	err   error
}

func (f *fakeProber) Probe(_ context.Context, _ string) (*models.MediaProbe, error) {
	return f.probe, f.err
}

// TestProbeReadyAsset_UnavailableIsSoft verifies the ingest probe is a
// soft no-op when ffprobe is missing — the job must not fail.
func TestProbeReadyAsset_UnavailableIsSoft(t *testing.T) {
	w := &UploadWorker{
		prober:     &fakeProber{err: ErrProbeUnavailable},
		mediaStore: &fakeMediaStore{},
		storage:    &fakeStorage{},
		logger:     discardLogger(),
	}
	w.probeReadyAsset(context.Background(), "asset-1", "uploads/1/uuid_name.mp4")
}

func TestProbeReadyAsset_PersistsProbe(t *testing.T) {
	store := &fakeMediaStore{}
	probe := &models.MediaProbe{DurationSeconds: 10, Width: 1920, Height: 1080, FPS: 30, HasAudio: true}
	w := &UploadWorker{
		prober:     &fakeProber{probe: probe},
		mediaStore: store,
		storage:    &fakeStorage{},
		logger:     discardLogger(),
	}
	w.probeReadyAsset(context.Background(), "asset-1", "uploads/1/uuid_name.mp4")
	if store.saveProbeCalls != 1 {
		t.Fatalf("SaveProbe calls: got %d, want 1", store.saveProbeCalls)
	}
	if probe.ProbedAt.IsZero() {
		t.Error("ProbedAt: want stamped")
	}
}

func TestProbeReadyAsset_NilProberIsNoop(t *testing.T) {
	w := &UploadWorker{mediaStore: &fakeMediaStore{}, logger: discardLogger()}
	w.probeReadyAsset(context.Background(), "asset-1", "key")
}

func TestProbeRunner_ReportsUnavailable(t *testing.T) {
	// A binPath that can never resolve keeps the test off the host
	// PATH; LookPath must surface ErrProbeUnavailable.
	runner := &ffprobeRunner{binPath: "\x00missing"}
	_, err := runner.Probe(context.Background(), "https://example.test/v.mp4")
	if err == nil {
		t.Fatal("Probe: want error for missing binary")
	}
	if !errors.Is(err, ErrProbeUnavailable) {
		t.Fatalf("Probe: want ErrProbeUnavailable, got %v", err)
	}
}
