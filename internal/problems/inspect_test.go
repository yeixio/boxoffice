package problems

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestEdition(t *testing.T) {
	for name, want := range map[string]string{
		"Dances with Wolves (1990) Extended Cut br-disk_1.mkv": "Extended cut",
		"Blade.Runner.1982.Final.Cut.1080p.mkv":                "Final cut",
		"Aliens (1986) {edition-Special Edition}.mkv":          "Special edition",
		"Brazil (1985) Director's Cut.mkv":                     "Director's cut",
		"stargate (1994) br-disk_1.mkv":                        "",
	} {
		if got := Edition(name); got != want {
			t.Errorf("Edition(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestLikelyCopies(t *testing.T) {
	m := func(min float64, w, h int, hdr string) *Media {
		return &Media{Duration: min * 60, Width: w, Height: h, HDR: hdr}
	}
	copies := []File{{Path: "a", Size: 44, Media: m(181, 1920, 1080, "")}, {Path: "b", Size: 43, Media: m(236, 1920, 1080, "")}, {Path: "c", Size: 44, Media: m(181, 3840, 2160, "HDR10")}}
	if !likelyCopies(copies) {
		t.Error("three movie-length files aren't copies")
	}
	if b := best(copies); b != "c" {
		t.Errorf("best is %s, want the 4K one", b)
	}
	if b := best(copies[:2]); b != "b" {
		t.Errorf("best is %s, want the longer cut", b)
	}
	halves := []File{{Media: m(62, 720, 480, "")}, {Media: m(58, 720, 480, "")}}
	if likelyCopies(halves) {
		t.Error("two halves of a two-hour movie are parts")
	}
	if !likelyCopies([]File{{Media: m(85, 1, 1, "")}, {Media: m(90, 1, 1, "")}}) {
		t.Error("two feature-length files are copies")
	}
	if likelyCopies([]File{{Media: m(120, 1, 1, "")}, {Media: m(50, 1, 1, "")}}) {
		t.Error("a long part and a short one are parts")
	}
	if likelyCopies([]File{{Media: m(120, 1, 1, "")}, {}}) {
		t.Error("a file FFmpeg couldn't read isn't known to be a copy")
	}
}

func TestPartsAsCopies(t *testing.T) {
	tools, err := FindTools()
	if err != nil {
		t.Skip("no FFmpeg")
	}
	l := newLibrary(t)
	ctx := context.Background()
	dir := filepath.Join(l.movies, "Alien (1979)")
	// Two movie-length "discs", the second sharper: tiny and fast to make.
	for i, size := range []string{"32x24", "64x48"} {
		out := filepath.Join(dir, []string{"Alien CD1.avi", "Alien CD2.avi"}[i])
		os.Remove(out)
		cmd := exec.Command(tools.FFmpeg, "-v", "error", "-f", "lavfi", "-i", "color=c=black:duration=4900:size="+size+":rate=1",
			"-f", "lavfi", "-i", "anullsrc=r=8000:cl=stereo", "-t", "4900", "-c:v", "mpeg4", "-c:a", "pcm_s16le", out)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("can't make test videos: %v %s", err, b)
		}
	}
	s := &Service{Store: l.st, Log: slog.New(slog.DiscardHandler), Tools: func() (Tools, error) { return tools, nil }}
	lookAndWait(t, s, false)
	_, rep := s.State()
	p := titled(byKind(*rep, Parts), "Alien")
	if p == nil || !p.Copies || p.Fix.Keep != filepath.Join(dir, "Alien CD2.avi") {
		t.Fatalf("parts %+v", p)
	}
	f := p.Files[1]
	if f.Media == nil || f.Media.Width != 64 || f.Media.Video != "MPEG-4" || len(f.Media.Audio) != 1 || f.Media.Audio[0] != "PCM 2.0" {
		t.Fatalf("media %+v", f.Media)
	}
	if err := s.Fix([]string{p.ID}, nil, map[string]string{p.ID: "rename"}); err == nil {
		t.Fatal("took a way to fix it that isn't one")
	}
	if err := s.Fix([]string{p.ID}, nil, map[string]string{p.ID: Instead}); err != nil {
		t.Fatal(err)
	}
	for range 500 {
		if st, _ := s.State(); st.Fixing == nil && len(st.Queued) == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st, _ := s.State(); len(st.Failed) > 0 {
		t.Fatalf("failed %+v", st.Failed)
	}
	if !exists(filepath.Join(dir, "Alien CD2.avi")) || exists(filepath.Join(dir, "Alien CD1.avi")) || exists(filepath.Join(dir, "Alien (1979).mkv")) {
		t.Fatal("didn't keep the sharper copy alone")
	}
	if m, _ := l.st.Movie(ctx, l.ids["Alien"]); m.File == nil || m.File.Path != filepath.Join(dir, "Alien CD2.avi") {
		t.Errorf("copy %+v", m.File)
	}
}
