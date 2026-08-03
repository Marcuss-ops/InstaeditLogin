package models

import "testing"

func TestMediaProbe_LiveCompatible_Profiles(t *testing.T) {
	base := MediaProbe{DurationSeconds: 60, FPS: 30, HasAudio: true}
	baseCases := []struct {
		name   string
		width  int
		height int
		want   bool
	}{
		{"1080p", 1920, 1080, true},
		{"720p", 1280, 720, true},
		{"4k", 3840, 2160, false},
		{"vertical 9:16", 1080, 1920, false},
		{"480p", 854, 480, false},
		{"odd resolution", 1918, 1078, false},
	}
	for _, tc := range baseCases {
		t.Run(tc.name, func(t *testing.T) {
			p := base
			p.Width, p.Height = tc.width, tc.height
			if got := p.LiveCompatible(); got != tc.want {
				t.Errorf("LiveCompatible() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMediaProbe_LiveCompatible_RejectsNonLiveShapes(t *testing.T) {
	ok := MediaProbe{DurationSeconds: 60, Width: 1920, Height: 1080, FPS: 30, HasAudio: true}
	if !ok.LiveCompatible() {
		t.Fatal("baseline should be compatible")
	}
	noDuration := ok
	noDuration.DurationSeconds = 0
	if noDuration.LiveCompatible() {
		t.Error("zero duration must not be live compatible")
	}
	noAudio := ok
	noAudio.HasAudio = false
	if noAudio.LiveCompatible() {
		t.Error("silent file must not be live compatible")
	}
	vfr := ok
	vfr.FPS = 23.976
	if vfr.LiveCompatible() {
		t.Error("VFR (23.976) must not be live compatible")
	}
	highFps := ok
	highFps.FPS = 60
	if highFps.LiveCompatible() {
		t.Error("60fps must not be live compatible")
	}
}
