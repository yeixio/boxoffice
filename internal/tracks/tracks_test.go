package tracks

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
)

func TestPlan(t *testing.T) {
	streams := []Stream{
		{0, "video", ""}, {1, "audio", "ja"}, {2, "audio", "en"}, {3, "audio", "fr"}, {4, "audio", ""},
		{5, "subtitle", "en"}, {6, "subtitle", "de"}, {7, "subtitle", "es"}, {8, "subtitle", ""}, {9, "attachment", ""},
	}
	indexes := func(drop []Stream) []int {
		var out []int
		for _, st := range drop {
			out = append(out, st.Index)
		}
		return out
	}
	// Japanese (as made) and English stay; Spanish subtitles too, as
	// Settings → Subtitles wants them. Untagged tracks always stay.
	if got := indexes(Plan(streams, []string{"en", "ja"}, []string{"es"})); len(got) != 2 || got[0] != 3 || got[1] != 6 {
		t.Fatalf("dropped %v", got)
	}
	// No audio in a language kept: the audio is left whole, the
	// subtitles still go.
	only := []Stream{{0, "video", ""}, {1, "audio", "fr"}, {2, "audio", "de"}, {3, "subtitle", "fr"}}
	if got := indexes(Plan(only, []string{"en"}, nil)); len(got) != 1 || got[0] != 3 {
		t.Fatalf("dropped %v", got)
	}
	if got := Plan(streams[:3], []string{"en", "ja"}, nil); len(got) != 0 {
		t.Fatalf("dropped %v", got)
	}
}

func TestConfig(t *testing.T) {
	c := Config{On: true, Languages: []string{"English", "eng", "pt-BR", "nonsense"}}
	c.Normalize()
	if len(c.Languages) != 2 || c.Languages[0] != "en" || c.Languages[1] != "pt" {
		t.Fatalf("languages %v", c.Languages)
	}
	if err := (Config{On: true}).Check(); err == nil {
		t.Fatal("on with no languages to keep")
	}
}

// video makes a short Matroska file with Japanese, English and French
// audio and English and German subtitles.
func video(t *testing.T, path string) {
	t.Helper()
	srt := filepath.Join(t.TempDir(), "s.srt")
	os.WriteFile(srt, []byte("1\n00:00:00,000 --> 00:00:01,000\nHello\n"), 0o644)
	args := []string{"-v", "error", "-y", "-f", "lavfi", "-i", "testsrc=d=2:s=64x64:r=5"}
	for range 3 {
		args = append(args, "-f", "lavfi", "-i", "sine=d=2")
	}
	args = append(args, "-i", srt, "-i", srt, "-map", "0", "-map", "1", "-map", "2", "-map", "3", "-map", "4", "-map", "5",
		"-c:v", "mpeg4", "-c:a", "pcm_s16le", "-c:s", "srt",
		"-metadata:s:a:0", "language=jpn", "-metadata:s:a:1", "language=eng", "-metadata:s:a:2", "language=fre",
		"-metadata:s:s:0", "language=eng", "-metadata:s:s:1", "language=ger", path)
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg can't make the test video: %v: %s", err, out)
	}
}

func TestStrip(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg isn't installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe isn't installed")
	}
	ctx := context.Background()
	st := storetest.Open(t)
	root := t.TempDir()
	profiles, _ := st.Profiles(ctx)
	m, err := st.AddMovie(ctx, store.Movie{TMDBID: 129, Title: "Spirited Away", Year: 2001, ProfileID: profiles[0].ID, RootFolder: root, Path: filepath.Join(root, "Spirited Away (2001)")})
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(m.Path, 0o755)
	path := filepath.Join(m.Path, "Spirited Away (2001).mkv")
	video(t, path)
	info, _ := os.Stat(path)
	f, err := st.AddFile(ctx, store.File{Kind: "movie", OwnerID: m.ID, Path: path, Size: info.Size()})
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveConfig(ctx, st, Config{On: true, Languages: []string{"en"}}); err != nil {
		t.Fatal(err)
	}
	s := &Service{Store: st, Original: func(context.Context, string, int64) (string, error) { return "ja", nil }}
	s.Imported(ctx, store.Download{Kind: "movie", TitleID: m.ID, ImportedTo: path})

	streams, _, err := Probe(ctx, "ffprobe", path)
	if err != nil {
		t.Fatal(err)
	}
	var langs []string
	for _, st := range streams {
		langs = append(langs, st.Kind+":"+st.Language)
	}
	if want := []string{"video:", "audio:ja", "audio:en", "subtitle:en"}; len(langs) != len(want) || langs[1] != want[1] || langs[2] != want[2] || langs[3] != want[3] {
		t.Fatalf("tracks left: %v", langs)
	}
	after, _ := os.Stat(path)
	if after.Size() >= info.Size() {
		t.Fatalf("no smaller: %d then %d", info.Size(), after.Size())
	}
	if got, _ := st.FileByID(ctx, f.ID); got.Size != after.Size() {
		t.Fatalf("size noted %d, is %d", got.Size, after.Size())
	}
	saved, _ := s.Saved(ctx)
	if saved.Files != 1 || saved.Bytes != info.Size()-after.Size() {
		t.Fatalf("saved %+v", saved)
	}
	// The original is in the recycle folder, whole.
	kept, _ := filepath.Glob(filepath.Join(root, ".recycle", "*", "Spirited Away (2001).mkv"))
	if len(kept) != 1 {
		t.Fatalf("the original isn't in the recycle folder: %v", kept)
	}
	if o, _ := os.Stat(kept[0]); o.Size() != info.Size() {
		t.Fatal("the recycled original isn't whole")
	}
	if left, _ := filepath.Glob(filepath.Join(m.Path, "*tracks-tmp*")); len(left) != 0 {
		t.Fatalf("left behind: %v", left)
	}

	// A second go finds nothing to drop and leaves the file alone.
	s.Imported(ctx, store.Download{Kind: "movie", TitleID: m.ID, ImportedTo: path})
	if again, _ := os.Stat(path); again.Size() != after.Size() || !again.ModTime().Equal(after.ModTime()) {
		t.Fatal("written again with nothing to drop")
	}
	if saved, _ := s.Saved(ctx); saved.Files != 1 {
		t.Fatalf("saved %+v", saved)
	}
}

// Off, or a title on a Do not touch profile, or one whose language isn't
// known: the file is left as it came.
func TestStripLeftAlone(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg isn't installed")
	}
	ctx := context.Background()
	st := storetest.Open(t)
	root := t.TempDir()
	profiles, _ := st.Profiles(ctx)
	m, err := st.AddMovie(ctx, store.Movie{TMDBID: 129, Title: "Spirited Away", Year: 2001, ProfileID: profiles[0].ID, RootFolder: root, Path: filepath.Join(root, "Spirited Away (2001)")})
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(m.Path, 0o755)
	path := filepath.Join(m.Path, "Spirited Away (2001).mkv")
	video(t, path)
	info, _ := os.Stat(path)
	if _, err := st.AddFile(ctx, store.File{Kind: "movie", OwnerID: m.ID, Path: path, Size: info.Size()}); err != nil {
		t.Fatal(err)
	}
	original := "ja"
	s := &Service{Store: st, Original: func(context.Context, string, int64) (string, error) { return original, nil }}
	same := func(why string) {
		t.Helper()
		s.Imported(ctx, store.Download{Kind: "movie", TitleID: m.ID, ImportedTo: path})
		if now, _ := os.Stat(path); now.Size() != info.Size() {
			t.Fatalf("%s: the file was written again", why)
		}
	}
	same("off")
	SaveConfig(ctx, st, Config{On: true, Languages: []string{"en"}})
	original = "xx"
	same("made in no language")
	original = "ja"
	for _, p := range profiles {
		if p.Locked {
			m.ProfileID = p.ID
			if err := st.UpdateMovie(ctx, m); err != nil {
				t.Fatal(err)
			}
			same("do not touch")
		}
	}
}
