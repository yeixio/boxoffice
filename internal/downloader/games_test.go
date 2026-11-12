package downloader

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/usenet/nntp/nntptest"
)

// fakeGames takes a download for a game when it holds right.sfc.
type fakeGames struct{ imported []string }

func (f *fakeGames) MatchDownload(_ context.Context, _ int64, _ string, files []string) (string, error) {
	if slices.Contains(files, "right.sfc") {
		return "", nil
	}
	return "none of the 1 SNES games in it is Super Metroid, going by No-Intro's list", nil
}

func (f *fakeGames) ImportDownload(_ context.Context, _ int64, dir string, files []string) (string, error) {
	f.imported = append(f.imported, files...)
	return "/games/snes/Super Metroid (USA).sfc", nil
}

// A game download is checked against its system's list: one that isn't
// the game is quarantined (and so blocked and looked for again), and one
// that is goes to the Games section.
func TestGameDownloads(t *testing.T) {
	srv := nntptest.New()
	defer srv.Close()
	e, st := newEngine(t, srv)
	games := &fakeGames{}
	e.Importer = &LibraryImporter{Store: st, Games: games}
	enable(t, st, Settings{Import: true})
	run(t, e)
	ctx := context.Background()
	w, err := st.AddGameWant(ctx, store.GameWant{System: "snes", Name: "Super Metroid (USA)", Title: "Super Metroid"})
	if err != nil {
		t.Fatal(err)
	}
	d, err := e.Add(ctx, store.Download{Source: "search", Kind: store.GameKind, TitleID: w.ID}, postFiles(srv, "Super.Metroid.Wrong", [][2]any{{"wrong.sfc", []byte("nope")}}))
	if err != nil {
		t.Fatal(err)
	}
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	if d.Status != store.DownloadFailed || d.Quarantined == "" || !strings.Contains(d.Message, "isn't the game it said it was") {
		t.Fatalf("%s: %s", d.Status, d.Message)
	}
	d, err = e.Add(ctx, store.Download{Source: "search", Kind: store.GameKind, TitleID: w.ID}, postFiles(srv, "Super.Metroid.SNES", [][2]any{{"right.sfc", []byte("SNES")}}))
	if err != nil {
		t.Fatal(err)
	}
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	if d.Status != store.DownloadCompleted || d.ImportedTo != "/games/snes/Super Metroid (USA).sfc" || !slices.Equal(games.imported, []string{"right.sfc"}) {
		t.Fatalf("%s: %s %v", d.Status, d.Message, games.imported)
	}
	// Its folder in the downloads folder is gone once it's in.
	if _, err := os.Stat(filepath.Join(e.Dir, d.Folder)); !os.IsNotExist(err) {
		t.Errorf("left %s: %v", d.Folder, err)
	}
}
