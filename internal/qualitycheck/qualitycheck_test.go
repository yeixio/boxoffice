package qualitycheck

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/quality"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
)

func init() { pause = 0 }

// video makes a second of video of a size with ffmpeg.
func video(t *testing.T, path string, w, h int) {
	t.Helper()
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	os.MkdirAll(filepath.Dir(path), 0o755)
	out, err := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "testsrc=duration=1:size="+itoa(w)+"x"+itoa(h)+":rate=5",
		"-f", "lavfi", "-i", "sine=duration=1", "-c:v", "mpeg4", "-c:a", "aac", "-shortest", path).CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg: %v %s", err, out)
	}
}

func itoa(n int) string {
	return string(rune('0'+n/1000%10)) + string(rune('0'+n/100%10)) + string(rune('0'+n/10%10)) + string(rune('0'+n%10))
}

func TestPass(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("no ffprobe")
	}
	ctx := context.Background()
	st := storetest.Open(t)
	dir := t.TempDir()
	profiles, _ := st.Profiles(ctx)
	var hd quality.Profile
	for _, p := range profiles {
		if p.Name == "HD 1080p" {
			hd = p
		}
	}

	n := int64(0)
	add := func(title, file string, w, h, q int) store.File {
		n++
		m, err := st.AddMovie(ctx, store.Movie{TMDBID: n, Title: title, ProfileID: hd.ID, Path: filepath.Join(dir, title)})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, title, file)
		if w > 0 {
			video(t, path, w, h)
		}
		info, _ := os.Stat(path)
		var size int64 = 1
		if info != nil {
			size = info.Size()
		}
		f, err := st.AddFile(ctx, store.File{Kind: "movie", OwnerID: m.ID, Path: path, Size: size, Quality: q})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	wrong := add("Wrong", "Wrong.720p.BluRay.mkv", 1920, 800, 7)    // says 720p, is 1080p
	right := add("Rightish", "Right.1080p.WEB.mkv", 1920, 1080, 10) // as it says
	unknown := add("Unknownish", "Unknown.mkv", 1920, 1080, 0)      // nothing known: meets the cutoff
	gone := add("Goneaway", "Gone.mkv", 0, 0, 0)

	s := &Service{Store: st, Log: slog.New(slog.DiscardHandler)}
	if err := s.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	check := func(f store.File, want int, corrected bool) {
		t.Helper()
		got, err := st.FileByID(ctx, f.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Quality != want {
			t.Errorf("%s: quality %d, want %d", f.Path, got.Quality, want)
		}
		if (got.Guessed != nil) != corrected || corrected && *got.Guessed != f.Quality {
			t.Errorf("%s: guessed %v", f.Path, got.Guessed)
		}
		if got.Media == nil {
			t.Errorf("%s: no media", f.Path)
		}
	}
	check(wrong, 11, true)
	check(right, 10, false)
	check(unknown, 10, true)
	check(gone, 0, false)
	if g, _ := st.FileByID(ctx, wrong.ID); g.Media.Width != 1920 || g.Media.Height != 800 || g.Media.AudioCodec != "AAC" || g.Media.Resolution() != 1080 {
		t.Errorf("media %+v", g.Media)
	}
	if g, _ := st.FileByID(ctx, gone.ID); g.Media.Error == "" {
		t.Errorf("missing file: %+v", g.Media)
	}

	c, err := st.CheckCounts(ctx)
	if err != nil || c.Files != 4 || c.Checked != 4 || c.Unreadable != 1 || c.Unknown != 1 {
		t.Fatalf("counts %+v %v", c, err)
	}
	fixes, err := st.Corrections(ctx)
	if err != nil || len(fixes) != 2 {
		t.Fatalf("corrections %+v %v", fixes, err)
	}

	// A second pass has nothing to do; checking again keeps what was
	// guessed at first.
	s.Pass(ctx)
	if s.done != 0 {
		t.Errorf("second pass looked at %d", s.done)
	}
	st.CheckAgain(ctx)
	s.Pass(ctx)
	if s.done != 4 {
		t.Errorf("check again looked at %d", s.done)
	}
	check(wrong, 11, true)

	// A new copy starts unchecked.
	add("Wrong2", "W.mkv", 1280, 720, 0)
	if c, _ := st.CheckCounts(ctx); c.Checked != 4 || c.Files != 5 {
		t.Errorf("after a new file %+v", c)
	}
}

func TestParse(t *testing.T) {
	m, err := Parse([]byte(`{"format":{"duration":"7200.5","bit_rate":"60000000","tags":{"TITLE":"Blade Runner: The Final Cut"}},"streams":[
		{"codec_type":"video","codec_name":"mjpeg","width":600,"height":900,"disposition":{"attached_pic":1}},
		{"codec_type":"video","codec_name":"hevc","width":3840,"height":1608,"pix_fmt":"yuv420p10le","color_transfer":"smpte2084",
		 "side_data_list":[{"side_data_type":"DOVI configuration record"}]},
		{"codec_type":"audio","codec_name":"truehd","channels":8},
		{"codec_type":"audio","codec_name":"ac3","channels":6}]}`))
	if err != nil {
		t.Fatal(err)
	}
	want := quality.Media{Width: 3840, Height: 1608, VideoCodec: "hevc", BitDepth: 10, HDR: "Dolby Vision", AudioCodec: "TrueHD", Channels: 8, Bitrate: 60000000, Duration: 7200.5, Title: "Blade Runner: The Final Cut"}
	if m != want {
		t.Fatalf("got %+v", m)
	}
	if m.Resolution() != 2160 {
		t.Error("resolution")
	}
	if _, err := Parse([]byte(`{"streams":[{"codec_type":"audio","codec_name":"flac"}]}`)); err == nil {
		t.Error("no picture should fail")
	}
}

// A file whose share stops answering isn't taken for unreadable for good:
// ffprobe running out of time is marked to try again later, and the pass
// goes on without looking at it again.
func TestStalledProbe(t *testing.T) {
	dir := t.TempDir()
	slow := filepath.Join(dir, "ffprobe")
	if err := os.WriteFile(slow, []byte("#!/bin/sh\nexec sleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := probeTimeout
	probeTimeout = 100 * time.Millisecond
	defer func() { probeTimeout = old }()
	_, err := FFprobe(context.Background(), slow, filepath.Join(dir, "a.mkv"))
	if !errors.Is(err, ErrStalled) {
		t.Fatalf("timed out: %v", err)
	}

	ctx := context.Background()
	st := storetest.Open(t)
	profiles, _ := st.Profiles(ctx)
	m, err := st.AddMovie(ctx, store.Movie{TMDBID: 1, Title: "Stalled", ProfileID: profiles[0].ID, Path: filepath.Join(dir, "Stalled")})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "Stalled", "Stalled.mkv")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte("x"), 0o644)
	f, err := st.AddFile(ctx, store.File{Kind: "movie", OwnerID: m.ID, Path: path, Size: 1})
	if err != nil {
		t.Fatal(err)
	}
	probes := 0
	s := &Service{Store: st, Log: slog.New(slog.DiscardHandler), Probe: func(ctx context.Context, p string) (quality.Media, error) {
		probes++
		return FFprobe(ctx, slow, p)
	}}
	if err := s.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if probes != 1 {
		t.Errorf("probed %d times in a pass", probes)
	}
	got, _ := st.FileByID(ctx, f.ID)
	if got.Media == nil || !got.Media.Retry || got.Media.Error == "" {
		t.Errorf("media %+v", got.Media)
	}
	s.Pass(ctx)
	if probes != 1 {
		t.Errorf("looked again straight away: %d", probes)
	}
}
