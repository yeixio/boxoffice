package problems

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
)

func touch(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

type library struct {
	st         *store.Store
	movies, tv string
	ids        map[string]int64
	episodeIDs map[string]int64
}

func (l *library) movie(t *testing.T, title string, year int, folder string, file string, size int) int64 {
	t.Helper()
	ctx := context.Background()
	m, err := l.st.AddMovie(ctx, store.Movie{TMDBID: int64(len(l.ids) + 1), Title: title, Year: year, Monitored: true, ProfileID: 1,
		RootFolder: l.movies, Path: filepath.Join(l.movies, folder)})
	if err != nil {
		t.Fatal(err)
	}
	l.ids[title] = m.ID
	if file != "" {
		path := filepath.Join(l.movies, folder, file)
		if size > 0 {
			touch(t, path, size)
		}
		if _, err := l.st.AddFile(ctx, store.File{Kind: "movie", OwnerID: m.ID, Path: path, Size: int64(size)}); err != nil {
			t.Fatal(err)
		}
	}
	return m.ID
}

func newLibrary(t *testing.T) *library {
	t.Helper()
	dir := t.TempDir()
	l := &library{st: storetest.Open(t), movies: filepath.Join(dir, "movies"), tv: filepath.Join(dir, "tv"), ids: map[string]int64{}, episodeIDs: map[string]int64{}}
	ctx := context.Background()
	for kind, p := range map[string]string{"movie": l.movies, "series": l.tv} {
		os.MkdirAll(p, 0o755)
		if _, err := l.st.AddRootFolder(ctx, kind, p); err != nil {
			t.Fatal(err)
		}
	}
	m := l.movies
	// Fine: named and recorded the way Box Office does.
	l.movie(t, "Fargo", 1996, "Fargo (1996)", "Fargo (1996).mkv", 10)
	// Fine too: extra tags after the title, and ids on the folder.
	l.movie(t, "Se7en", 1995, "Se7en (1995) {tmdb-807}", "Se7en (1995) Bluray-1080p.mkv", 10)
	// Two copies.
	l.movie(t, "Heat", 1995, "Heat (1995)", "Heat (1995).mkv", 10)
	touch(t, filepath.Join(m, "Heat (1995)", "Heat.1995.720p.mkv"), 11)
	// A release name, with subtitles, and its extras left alone.
	l.movie(t, "The Matrix", 1999, "The Matrix (1999)", "The.Matrix.1999.1080p.BluRay.x264.mkv", 12)
	touch(t, filepath.Join(m, "The Matrix (1999)", "The.Matrix.1999.1080p.BluRay.x264.en.srt"), 1)
	touch(t, filepath.Join(m, "The Matrix (1999)", "Featurettes", "Making of.mkv"), 1)
	// Parts.
	l.movie(t, "Alien", 1979, "Alien (1979)", "", 0)
	touch(t, filepath.Join(m, "Alien (1979)", "Alien CD1.avi"), 5)
	touch(t, filepath.Join(m, "Alien (1979)", "Alien CD2.avi"), 6)
	// A title named with a part is not in parts.
	l.movie(t, "Kill Bill Vol 1", 2003, "Kill Bill Vol 1 (2003)", "Kill Bill Vol 1 (2003).mkv", 4)
	// A disc image.
	l.movie(t, "Brazil", 1985, "Brazil (1985)", "", 0)
	touch(t, filepath.Join(m, "Brazil (1985)", "BRAZIL.iso"), 20)
	// A movie on the list, loose in the root, with no copy.
	l.movie(t, "Up", 2009, "Up (2009)", "", 0)
	touch(t, filepath.Join(m, "Up.2009.1080p.mp4"), 7)
	// Not on the list, a leftover, an empty folder.
	touch(t, filepath.Join(m, "Random Film 2001.mkv"), 3)
	touch(t, filepath.Join(m, "Something Else (2010)", "x.mkv"), 3)
	touch(t, filepath.Join(m, "Fargo (1996)", "RARBG.com.url"), 1)
	touch(t, filepath.Join(m, "Fargo (1996)", "sample.mkv"), 1)
	os.MkdirAll(filepath.Join(m, "Empty"), 0o755)
	// A recorded copy that's gone.
	l.movie(t, "Gone", 2000, "Gone (2000)", "Gone (2000).mkv", 0)
	os.MkdirAll(filepath.Join(m, "Gone (2000)"), 0o755)
	touch(t, filepath.Join(m, "Gone (2000)", "poster.jpg"), 1)
	// A folder not named for its movie.
	l.movie(t, "Jaws", 1975, "jaws", "Jaws (1975).mkv", 8)
	// Recycled copies are never looked at.
	touch(t, filepath.Join(m, ".recycle", "2026-01-01", "old.mkv"), 1)

	// A show.
	ctx2 := context.Background()
	show, err := l.st.AddSeries(ctx2, store.Series{TMDBID: 99, Title: "Lost", Year: 2004, Monitored: true, ProfileID: 1, RootFolder: l.tv, Path: filepath.Join(l.tv, "Lost (2004)")})
	if err != nil {
		t.Fatal(err)
	}
	l.ids["Lost"] = show.ID
	for n, title := range []string{"Pilot", "Pilot Part 2", "Tabula Rasa"} {
		if err := l.st.UpsertEpisode(ctx2, store.Episode{SeriesID: show.ID, Season: 1, Number: n + 1, Title: title, Monitored: true}); err != nil {
			t.Fatal(err)
		}
	}
	eps, _ := l.st.Episodes(ctx2, show.ID)
	for _, e := range eps {
		l.episodeIDs[episodeCode(e.Season, []int{e.Number})] = e.ID
	}
	ep1 := filepath.Join(l.tv, "Lost (2004)", "Season 01", "Lost (2004) - S01E01 - Pilot.mkv")
	touch(t, ep1, 9)
	l.st.AddFile(ctx2, store.File{Kind: "episode", OwnerID: l.episodeIDs["S01E01"], Path: ep1, Size: 9})
	touch(t, filepath.Join(l.tv, "Lost (2004)", "lost.s01e02.720p.mkv"), 9)
	touch(t, filepath.Join(l.tv, "Lost (2004)", "lost.s01e02.720p.srt"), 1)
	touch(t, filepath.Join(l.tv, "Lost (2004)", "Season 01", "bonus clip.mkv"), 2)
	ep3 := filepath.Join(l.tv, "Lost (2004)", "Season 2", "Lost - S01E03.mkv")
	touch(t, ep3, 9)
	l.st.AddFile(ctx2, store.File{Kind: "episode", OwnerID: l.episodeIDs["S01E03"], Path: ep3, Size: 9})
	return l
}

func byKind(rep Report, kind string) []Problem {
	var out []Problem
	for _, p := range rep.Problems {
		if p.Kind == kind {
			out = append(out, p)
		}
	}
	return out
}

func titled(list []Problem, name string) *Problem {
	for i := range list {
		if list[i].Title != nil && list[i].Title.Name == name {
			return &list[i]
		}
	}
	return nil
}

func names(list []Problem) []string {
	var out []string
	for _, p := range list {
		if p.Title != nil {
			out = append(out, p.Title.Name+" "+p.Detail)
		} else {
			out = append(out, filepath.Base(p.Files[0].Path))
		}
	}
	slices.Sort(out)
	return out
}

func snapshot(t *testing.T, root string) []string {
	var out []string
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		info, _ := d.Info()
		out = append(out, path+" "+info.ModTime().String()+" "+info.Mode().String())
		return nil
	})
	return out
}

func TestFind(t *testing.T) {
	l := newLibrary(t)
	before := snapshot(t, filepath.Dir(l.movies))
	rep, err := Find(context.Background(), l.st, nil)
	if err != nil {
		t.Fatal(err)
	}
	if after := snapshot(t, filepath.Dir(l.movies)); !slices.Equal(before, after) {
		t.Fatal("looking changed the disk")
	}
	if len(rep.Roots) != 2 || !rep.Roots[0].Readable || !rep.Roots[0].Writable {
		t.Fatalf("roots %+v", rep.Roots)
	}
	want := map[string][]string{
		Duplicate: {"Heat "},
		Naming:    {"Jaws folder", "Lost S01E03", "The Matrix "},
		Parts:     {"Alien 2"},
		DiscImage: {"Brazil "},
		Untracked: {"Lost S01E02", "Up "},
		Missing:   {"Gone "},
		Unknown:   {"Lost "},
		NotListed: {"Random Film 2001.mkv", "Something Else (2010)"},
		Leftover:  {"Fargo ", "Fargo "},
		EmptyDir:  {"Empty"},
	}
	for kind, w := range want {
		if got := names(byKind(rep, kind)); !slices.Equal(got, w) {
			t.Errorf("%s: got %q, want %q", kind, got, w)
		}
	}
	if len(rep.Problems) != 15 {
		for _, p := range rep.Problems {
			t.Logf("%s %v %s", p.Kind, p.Title, p.Files[0].Path)
		}
		t.Errorf("%d problems", len(rep.Problems))
	}
	matrix := titled(byKind(rep, Naming), "The Matrix")
	if len(matrix.Fix.Moves) != 2 || filepath.Base(matrix.Fix.Moves[0].To) != "The Matrix (1999).mkv" || filepath.Base(matrix.Fix.Moves[1].To) != "The Matrix (1999).en.srt" {
		t.Errorf("matrix moves %+v", matrix.Fix.Moves)
	}
	lost := titled(byKind(rep, Untracked), "Lost")
	if lost.Fix.Action != ActRecord || len(lost.Fix.Moves) != 2 || !strings.HasSuffix(lost.Fix.Moves[0].To, filepath.Join("Lost (2004)", "Season 01", "Lost (2004) - S01E02 - Pilot Part 2.mkv")) {
		t.Errorf("lost fix %+v", lost.Fix)
	}
	ep3 := titled(byKind(rep, Naming), "Lost")
	if !strings.HasSuffix(ep3.Fix.Moves[0].To, filepath.Join("Season 01", "Lost (2004) - S01E03 - Tabula Rasa.mkv")) {
		t.Errorf("ep3 fix %+v", ep3.Fix)
	}
	up := titled(byKind(rep, Untracked), "Up")
	if up.Fix.Moves[0].To != filepath.Join(l.movies, "Up (2009)", "Up (2009).mp4") {
		t.Errorf("up fix %+v", up.Fix)
	}
	heat := titled(byKind(rep, Duplicate), "Heat")
	if heat.Fix.Keep != filepath.Join(l.movies, "Heat (1995)", "Heat (1995).mkv") {
		t.Errorf("heat keeps %s", heat.Fix.Keep)
	}
}

func find(t *testing.T, l *library, kind, title string) Problem {
	t.Helper()
	rep, err := Find(context.Background(), l.st, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range byKind(rep, kind) {
		if title == "" && p.Title == nil || p.Title != nil && p.Title.Name == title {
			return p
		}
	}
	t.Fatalf("no %s problem for %q", kind, title)
	return Problem{}
}

func noTools() (Tools, error) { return Tools{}, ErrNoFFmpeg }

func TestKeepOneAndPutBack(t *testing.T) {
	l := newLibrary(t)
	ctx := context.Background()
	p := find(t, l, Duplicate, "Heat")
	other := filepath.Join(l.movies, "Heat (1995)", "Heat.1995.720p.mkv")
	keep := filepath.Join(l.movies, "Heat (1995)", "Heat (1995).mkv")
	// Keep the one that isn't recorded: it becomes the copy.
	fix, err := Apply(ctx, l.st, noTools, p, other, nil)
	if err != nil {
		t.Fatal(err)
	}
	if exists(keep) || !exists(other) {
		t.Fatal("the other copy wasn't set aside")
	}
	aside := fix.Files[0].Dest
	if !strings.Contains(aside, filepath.Join(".recycle", RecycleSub)) || !exists(aside) {
		t.Fatalf("set aside to %s", aside)
	}
	m, _ := l.st.Movie(ctx, l.ids["Heat"])
	if m.File == nil || m.File.Path != other {
		t.Fatalf("copy is %+v", m.File)
	}
	// Emptying the recycle folders' day folders leaves it be (see library.EmptyRecycle).
	if fixes, _ := l.st.ProblemFixes(ctx, 5); len(fixes) != 1 || fixes[0].State != store.FixWaiting {
		t.Fatalf("fixes %+v", fixes)
	}
	if err := PutBack(ctx, l.st, fix.ID); err != nil {
		t.Fatal(err)
	}
	if !exists(keep) || !exists(other) || exists(aside) {
		t.Fatal("not put back")
	}
	m, _ = l.st.Movie(ctx, l.ids["Heat"])
	if m.File == nil || m.File.Path != keep {
		t.Fatalf("copy after putting back is %+v", m.File)
	}
	if exists(filepath.Join(l.movies, ".recycle", RecycleSub)) {
		t.Error("left an empty recycle folder")
	}
	if err := PutBack(ctx, l.st, fix.ID); err == nil {
		t.Error("put back twice")
	}
}

func TestDeleteForGood(t *testing.T) {
	l := newLibrary(t)
	ctx := context.Background()
	p := find(t, l, Leftover, "Fargo")
	fix, err := Apply(ctx, l.st, noTools, p, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	aside := fix.Files[0].Dest
	freed, err := DeleteForGood(ctx, l.st, fix.ID)
	if err != nil || freed != 1 {
		t.Fatal(freed, err)
	}
	if exists(aside) || exists(p.Files[0].Path) {
		t.Error("not deleted")
	}
	if !exists(filepath.Join(l.movies, "Fargo (1996)", "Fargo (1996).mkv")) {
		t.Error("deleted the movie")
	}
	if err := PutBack(ctx, l.st, fix.ID); err == nil {
		t.Error("put back after deleting")
	}
}

func TestRenameAndPutBack(t *testing.T) {
	l := newLibrary(t)
	ctx := context.Background()
	p := find(t, l, Naming, "The Matrix")
	fix, err := Apply(ctx, l.st, noTools, p, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(l.movies, "The Matrix (1999)")
	if !exists(filepath.Join(dir, "The Matrix (1999).mkv")) || !exists(filepath.Join(dir, "The Matrix (1999).en.srt")) {
		t.Fatal("not renamed")
	}
	m, _ := l.st.Movie(ctx, l.ids["The Matrix"])
	if m.File.Path != filepath.Join(dir, "The Matrix (1999).mkv") {
		t.Fatalf("record at %s", m.File.Path)
	}
	if err := PutBack(ctx, l.st, fix.ID); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(dir, "The.Matrix.1999.1080p.BluRay.x264.mkv")) || !exists(filepath.Join(dir, "The.Matrix.1999.1080p.BluRay.x264.en.srt")) {
		t.Fatal("not renamed back")
	}
	m, _ = l.st.Movie(ctx, l.ids["The Matrix"])
	if filepath.Base(m.File.Path) != "The.Matrix.1999.1080p.BluRay.x264.mkv" {
		t.Fatalf("record at %s", m.File.Path)
	}
}

func TestFolderRename(t *testing.T) {
	l := newLibrary(t)
	ctx := context.Background()
	p := find(t, l, Naming, "Jaws")
	if _, err := Apply(ctx, l.st, noTools, p, "", nil); err != nil {
		t.Fatal(err)
	}
	m, _ := l.st.Movie(ctx, l.ids["Jaws"])
	if m.Path != filepath.Join(l.movies, "Jaws (1975)") || m.File.Path != filepath.Join(l.movies, "Jaws (1975)", "Jaws (1975).mkv") || !exists(m.File.Path) {
		t.Fatalf("jaws at %s, %s", m.Path, m.File.Path)
	}
}

func TestRecordEpisode(t *testing.T) {
	l := newLibrary(t)
	ctx := context.Background()
	p := find(t, l, Untracked, "Lost")
	if _, err := Apply(ctx, l.st, noTools, p, "", nil); err != nil {
		t.Fatal(err)
	}
	e, _ := l.st.Episode(ctx, l.episodeIDs["S01E02"])
	want := filepath.Join(l.tv, "Lost (2004)", "Season 01", "Lost (2004) - S01E02 - Pilot Part 2.mkv")
	if e.File == nil || e.File.Path != want || !exists(want) || !exists(strings.TrimSuffix(want, ".mkv")+".srt") {
		t.Fatalf("episode file %+v", e.File)
	}
}

func TestMissingAndChanged(t *testing.T) {
	l := newLibrary(t)
	ctx := context.Background()
	p := find(t, l, Missing, "Gone")
	fix, err := Apply(ctx, l.st, noTools, p, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := l.st.Movie(ctx, l.ids["Gone"]); m.File != nil {
		t.Fatal("still recorded")
	}
	if err := PutBack(ctx, l.st, fix.ID); err != nil {
		t.Fatal(err)
	}
	if m, _ := l.st.Movie(ctx, l.ids["Gone"]); m.File == nil {
		t.Fatal("not recorded again")
	}
	// A file that changed since it was found isn't touched.
	p = find(t, l, NotListed, "")
	touch(t, p.Files[0].Path, 99)
	if _, err := Apply(ctx, l.st, noTools, p, "", nil); err == nil {
		t.Fatal("fixed a changed file")
	}
	if !exists(p.Files[0].Path) {
		t.Fatal("moved a changed file")
	}
}

func TestLockedAndReadOnly(t *testing.T) {
	l := newLibrary(t)
	ctx := context.Background()
	m, _ := l.st.Movie(ctx, l.ids["Heat"])
	profiles, _ := l.st.Profiles(ctx)
	for _, pr := range profiles {
		if pr.Locked {
			m.ProfileID = pr.ID
		}
	}
	if err := l.st.UpdateMovie(ctx, m); err != nil {
		t.Fatal(err)
	}
	p := find(t, l, Duplicate, "Heat")
	if p.Fix.Blocked != "locked" {
		t.Fatalf("blocked %q", p.Fix.Blocked)
	}
	if _, err := Apply(ctx, l.st, noTools, p, "", nil); err == nil {
		t.Fatal("fixed a Do not touch title")
	}
}

func TestPart(t *testing.T) {
	for name, want := range map[string]int{
		"Alien CD1.avi": 1, "Alien.cd2.avi": 2, "Alien - Part 3.mkv": 3, "alien (disc 1).mkv": 1, "Alien pt2.mp4": 2,
		"Alien.mkv": 0, "Alien Part.mkv": 0, "Apart1.mkv": 0,
	} {
		if _, got := Part(name); got != want {
			t.Errorf("%s: %d, want %d", name, got, want)
		}
	}
}

// TestJoin joins two real parts with FFmpeg, when it's installed.
func TestJoin(t *testing.T) {
	tools, err := FindTools()
	if err != nil {
		t.Skip("no FFmpeg")
	}
	l := newLibrary(t)
	ctx := context.Background()
	dir := filepath.Join(l.movies, "Alien (1979)")
	for i, name := range []string{"Alien CD1.avi", "Alien CD2.avi"} {
		out := filepath.Join(dir, name)
		os.Remove(out)
		cmd := exec.Command(tools.FFmpeg, "-v", "error", "-f", "lavfi", "-i", "testsrc=duration=3:size=64x48:rate=10",
			"-f", "lavfi", "-i", "sine=duration=3", "-shortest", "-c:v", "mpeg4", "-c:a", "mp2", "-metadata", "title=part"+string(rune('1'+i)), out)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("can't make test videos: %v %s", err, b)
		}
	}
	p := find(t, l, Parts, "Alien")
	var last float64
	fix, err := Apply(ctx, l.st, func() (Tools, error) { return tools, nil }, p, "", func(f float64) { last = f })
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "Alien (1979).mkv")
	got, err := tools.probe(ctx, "-i", out)
	if err != nil || got.Duration < 5.5 || got.Duration > 6.5 {
		t.Fatalf("joined %+v %v", got, err)
	}
	if last < 0.5 {
		t.Errorf("progress got to %v", last)
	}
	if exists(filepath.Join(dir, "Alien CD1.avi")) || exists(filepath.Join(dir, "Alien CD2.avi")) {
		t.Error("parts not set aside")
	}
	if m, _ := l.st.Movie(ctx, l.ids["Alien"]); m.File == nil || m.File.Path != out {
		t.Errorf("copy %+v", m.File)
	}
	if err := PutBack(ctx, l.st, fix.ID); err != nil {
		t.Fatal(err)
	}
	if exists(out) || !exists(filepath.Join(dir, "Alien CD1.avi")) {
		t.Error("not put back")
	}
	if m, _ := l.st.Movie(ctx, l.ids["Alien"]); m.File != nil {
		t.Errorf("copy after putting back %+v", m.File)
	}
}
