package models

import "time"

// MediaProbe is the ffprobe-derived technical metadata attached to a
// ready media asset (migration 092). DurationSeconds / Width / Height
// / FPS / HasAudio / ProbedAt are NULL on the row until the upload
// worker probes the asset; Codecs default to ” until then. The
// live-streaming wizard renders these fields and derives the
// "Pronto per live" badge from LiveCompatible().
type MediaProbe struct {
	DurationSeconds float64   `json:"duration_seconds"`
	Width           int       `json:"width"`
	Height          int       `json:"height"`
	FPS             float64   `json:"fps"`
	HasAudio        bool      `json:"has_audio"`
	VideoCodec      string    `json:"video_codec"`
	AudioCodec      string    `json:"audio_codec"`
	ProbedAt        time.Time `json:"probed_at"`
}

// LiveCompatible reports whether the probed asset matches one of the
// canonical live profiles (1080p30 or 720p30, ~30 fps, with an audio
// track and a real duration). Everything else — odd resolutions, VFR,
// silent files, zero-length probes — must be normalised before it can
// feed a live encoder.
func (p MediaProbe) LiveCompatible() bool {
	if p.DurationSeconds <= 0 || !p.HasAudio {
		return false
	}
	if p.FPS < 29 || p.FPS > 31 {
		return false
	}
	switch {
	case p.Width == 1920 && p.Height == 1080:
		return true
	case p.Width == 1280 && p.Height == 720:
		return true
	default:
		return false
	}
}

// Probe projects the asset's probe columns (migration 092) onto a
// MediaProbe. An unprobed asset (ProbedAt nil) yields a zero-value
// probe with a zero ProbedAt. Used by the Media Library endpoint to
// derive live_compatibility without duplicating the field mapping.
func (a *MediaAsset) Probe() MediaProbe {
	p := MediaProbe{}
	if a == nil {
		return p
	}
	if a.ProbedAt != nil {
		p.ProbedAt = *a.ProbedAt
	}
	if a.DurationSeconds != nil {
		p.DurationSeconds = *a.DurationSeconds
	}
	if a.Width != nil {
		p.Width = *a.Width
	}
	if a.Height != nil {
		p.Height = *a.Height
	}
	if a.FPS != nil {
		p.FPS = *a.FPS
	}
	if a.HasAudio != nil {
		p.HasAudio = *a.HasAudio
	}
	p.VideoCodec = a.VideoCodec
	p.AudioCodec = a.AudioCodec
	return p
}
