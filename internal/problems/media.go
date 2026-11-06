package problems

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Remuxing a disc image and joining parts are done by FFmpeg, copying the
// streams as they are: nothing is re-encoded, so nothing is lost and it
// takes minutes, not hours. The new file is written under a temporary
// name next to where it goes, checked (it opens, it's as long as what it
// came from, it has the same kinds of streams), and only then given its
// name.

// ErrNoFFmpeg means FFmpeg isn't installed where Box Office runs.
var ErrNoFFmpeg = errors.New("FFmpeg isn't installed where Box Office runs, so it can't remux or join files")

// Tools are the FFmpeg programs, found on the PATH.
type Tools struct{ FFmpeg, FFprobe string }

// FindTools looks for ffmpeg and ffprobe.
func FindTools() (Tools, error) {
	ff, err1 := exec.LookPath("ffmpeg")
	fp, err2 := exec.LookPath("ffprobe")
	if err1 != nil || err2 != nil {
		return Tools{}, ErrNoFFmpeg
	}
	return Tools{ff, fp}, nil
}

// probed is what ffprobe says about an input.
type probed struct {
	Duration float64 // seconds
	Streams  map[string]int
}

func (t Tools) probe(ctx context.Context, args ...string) (probed, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, t.FFprobe, append([]string{"-v", "error", "-print_format", "json", "-show_format", "-show_streams"}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return probed{}, fmt.Errorf("ffprobe: %s", firstLine(stderr.String(), err))
	}
	var r struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			CodecType string `json:"codec_type"`
			Duration  string `json:"duration"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return probed{}, err
	}
	p := probed{Streams: map[string]int{}}
	p.Duration, _ = strconv.ParseFloat(r.Format.Duration, 64)
	for _, s := range r.Streams {
		p.Streams[s.CodecType]++
		if d, _ := strconv.ParseFloat(s.Duration, 64); p.Duration == 0 && d > 0 {
			p.Duration = d
		}
	}
	return p, nil
}

func firstLine(s string, err error) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return err.Error()
	}
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	return s
}

// discInput is how FFmpeg reads a disc image: a Blu-ray through libbluray
// (its longest playlist), a DVD through the dvdvideo demuxer (its longest
// title).
func (t Tools) discInput(ctx context.Context, iso string) ([]string, probed, error) {
	if p, err := t.probe(ctx, "-i", "bluray:"+iso); err == nil && p.Duration > 0 && p.Streams["video"] > 0 {
		return []string{"-i", "bluray:" + iso}, p, nil
	}
	var best []string
	var bestP probed
	var lastErr error
	for title := 1; title <= 99; title++ {
		args := []string{"-f", "dvdvideo", "-title", strconv.Itoa(title), "-i", iso}
		p, err := t.probe(ctx, args...)
		if err != nil {
			lastErr = err
			if best != nil || title > 3 {
				break
			}
			continue
		}
		if p.Streams["video"] > 0 && p.Duration > bestP.Duration {
			best, bestP = args, p
		}
	}
	if best == nil {
		if lastErr != nil && strings.Contains(lastErr.Error(), "Unknown input format") {
			return nil, probed{}, errors.New("this FFmpeg can't read DVD images (it was built without dvdvideo)")
		}
		return nil, probed{}, fmt.Errorf("FFmpeg couldn't find a movie on the disc image: %v", lastErr)
	}
	return best, bestP, nil
}

// Progress is how far a remux has got, 0 to 1.
type Progress func(float64)

// Convert remuxes the main movie on a disc image into an MKV at out.
func (t Tools) Convert(ctx context.Context, iso, out string, progress Progress) error {
	input, src, err := t.discInput(ctx, iso)
	if err != nil {
		return err
	}
	args := append(input, "-map", "0:v:0", "-map", "0:a?", "-map", "0:s?", "-c", "copy")
	return t.remux(ctx, args, out, src, progress)
}

// Join puts parts together, in order, into one MKV at out.
func (t Tools) Join(ctx context.Context, parts []string, out string, progress Progress) error {
	var sum probed
	sum.Streams = map[string]int{}
	for i, p := range parts {
		pr, err := t.probe(ctx, "-i", p)
		if err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(p), err)
		}
		if pr.Streams["video"] == 0 {
			return fmt.Errorf("%s has no video", filepath.Base(p))
		}
		if i == 0 {
			sum.Streams = pr.Streams
		} else if pr.Streams["video"] != sum.Streams["video"] || pr.Streams["audio"] != sum.Streams["audio"] {
			return fmt.Errorf("the parts don't have the same streams (%s differs from %s), so they can't be joined without re-encoding", filepath.Base(p), filepath.Base(parts[0]))
		}
		sum.Duration += pr.Duration
	}
	list, err := os.CreateTemp(filepath.Dir(out), ".boxoffice-join-*.txt")
	if err != nil {
		return err
	}
	defer os.Remove(list.Name())
	for _, p := range parts {
		abs, _ := filepath.Abs(p)
		fmt.Fprintf(list, "file '%s'\n", strings.ReplaceAll(abs, "'", `'\''`))
	}
	if err := list.Close(); err != nil {
		return err
	}
	args := []string{"-fflags", "+genpts", "-f", "concat", "-safe", "0", "-i", list.Name(), "-map", "0", "-c", "copy"}
	return t.remux(ctx, args, out, sum, progress)
}

// remux runs FFmpeg into a temporary file next to out, checks the result
// against want, and gives it its name. out must not exist.
func (t Tools) remux(ctx context.Context, args []string, out string, want probed, progress Progress) error {
	if _, err := os.Lstat(out); err == nil {
		return fmt.Errorf("%s is there already", out)
	}
	tmp := strings.TrimSuffix(out, filepath.Ext(out)) + ".boxoffice-part"
	os.Remove(tmp)
	full := append([]string{"-nostdin", "-hide_banner", "-v", "error", "-y"}, args...)
	full = append(full, "-f", "matroska", "-progress", "pipe:1", "-nostats", tmp)
	cmd := exec.CommandContext(ctx, t.FFmpeg, full...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		line := sc.Text()
		if v, ok := strings.CutPrefix(line, "out_time_us="); ok && progress != nil && want.Duration > 0 {
			if us, err := strconv.ParseFloat(v, 64); err == nil {
				progress(math.Min(1, us/1e6/want.Duration))
			}
		}
	}
	if err := cmd.Wait(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("ffmpeg: %s", firstLine(stderr.String(), err))
	}
	if err := t.check(ctx, tmp, want); err != nil {
		os.Remove(tmp)
		return err
	}
	if _, err := os.Lstat(out); err == nil {
		os.Remove(tmp)
		return fmt.Errorf("%s turned up while remuxing", out)
	}
	if err := os.Rename(tmp, out); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// check makes sure a new file opens and matches what it was made from.
func (t Tools) check(ctx context.Context, path string, want probed) error {
	got, err := t.probe(ctx, "-i", path)
	if err != nil {
		return fmt.Errorf("the new file doesn't open: %w", err)
	}
	if got.Streams["video"] == 0 {
		return errors.New("the new file has no video")
	}
	if want.Streams["audio"] > 0 && got.Streams["audio"] == 0 {
		return errors.New("the new file has no sound")
	}
	if want.Duration > 0 {
		slack := math.Max(5, want.Duration*0.02)
		if math.Abs(got.Duration-want.Duration) > slack {
			return fmt.Errorf("the new file is %s long, not %s", clock(got.Duration), clock(want.Duration))
		}
	}
	return nil
}

func clock(sec float64) string {
	d := time.Duration(sec * float64(time.Second)).Round(time.Second)
	return fmt.Sprintf("%d:%02d:%02d", int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60)
}
