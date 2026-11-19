package api

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/yeixio/boxoffice/internal/mediatypes"
	"github.com/yeixio/boxoffice/internal/store"
)

// A root folder can't overlap another library, be the whole disk, or hold
// Box Office's own folders.
func TestRootFoldersCannotOverlap(t *testing.T) {
	s, hs, _ := monitorServer(t)
	base := t.TempDir()
	movies := filepath.Join(base, "movies")
	inside := filepath.Join(movies, "Sintel (2010)")
	tv := filepath.Join(base, "tv")
	books := filepath.Join(base, "books")
	for _, d := range []string{inside, tv, books} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	add := func(kind, path string) (int, string) {
		var out struct {
			Error string `json:"error"`
		}
		code := call(t, hs, "POST", "/api/v1/rootfolders", `{"kind":"`+kind+`","path":"`+path+`"}`, &out)
		return code, out.Error
	}
	if code, msg := add("movie", movies); code != 201 {
		t.Fatalf("adding the movies folder: %d %s", code, msg)
	}
	for _, tc := range []struct{ name, kind, path, want string }{
		{"inside a root", "movie", inside, "is inside"},
		{"another kind's root", "series", movies, "already the root folder for movies"},
		{"a root's parent", "series", base, "holds"},
		{"the whole disk", "movie", "/", "whole disk"},
		{"the data folder", "movie", s.DataDir, "keeps its data"},
	} {
		if tc.path == "" {
			continue
		}
		if code, msg := add(tc.kind, tc.path); code != 400 || !strings.Contains(msg, tc.want) {
			t.Errorf("%s: got %d %q, want 400 with %q", tc.name, code, msg, tc.want)
		}
	}
	if code, _ := add("movie", movies); code != 409 {
		t.Errorf("the same folder again: got %d, want 409", code)
	}
	if code, msg := add("series", tv); code != 201 {
		t.Errorf("a folder beside it: %d %s", code, msg)
	}
	// Ebooks and audiobooks may share one, as Readarr's did.
	ctx := context.Background()
	for _, k := range []string{store.Ebook, store.Audiobook} {
		if err := s.Store.SetSetting(ctx, mediatypes.SettingKey(k), "1"); err != nil {
			t.Fatal(err)
		}
		if code, msg := add(k, books); code != 201 {
			t.Errorf("%s in the shared books folder: %d %s", k, code, msg)
		}
	}
}

// A root folder with titles filed under it stays until they've moved.
func TestRootFolderInUseStays(t *testing.T) {
	s, hs, _ := monitorServer(t)
	ctx := context.Background()
	root := t.TempDir()
	f, err := s.Store.AddRootFolder(ctx, "movie", root)
	if err != nil {
		t.Fatal(err)
	}
	ps, _ := s.Store.Profiles(ctx)
	m, err := s.Store.AddMovie(ctx, store.Movie{TMDBID: 1, Title: "Keep", Year: 2000, ProfileID: ps[0].ID, RootFolder: root, Path: filepath.Join(root, "Keep (2000)")})
	if err != nil {
		t.Fatal(err)
	}
	var list []struct {
		ID     int `json:"id"`
		Titles int `json:"titles"`
	}
	if code := call(t, hs, "GET", "/api/v1/rootfolders?kind=movie", "", &list); code != 200 || len(list) != 1 || list[0].Titles != 1 {
		t.Fatalf("listing: %d %+v, want one folder with 1 title", code, list)
	}
	var out struct {
		Error string `json:"error"`
	}
	path := "/api/v1/rootfolders/" + strconv.Itoa(f.ID)
	if code := call(t, hs, "DELETE", path, "", &out); code != 409 || !strings.Contains(out.Error, "1 title is still filed") {
		t.Fatalf("removing it in use: %d %q, want 409", code, out.Error)
	}
	if err := s.Store.DeleteMovie(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if code := call(t, hs, "DELETE", path, "", nil); code != 204 {
		t.Fatalf("removing it once empty: %d, want 204", code)
	}
}
