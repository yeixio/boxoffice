package problems

import (
	"context"
	"log/slog"
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
	second := titled(byKind(rep, NotListed), "Fárgo")
	if second == nil || second.Detail != "second_folder" || second.Fix.Action != ActSetAside {
		t.Fatalf("second Fargo folder: %+v", second)
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
