package downloader

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

// checkLength finds a release whose main video is too short to be the
// movie or episode it's for. Only the main video counts: samples,
// trailers and extras are short, and that's fine.
func (e *Engine) checkLength(ctx context.Context, d *store.Download, dir string, files []string) []screen.Finding {
	if d.Kind != "movie" && d.Kind != "series" {
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
	for _, v := range mainVideos(dir, files, d.Kind == "series") {
		p := filepath.Join(dir, v.name)
		if dur, ok := screen.Duration(p); ok && float64(v.size)/dur.Seconds() < maxBytesPerSecond {
			if runtime > 0 && dur < time.Duration(float64(runtime)*minRunShare*float64(time.Minute)) {
				out = append(out, screen.Finding{File: v.name, Why: fmt.Sprintf("the main video (%s) runs %s, but %s runs %d minutes, so it isn't the real thing", humanBytes(v.size), minutes(dur), what, runtime)})
			}
			continue
		}
		// No running time to go on (or one that can't be right for its
		// size): the size has to do.
		if v.size < minSize {
			out = append(out, screen.Finding{File: v.name, Why: fmt.Sprintf("the main video is only %s, too small to be %s", humanBytes(v.size), what)})
		}
	}
	return out
}

// A running time that puts more than this through a second is misread.
const maxBytesPerSecond = 25 << 20 // 200 Mbit/s; a 4K remux is under half that

type video struct {
	name string
	size int64
}

// mainVideos picks the files that should be the movie or episodes, out of
// bundles that may add samples, trailers and extras: for a movie, the
// biggest video; for a show, every video at least a quarter the size of
// the biggest, as a season pack holds one file per episode. Files named as
// extras are passed over unless nothing else is there.
func mainVideos(dir string, files []string, series bool) []video {
	var all, main []video
	for _, f := range files {
		if !screen.IsVideo(f) {
			continue
		}
		info, err := os.Stat(filepath.Join(dir, f))
		if err != nil {
			continue
		}
		v := video{f, info.Size()}
		all = append(all, v)
		if !isExtra(f) {
			main = append(main, v)
		}
	}
	if len(main) == 0 {
		main = all
	}
	if len(main) == 0 {
		return nil
	}
	slices.SortFunc(main, func(a, b video) int { return cmp.Compare(b.size, a.size) })
	if !series {
		return main[:1]
	}
	n := 1
	for n < len(main) && main[n].size*4 >= main[0].size {
		n++
	}
	return main[:n]
}

// isExtra says whether a video is named as something other than the
// feature: a sample, trailer or bonus material.
func isExtra(name string) bool {
	low := strings.ToLower(filepath.ToSlash(name))
	for _, w := range []string{"sample", "trailer", "extras/", "featurette", "behind.the.scenes", "behind the scenes", "deleted.scene", "deleted scene", "interview", "bonus"} {
		if strings.Contains(low, w) {
			return true
		}
	}
	return false
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
