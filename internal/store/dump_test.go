package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/yeixio/boxoffice/internal/quality"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
)

// A dump from SQLite restores into whichever database the tests use, so a
// backup also moves a server between SQLite and PostgreSQL.
func TestDumpRestore(t *testing.T) {
	ctx := context.Background()
	src, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	p, err := src.CreateProfile(ctx, quality.Profile{Name: "Mine", Allowed: []int{10, 11}, Cutoff: 11})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.AddRootFolder(ctx, "movie", "/media/movies"); err != nil {
		t.Fatal(err)
	}
	if _, err := src.AddMovie(ctx, store.Movie{TMDBID: 603, Title: "The Matrix", Year: 1999, ProfileID: p.ID, RootFolder: "/media/movies", Path: "/media/movies/The Matrix (1999)", Monitored: true}); err != nil {
		t.Fatal(err)
	}
	if err := src.UpsertIndexer(ctx, store.Indexer{Name: "NZBgeek", Protocol: "usenet", Kind: "newznab", URL: "https://api.nzbgeek.info", APIKey: "k", Categories: []int{2000}, Settings: json.RawMessage(`{"a":1}`)}); err != nil {
		t.Fatal(err)
	}
	src.SetSetting(ctx, "tmdb_key", "abc")
	src.SetSetting(ctx, "backups", `{"keep":3}`)
	var buf bytes.Buffer
	if err := src.Dump(ctx, &buf, "backups"); err != nil {
		t.Fatal(err)
	}

	dst := storetest.Open(t)
	dst.SetSetting(ctx, "backups", `{"keep":9}`)
	dst.SetSetting(ctx, "tmdb_key", "old")
	if err := dst.Restore(ctx, bytes.NewReader(buf.Bytes()), "backups"); err != nil {
		t.Fatal(err)
	}
	movies, _ := dst.Movies(ctx)
	if len(movies) != 1 || movies[0].Title != "The Matrix" || movies[0].ProfileID != p.ID {
		t.Fatalf("movies: %+v", movies)
	}
	idx, _ := dst.Indexers(ctx)
	if len(idx) != 1 || idx[0].APIKey != "k" || len(idx[0].Categories) != 1 {
		t.Fatalf("indexers: %+v", idx)
	}
	if v, _ := dst.Setting(ctx, "tmdb_key"); v != "abc" {
		t.Fatalf("tmdb_key %q", v)
	}
	if v, _ := dst.Setting(ctx, "backups"); v != `{"keep":9}` {
		t.Fatalf("the backup settings weren't kept: %q", v)
	}
	// New rows after a restore don't clash with restored ids.
	if _, err := dst.CreateProfile(ctx, quality.Profile{Name: "After", Allowed: []int{10}, Cutoff: 10}); err != nil {
		t.Fatal(err)
	}

	if err := dst.Restore(ctx, bytes.NewReader([]byte(`{"format":"something"}`))); err != store.ErrNotBackup {
		t.Fatalf("a non-backup: %v", err)
	}
	if movies, _ := dst.Movies(ctx); len(movies) != 1 {
		t.Fatal("a failed restore changed things")
	}
}
