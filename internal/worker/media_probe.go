package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ErrProbeUnavailable is returned by the ffprobe runner when the
// binary cannot be found (e.g. a dev box without ffmpeg installed).
// Callers treat it as a soft signal: skip the probe, leave the asset's
// probe columns NULL, log at debug level. It is NOT a job failure.
var ErrProbeUnavailable = errors.New("ffprobe not available")

// MediaProber runs a technical probe against a media object (a
// presigned GET URL) and returns the ffprobe-derived metadata. The
// live wizard's compatibility badge is computed from the probe.
type MediaProber interface {
	Probe(ctx context.Context, mediaURL string) (*models.MediaProbe, error)
}

// ffprobeRunner shells out to the ffprobe binary:
//
//	ffprobe -v error -print_format json -show_format -show_streams <url>
//
// The mediaURL is a short-lived presigned GET URL minted by the
// storage provider (the worker never probes the private bucket
// directly). BinPath empty → resolved from PATH; tests may inject a
// fake MediaProber via SetMediaProber instead of stubbing exec.
// NewFFprobeProber returns the production MediaProber: it shells out
// to the ffprobe binary (resolved from PATH; override via
// ffprobeRunner.binPath in tests). Probe returns ErrProbeUnavailable
// when the binary cannot be found — callers treat that as a soft
// skip, never a job failure.
func NewFFprobeProber() MediaProber {
	return &ffprobeRunner{}
}

type ffprobeRunner struct {
	binPath string
}

func (r *ffprobeRunner) Probe(ctx context.Context, mediaURL string) (*models.MediaProbe, error) {
	bin := r.binPath
	if bin == "" {
		bin = "ffprobe"
	}
	if _, err := exec.LookPath(bin); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProbeUnavailable, err)
	}
	cmd := exec.CommandContext(ctx, bin,
		"-v", "error",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		mediaURL,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %w", err)
	}
	probe, err := parseFFprobeOutput(out)
	if err != nil {
		return nil, fmt.Errorf("parse ffprobe output: %w", err)
	}
	return probe, nil
}

// ffprobeStream is the minimal projection of ffprobe's stream object
// the worker needs. Extra fields are ignored.
type ffprobeStream struct {
	CodecType    string `json:"codec_type"`
	CodecName    string `json:"codec_name"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	AvgFrameRate string `json:"avg_frame_rate"`
	RFrameRate   string `json:"r_frame_rate"`
}

type ffprobeOutput struct {
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
	Streams []ffprobeStream `json:"streams"`
}

// parseFFprobeOutput converts the ffprobe JSON envelope into a
// MediaProbe. Rate parsing is lenient: a malformed fraction yields 0
// (which fails the live-compatibility check rather than the job).
func parseFFprobeOutput(raw []byte) (*models.MediaProbe, error) {
	var out ffprobeOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	probe := &models.MediaProbe{}
	if d, err := strconv.ParseFloat(strings.TrimSpace(out.Format.Duration), 64); err == nil && d > 0 {
		probe.DurationSeconds = d
	}
	for i := range out.Streams {
		s := &out.Streams[i]
		switch s.CodecType {
		case "video":
			if probe.Width == 0 {
				probe.Width = s.Width
				probe.Height = s.Height
				probe.FPS = parseFrameRate(s.AvgFrameRate)
				if probe.FPS == 0 {
					probe.FPS = parseFrameRate(s.RFrameRate)
				}
			}
			if probe.VideoCodec == "" {
				probe.VideoCodec = s.CodecName
			}
		case "audio":
			probe.HasAudio = true
			if probe.AudioCodec == "" {
				probe.AudioCodec = s.CodecName
			}
		}
	}
	return probe, nil
}

// parseFrameRate converts ffprobe's "30000/1001" style fractions to a
// float. "0/0" (unknown) and malformed values yield 0.
func parseFrameRate(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if !strings.Contains(s, "/") {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0
		}
		return f
	}
	parts := strings.SplitN(s, "/", 2)
	num, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	den, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err1 != nil || err2 != nil || den == 0 {
		return 0
	}
	return num / den
}
