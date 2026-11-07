package problems

import (
	"context"
	"path/filepath"
	"testing"
)

func TestEditionsByLength(t *testing.T) {
	l := newLibrary(t)
	ctx := context.Background()
	p := find(t, l, Duplicate, "Heat")
	main := filepath.Join(l.movies, "Heat (1995)", "Heat (1995).mkv")
	other := filepath.Join(l.movies, "Heat (1995)", "Heat.1995.720p.mkv")
	for i := range p.Files {
		d := 170 * 60.0
		if p.Files[i].Path == other {
			d = 200 * 60
		}
		p.Files[i].Media = &Media{Duration: d}
	}
	asEditions(&p)
	if p.Kind != Editions || p.Fix == nil || p.Fix.Action != ActKeepEditions || p.Fix.Keep != main {
		t.Fatalf("got %s %+v", p.Kind, p.Fix)
	}
	tagged := filepath.Join(l.movies, "Heat (1995)", "Heat (1995) {edition-Extended}.mkv")
	if len(p.Fix.Moves) != 1 || p.Fix.Moves[0].To != tagged {
		t.Fatalf("moves %+v", p.Fix.Moves)
	}
	for _, f := range p.Files {
		if f.Path == other && (f.Edition != "Extended" || !f.ByLength) {
			t.Errorf("other copy labelled %q by length %v", f.Edition, f.ByLength)
		}
	}
	fix, err := Apply(ctx, l.st, noTools, p, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !exists(main) || !exists(tagged) || exists(other) {
		t.Fatal("files not where they should be")
	}
	m, _ := l.st.Movie(ctx, l.ids["Heat"])
	eds, _ := l.st.EditionFiles(ctx, m.ID)
	if m.File == nil || m.File.Path != main || len(eds) != 1 || eds[0].Path != tagged {
		t.Fatalf("copy %+v, editions %+v", m.File, eds)
	}
	if err := PutBack(ctx, l.st, fix.ID); err != nil {
		t.Fatal(err)
	}
	if !exists(other) || exists(tagged) {
		t.Fatal("not put back")
	}
	if eds, _ := l.st.EditionFiles(ctx, m.ID); len(eds) != 0 {
		t.Fatalf("editions after putting back %+v", eds)
	}
}

func TestSameCutStaysDuplicate(t *testing.T) {
	l := newLibrary(t)
	p := find(t, l, Duplicate, "Heat")
	for i := range p.Files {
		p.Files[i].Media = &Media{Duration: 170*60 + float64(i*30)}
	}
	asEditions(&p)
	if p.Kind != Duplicate || p.Fix.Action != ActKeepOne {
		t.Fatalf("same cut became %s", p.Kind)
	}
}

func TestEditionsByName(t *testing.T) {
	files := []File{
		{Path: "/m/Heat (1995)/Heat (1995).mkv", Recorded: true},
		{Path: "/m/Heat (1995)/Heat.1995.Directors.Cut.1080p.mkv"},
	}
	p := Problem{Kind: Duplicate, Title: &TitleRef{Kind: "movie", ID: 1, Name: "Heat", Year: 1995}, Files: files, Fix: &Fix{Action: ActKeepOne}}
	asEditions(&p)
	if p.Kind != Editions || len(p.Fix.Moves) != 1 || filepath.Base(p.Fix.Moves[0].To) != "Heat (1995) {edition-Director's Cut}.mkv" {
		t.Fatalf("got %s %+v", p.Kind, p.Fix)
	}
	// Episodes have no editions.
	ep := Problem{Kind: Duplicate, Title: &TitleRef{Kind: "series", ID: 1, Name: "X"}, Detail: "S01E01", Files: files, Fix: &Fix{Action: ActKeepOne}}
	asEditions(&ep)
	if ep.Kind != Duplicate {
		t.Fatal("an episode's copies became editions")
	}
}
