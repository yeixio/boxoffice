package problems

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
	"golang.org/x/text/unicode/norm"
)

func TestRepair(t *testing.T) {
	for in, want := range map[string]string{
		"WALLÂ·E (2008)":                "WALL·E (2008)",
		"LÃ©on The Professional (1994)": "Léon The Professional (1994)",
		"Donâ€™t Look Up (2021)":        "Don’t Look Up (2021)",
		"AmÃƒÂ©lie (2001)":              "Amélie (2001)", // garbled twice
		"L\xe9on (1994)":                "Léon (1994)",   // Latin-1 bytes
		norm.NFD.String("TÁR (2022)"):   "TÁR (2022)",
		// Names that aren't garbled stay as they are.
		"Café Society (2016)": "Café Society (2016)",
		"Heat (1995)":         "Heat (1995)",
		"Ä Ö Ü":               "Ä Ö Ü",
		"Amélie":              "Amélie",
	} {
		if got := Repair(in); got != want {
			t.Errorf("Repair(%q) = %q, want %q", in, got, want)
		}
	}
	if !Garbled("WALLÂ·E (2008)") || Garbled(norm.NFD.String("TÁR (2022)")) || Garbled("Café Society (2016)") {
		t.Error("Garbled is off")
	}
}

// Folders named another way than the title Box Office has, garbled or with
// accents written another way, are that title's folder, not something
// that isn't on the list.
func TestFoundFolders(t *testing.T) {
	l := newLibrary(t)
	ctx := context.Background()
	// Box Office has WALL·E in "WALL·E (2008)", but the folder on disk is
	// garbled: its files look missing.
	l.movie(t, "WALL·E", 2008, "WALL·E (2008)", "WALL·E (2008).mkv", 0)
	touch(t, filepath.Join(l.movies, "WALLÂ·E (2008)", "WALL·E (2008).mkv"), 7)
	// TÁR on disk with its accent as a letter and a mark.
	l.movie(t, "TÁR", 2022, "TÁR (2022)", "", 0)
	touch(t, filepath.Join(l.movies, norm.NFD.String("TÁR (2022)"), "x.mkv"), 5)
	// Fargo's folder is there, and a second, garbled one too.
	touch(t, filepath.Join(l.movies, "FÃ¡rgo (1996)", "old.mkv"), 2)
	l.movie(t, "Fárgo", 1996, "Fárgo (1996)", "Fárgo (1996).mkv", 3)

	rep, err := Find(ctx, l.st, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range byKind(rep, NotListed) {
		if p.Title == nil && p.Files[0].Name != "" {
			t.Errorf("%s is listed as not on the list", p.Files[0].Name)
		}
	}
	if titled(byKind(rep, Missing), "WALL·E") != nil {
		t.Error("WALL·E's file is listed as missing")
	}
	wall := titled(byKind(rep, Naming), "WALL·E")
	if wall == nil || wall.Detail != "found_folder" || wall.Files[0].Name != "WALL·E (2008)" || wall.Files[0].Count != 1 {
		t.Fatalf("WALL·E: %+v", wall)
	}
	if m := wall.Fix.Moves; wall.Fix.Action != ActRename || len(m) != 1 || m[0].To != filepath.Join(l.movies, "WALL·E (2008)") {
		t.Fatalf("WALL·E fix %+v", wall.Fix)
	}
	tar := titled(byKind(rep, Naming), "TÁR")
	if tar == nil || tar.Detail != "found_folder" || tar.Files[0].Name != "" {
		t.Fatalf("TÁR: %+v", tar)
	}
	// The second folder's movie is looked at with Fárgo's own: a second
	// copy, not a folder set aside whole.
	second := titled(byKind(rep, Duplicate), "Fárgo")
	if second == nil || len(second.Files) != 2 || second.Fix.Keep != filepath.Join(l.movies, "Fárgo (1996)", "Fárgo (1996).mkv") {
		t.Fatalf("second Fargo folder: %+v", second)
	}
	if p := titled(byKind(rep, NotListed), "Fárgo"); p != nil {
		t.Fatalf("second Fargo folder set aside with a video in it: %+v", p)
	}

	// Renaming puts WALL·E's folder where its record says, and putting it
	// back turns that back.
	fix, err := Apply(ctx, l.st, noTools, *wall, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if fix.Kind != Naming || fix.Detail != "found_folder" {
		t.Errorf("fix noted as %s/%s", fix.Kind, fix.Detail)
	}
	if !exists(filepath.Join(l.movies, "WALL·E (2008)", "WALL·E (2008).mkv")) {
		t.Fatal("not renamed")
	}
	m, _ := l.st.Movie(ctx, l.ids["WALL·E"])
	if m.Path != filepath.Join(l.movies, "WALL·E (2008)") || m.File == nil || m.File.Path != filepath.Join(l.movies, "WALL·E (2008)", "WALL·E (2008).mkv") {
		t.Errorf("records changed: %+v", m)
	}
	if titled(byKind(mustFind(t, l), Naming), "WALL·E") != nil {
		t.Error("still listed after the rename")
	}
	if err := PutBack(ctx, l.st, fix.ID); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(l.movies, "WALLÂ·E (2008)")) || exists(filepath.Join(l.movies, "WALL·E (2008)")) {
		t.Error("not put back")
	}
	if m, _ := l.st.Movie(ctx, l.ids["WALL·E"]); m.Path != filepath.Join(l.movies, "WALL·E (2008)") {
		t.Errorf("putting back moved the record to %s", m.Path)
	}
}

func mustFind(t *testing.T, l *library) Report {
	t.Helper()
	rep, err := Find(context.Background(), l.st, nil)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestReason(t *testing.T) {
	root := "/media/movies"
	aside := func(srcs ...string) []store.ProblemFixFile {
		var out []store.ProblemFixFile
		for _, s := range srcs {
			out = append(out, store.ProblemFixFile{Role: store.FixAside, Src: s})
		}
		return out
	}
	for _, c := range []struct {
		f    store.ProblemFix
		want string
	}{
		{store.ProblemFix{Action: ActSetAside, Files: aside(root + "/WALLÂ·E (2008)")}, NotListed},
		{store.ProblemFix{Action: ActSetAside, Files: aside(root + "/Random.mkv")}, NotListed},
		{store.ProblemFix{Action: ActSetAside, Files: aside("/tv/How It's Made/How It's Made - 27x08.nzb")}, Leftover},
		{store.ProblemFix{Action: ActSetAside, Files: aside(root+"/Heat (1995)/sample.mkv", root+"/Heat (1995)/sample.srt")}, Leftover},
		{store.ProblemFix{Action: ActSetAside, Files: aside(root + "/Heat (1995)/RARBG.url")}, Leftover},
		{store.ProblemFix{Action: ActSetAside, Files: aside(root + "/Brazil.iso")}, DiscImage},
		{store.ProblemFix{Action: ActSetAside, Files: aside(root + "/Lost/Season 01/bonus.mkv")}, Unknown},
		{store.ProblemFix{Action: ActKeepOne}, Duplicate},
		{store.ProblemFix{Action: ActJoin}, Parts},
		{store.ProblemFix{Action: ActSetAside, Kind: Duplicate}, Duplicate},
	} {
		c.f.Root = root
		Reason(&c.f)
		if c.f.Kind != c.want {
			t.Errorf("%s %v: %s, want %s", c.f.Action, c.f.Files, c.f.Kind, c.want)
		}
	}
	f := store.ProblemFix{Kind: NotListed, Title: "LÃ©on (1994)"}
	Reason(&f)
	if f.Title != "Léon (1994)" {
		t.Errorf("title %q", f.Title)
	}
}

// A batch puts back each fix, carrying on past one that can't be.
func TestDecide(t *testing.T) {
	l := newLibrary(t)
	ctx := context.Background()
	var ids []int64
	for _, name := range []string{"Random Film 2001.mkv", "Something Else (2010)"} {
		var p *Problem
		for _, x := range byKind(mustFind(t, l), NotListed) {
			if filepath.Base(x.Files[0].Path) == name {
				p = &x
			}
		}
		fix, err := Apply(ctx, l.st, noTools, *p, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, fix.ID)
	}
	// Something's at the second one's place now, so it can't go back.
	touch(t, filepath.Join(l.movies, "Something Else (2010)", "new.mkv"), 1)

	s := &Service{Store: l.st, Log: slog.New(slog.DiscardHandler), Tools: noTools}
	if err := s.Decide(ids, PutBackAll); err != nil {
		t.Fatal(err)
	}
	if err := s.Decide(ids, PutBackAll); err != ErrBusy && err != nil {
		t.Fatalf("second batch: %v", err)
	}
	var st Status
	for range 200 {
		if st, _ = s.State(); st.Deciding == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st.Deciding != nil {
		t.Fatal("still deciding")
	}
	if len(st.Undecided) != 1 || st.Undecided[0].ID != ids[1] || st.Undecided[0].Title != "Something Else (2010)" {
		t.Fatalf("undecided %+v", st.Undecided)
	}
	if !exists(filepath.Join(l.movies, "Random Film 2001.mkv")) {
		t.Error("the first wasn't put back")
	}
	if f, _ := l.st.ProblemFix(ctx, ids[0]); f.State != store.FixUndone || f.Kind != NotListed {
		t.Errorf("first: %s %s", f.State, f.Kind)
	}
	if f, _ := l.st.ProblemFix(ctx, ids[1]); f.State != store.FixWaiting {
		t.Errorf("second: %s", f.State)
	}
	if err := s.Decide(ids, "shred"); err == nil {
		t.Error("took a decision that isn't one")
	}
}

// A folder a root folder scan linked to a show kept in another folder
// ("Cunk on Britain" for TMDB's "Cunk on...") is looked through with the
// show's own: episodes it hasn't got are moved in and recorded, and
// second copies of ones it has are duplicates.
func TestLinkedFolder(t *testing.T) {
	l := newLibrary(t)
	ctx := context.Background()
	extra := filepath.Join(l.tv, "Lost Season One")
	touch(t, filepath.Join(extra, "Lost.S01E01.HDTV.mkv"), 4) // Lost has it
	touch(t, filepath.Join(extra, "Lost.S01E02.HDTV.mkv"), 5) // and in its folder, unrecorded
	touch(t, filepath.Join(extra, "folder.jpg"), 1)

	// Before it's linked, it's a folder not on the list.
	rep := mustFind(t, l)
	if p := byPath(byKind(rep, NotListed), extra); p == nil || p.Title != nil {
		t.Fatalf("before linking: %+v", p)
	}
	if err := l.st.LinkFolder(ctx, store.FolderLink{Path: extra, Kind: "series", TitleID: l.ids["Lost"]}); err != nil {
		t.Fatal(err)
	}
	rep = mustFind(t, l)
	if p := byPath(byKind(rep, NotListed), extra); p != nil {
		t.Fatalf("linked folder still set aside whole: %+v", p)
	}
	var e1, e2 *Problem
	for i, p := range byKind(rep, Duplicate) {
		if p.Title != nil && p.Title.Name == "Lost" {
			switch p.Detail {
			case "S01E01":
				e1 = &byKind(rep, Duplicate)[i]
			case "S01E02":
				e2 = &byKind(rep, Duplicate)[i]
			}
		}
	}
	if e1 == nil || e1.Fix.Keep != filepath.Join(l.tv, "Lost (2004)", "Season 01", "Lost (2004) - S01E01 - Pilot.mkv") {
		t.Fatalf("S01E01: %+v", e1)
	}
	if e2 == nil || len(e2.Files) != 2 {
		t.Fatalf("S01E02: %+v", e2)
	}

	// Once its videos are gone, what's left can be set aside.
	os.Remove(filepath.Join(extra, "Lost.S01E01.HDTV.mkv"))
	os.Remove(filepath.Join(extra, "Lost.S01E02.HDTV.mkv"))
	rep = mustFind(t, l)
	if p := byPath(byKind(rep, NotListed), extra); p == nil || p.Detail != "second_folder" || p.Title == nil || p.Title.Name != "Lost" {
		t.Fatalf("what's left: %+v", p)
	}
}

func byPath(list []Problem, path string) *Problem {
	for i := range list {
		for _, f := range list[i].Files {
			if f.Path == path {
				return &list[i]
			}
		}
	}
	return nil
}

// TMDB keeps Cunk on Britain and Cunk on Earth as seasons 1 and 2 of one
// show, "Cunk on...". Cunk on Earth's files, numbered S01E01 and so on,
// were recorded as season 1; Cunk on Britain's sit in a folder of their
// own, linked to the show from a root folder scan. Each file goes to the
// episode its name says, and nothing is taken for a copy.
func TestEpisodesNamedForAnotherSeason(t *testing.T) {
	l := newLibrary(t)
	ctx := context.Background()
	show, err := l.st.AddSeries(ctx, store.Series{TMDBID: 79063, Title: "Cunk on...", Year: 2018, Monitored: true, ProfileID: 1, RootFolder: l.tv, Path: filepath.Join(l.tv, "Cunk on... (2018)")})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []store.Episode{{Season: 1, Number: 1, Title: "Beginnings"}, {Season: 1, Number: 2, Title: "The Empire Strikes Back"}, {Season: 2, Number: 1, Title: "In the Beginnings"}, {Season: 2, Number: 2, Title: "Faith Off"}} {
		e.SeriesID, e.Monitored = show.ID, true
		if err := l.st.UpsertEpisode(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	eps, _ := l.st.Episodes(ctx, show.ID)
	id := map[string]int64{}
	for _, e := range eps {
		id[episodeCode(e.Season, []int{e.Number})] = e.ID
	}
	earth1 := filepath.Join(l.tv, "Cunk on... (2018)", "Season 1", "Cunk on Earth - S01E01 - In the Beginnings WEBRip-480p.mp4")
	earth2 := filepath.Join(l.tv, "Cunk on... (2018)", "Season 1", "Cunk on Earth - S01E02 - Faith+Off WEBRip-480p.mp4")
	touch(t, earth1, 6)
	touch(t, earth2, 6)
	l.st.AddFile(ctx, store.File{Kind: "episode", OwnerID: id["S01E01"], Path: earth1, Size: 6})
	l.st.AddFile(ctx, store.File{Kind: "episode", OwnerID: id["S01E02"], Path: earth2, Size: 6})
	britain := filepath.Join(l.tv, "Cunk on Britain")
	britain1 := filepath.Join(britain, "Season 1", "Cunk on Britain - S01E01 - Beginnings WEBRip-480p.mp4")
	touch(t, britain1, 5)
	touch(t, filepath.Join(britain, "Season 1", "Cunk on Britain - S01E02 - The Empire Strikes Back WEBRip-480p.mp4"), 5)
	l.st.LinkFolder(ctx, store.FolderLink{Path: britain, Kind: "series", TitleID: show.ID})

	rep := mustFind(t, l)
	for _, p := range rep.Problems {
		if p.Title != nil && p.Title.ID == show.ID && p.Kind == Duplicate {
			t.Errorf("taken for a copy: %+v", p)
		}
	}
	var wrong, untracked []Problem
	for _, p := range rep.Problems {
		if p.Title == nil || p.Title.ID != show.ID {
			continue
		}
		switch {
		case p.Kind == Naming && p.Detail == "wrong_episode":
			wrong = append(wrong, p)
		case p.Kind == Untracked:
			untracked = append(untracked, p)
		}
	}
	if len(wrong) != 2 || len(untracked) != 2 {
		t.Fatalf("wrong episode %+v, not recorded %+v", wrong, untracked)
	}
	var w *Problem
	for i := range wrong {
		if wrong[i].Files[0].Path == earth1 {
			w = &wrong[i]
		}
	}
	if w == nil || w.Files[0].Episode != "S02E01" || len(w.Fix.Moves) == 0 || w.Fix.Moves[0].To != filepath.Join(l.tv, "Cunk on... (2018)", "Season 02", "Cunk on... (2018) - S02E01 - In the Beginnings.mp4") {
		t.Fatalf("Cunk on Earth's first: %+v", w)
	}

	// Fixing it records it as season 2's, renamed, and putting it back
	// turns that back.
	fix, err := Apply(ctx, l.st, noTools, *w, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	moved := w.Fix.Moves[0].To
	if e, _ := l.st.Episode(ctx, id["S02E01"]); e.File == nil || e.File.Path != moved {
		t.Fatalf("S02E01 %+v", e.File)
	}
	if e, _ := l.st.Episode(ctx, id["S01E01"]); e.File != nil {
		t.Fatalf("S01E01 still has %+v", e.File)
	}
	if err := PutBack(ctx, l.st, fix.ID); err != nil {
		t.Fatal(err)
	}
	if e, _ := l.st.Episode(ctx, id["S01E01"]); e.File == nil || e.File.Path != earth1 {
		t.Fatalf("S01E01 after putting back %+v", e.File)
	}
	if e, _ := l.st.Episode(ctx, id["S02E01"]); e.File != nil {
		t.Fatalf("S02E01 after putting back %+v", e.File)
	}

	// Cunk on Britain's first episode is season 1's, moved in.
	var b *Problem
	for i := range untracked {
		if untracked[i].Files[0].Path == britain1 {
			b = &untracked[i]
		}
	}
	if b == nil || b.Files[0].Episode != "S01E01" || b.Fix.Moves[0].To != filepath.Join(l.tv, "Cunk on... (2018)", "Season 01", "Cunk on... (2018) - S01E01 - Beginnings.mp4") {
		t.Fatalf("Cunk on Britain's first: %+v", b)
	}
}
