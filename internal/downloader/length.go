package downloader

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/usenet/screen"
)

// A real movie or episode is long enough. Fakes are often a short clip, a
// sample, or a tiny file named like the release. (Variables, so tests can
// use small files.)
var (
	minRunShare        = 0.6       // of the listed running time
	minMovieSize int64 = 150 << 20 // when the running time can't be read
	minEpSize    int64 = 15 << 20
)

// checkLength finds video files too short to be the movie or episode the
// release is for. Samples are left out; their being short is fine.
func (e *Engine) checkLength(ctx context.Context, d *store.Download, dir string, files []string) []screen.Finding {
	if d.Kind == "" {
		return nil
	}
	var runtime int // minutes
	switch d.Kind {
	case "movie":
		if m, err := e.Store.Movie(ctx, d.TitleID); err == nil {
			runtime = m.Runtime
		}
	case "series":
		if s, err := e.Store.Series(ctx, d.TitleID); err == nil {
			runtime = s.Runtime
		}
	}
	minSize := minMovieSize
	what := "the movie"
	if d.Kind == "series" {
		minSize, what = minEpSize, "an episode"
	}
	var out []screen.Finding
	for _, f := range files {
		if !screen.IsVideo(f) || isSample(f) {
			continue
		}
		p := filepath.Join(dir, f)
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if dur, ok := screen.Duration(p); ok {
			if runtime > 0 && dur < time.Duration(float64(runtime)*minRunShare*float64(time.Minute)) {
				out = append(out, screen.Finding{File: f, Why: fmt.Sprintf("it runs %s, but %s runs %d minutes, so it isn't the real thing", minutes(dur), what, runtime)})
			}
			continue
		}
		if info.Size() < minSize {
			out = append(out, screen.Finding{File: f, Why: fmt.Sprintf("it's only %s, too small to be %s", humanBytes(info.Size()), what)})
		}
	}
	return out
}

func isSample(name string) bool {
	low := strings.ToLower(name)
	return strings.Contains(low, "sample") || strings.Contains(low, "trailer")
}

func minutes(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d seconds", int(d.Seconds()))
	}
	if n := int(d.Minutes()); n != 1 {
		return fmt.Sprintf("%d minutes", n)
	}
	return "1 minute"
}
