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
	// Gone from the other folder: missing, and forgotten when asked.
	os.Remove(se7en)
	p := find(t, l, Missing, "Se7en")
	if len(p.Files) != 1 || p.Files[0].Path != se7en || p.Fix == nil || p.Fix.Action != ActForget {
		t.Fatalf("missing %+v", p)
	}
}
