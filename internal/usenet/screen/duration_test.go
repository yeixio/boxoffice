package screen

import (
	"path/filepath"
	"testing"
	"time"
)

// The testdata videos are 75 seconds of black, made by ffmpeg.
func TestDuration(t *testing.T) {
	for _, n := range []string{"75s.mkv", "75s.mp4", "75s-faststart.mp4"} {
		d, ok := Duration(filepath.Join("testdata", n))
		if !ok || d < 74*time.Second || d > 76*time.Second {
			t.Errorf("%s: %v %v", n, d, ok)
		}
	}
	if _, ok := Duration(filepath.Join("testdata", "missing.mkv")); ok {
		t.Error("duration of a missing file")
	}
}
