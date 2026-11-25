package problems

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/yeixio/boxoffice/internal/store"
)

// A movie's second copy isn't a duplicate next to the first, and its
// folder in another library folder isn't an unknown one; one that's gone
// is missing.
func TestSecondCopyIsNoProblem(t *testing.T) {
	l := newLibrary(t)
	ctx := context.Background()
	rep, err := Find(ctx, l.st, nil)
	if err != nil {
		t.Fatal(err)
	}
	before := len(rep.Problems)

	other := filepath.Join(filepath.Dir(l.movies), "movies-hd")
	os.MkdirAll(other, 0o755)
	if _, err := l.st.AddRootFolder(ctx, "movie", other); err != nil {
		t.Fatal(err)
	}
	// Next to the first copy.
	fargo := filepath.Join(l.movies, "Fargo (1996)", "Fargo (1996) - Blu-ray 720p.mkv")
	touch(t, fargo, 5)
	l.st.AddFile(ctx, store.File{Kind: store.SecondKind, OwnerID: l.ids["Fargo"], Path: fargo, Size: 5})
	// In another library folder.
	se7en := filepath.Join(other, "Se7en (1995) {tmdb-807}", "Se7en (1995).mkv")
	touch(t, se7en, 5)
	l.st.AddFile(ctx, store.File{Kind: store.SecondKind, OwnerID: l.ids["Se7en"], Path: se7en, Size: 5})

	rep, err = Find(ctx, l.st, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Problems) != before {
		for _, p := range rep.Problems {
			t.Logf("%s %v %s", p.Kind, p.Title, p.Files[0].Path)
		}
		t.Fatalf("%d problems, %d before", len(rep.Problems), before)
	}
	// A show's second copies: next to the first copy of an episode, and
	// under a folder named like the show's in another library folder.
	tvOther := filepath.Join(filepath.Dir(l.tv), "tv-hd")
	os.MkdirAll(tvOther, 0o755)
	if _, err := l.st.AddRootFolder(ctx, "series", tvOther); err != nil {
		t.Fatal(err)
	}
	pilot := filepath.Join(l.tv, "Lost (2004)", "Season 01", "Lost (2004) - S01E01 - Pilot - WEB 720p.mkv")
	touch(t, pilot, 5)
	l.st.AddFile(ctx, store.File{Kind: store.SecondEpisodeKind, OwnerID: l.episodeIDs["S01E01"], Path: pilot, Size: 5})
	tabula := filepath.Join(tvOther, "Lost (2004)", "Season 01", "Lost (2004) - S01E03 - Tabula Rasa.mkv")
	touch(t, tabula, 5)
	l.st.AddFile(ctx, store.File{Kind: store.SecondEpisodeKind, OwnerID: l.episodeIDs["S01E03"], Path: tabula, Size: 5})

	// Gone from the other folder: missing, and forgotten when asked.
	os.Remove(se7en)
	p := find(t, l, Missing, "Se7en")
	if len(p.Files) != 1 || p.Files[0].Path != se7en || p.Fix == nil || p.Fix.Action != ActForget {
		t.Fatalf("missing %+v", p)
	}
	os.Remove(tabula)
	rep, err = Find(ctx, l.st, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range byKind(rep, Missing) {
		if len(p.Files) == 1 && p.Files[0].Path == tabula {
			found = true
		}
	}
	if !found {
		t.Fatalf("the episode's second copy that's gone isn't missing: %v", names(byKind(rep, Missing)))
	}
}
