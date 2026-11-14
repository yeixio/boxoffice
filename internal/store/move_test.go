package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeixio/boxoffice/internal/quality"
)

// moveDSN is a new, empty database of the kind: a SQLite file, or a fresh
// PostgreSQL database when BOXOFFICE_TEST_DATABASE_URL is set (the test is
// skipped otherwise).
func moveDSN(t *testing.T, kind string) string {
	t.Helper()
	if kind == "sqlite" {
		return filepath.Join(t.TempDir(), "boxoffice.db")
	}
	admin := os.Getenv("BOXOFFICE_TEST_DATABASE_URL")
	if admin == "" {
		t.Skip("BOXOFFICE_TEST_DATABASE_URL isn't set")
	}
	b := make([]byte, 6)
	rand.Read(b)
	name := "boxoffice_move_" + hex.EncodeToString(b)
	conn, err := sql.Open("pgx", admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatalf("create test database: %v", err)
	}
	u, _ := url.Parse(admin)
	u.Path = "/" + name
	t.Cleanup(func() {
		conn.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)")
		conn.Close()
	})
	return u.String()
}

// fill puts a little of everything a move has to get right in s: rows that
// refer to others (a show's seasons and episodes, titles' profiles), a
// session, a number SQLite kept as text, and settings that stay behind.
func fill(t *testing.T, s *Store) (userID, seriesID int64) {
	t.Helper()
	ctx := context.Background()
	u, err := s.CreateUser(ctx, "mike", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.exec(ctx, "INSERT INTO sessions (token_hash, user_id, expires_at) VALUES (?, ?, ?)", "tok", u.ID, now()+3600); err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProfile(ctx, quality.Profile{Name: "Mine", Allowed: []int{10, 11}, Cutoff: 11})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddMovie(ctx, Movie{TMDBID: 603, Title: "The Matrix", Year: 1999, ProfileID: p.ID, RootFolder: "/media/movies", Path: "/media/movies/The Matrix (1999)", Monitored: true, Rating: 8.2}); err != nil {
		t.Fatal(err)
	}
	show, err := s.AddSeries(ctx, Series{TMDBID: 1399, Title: "Moving Day", ProfileID: int(p.ID), RootFolder: "/media/tv", Path: "/media/tv/Moving Day", Monitored: true,
		Seasons: []Season{{Number: 1, Monitored: true}, {Number: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 3; n++ {
		if err := s.UpsertEpisode(ctx, Episode{SeriesID: show.ID, Season: 1, Number: n, Title: "Episode", Monitored: true}); err != nil {
			t.Fatal(err)
		}
	}
	if !s.postgres {
		// SQLite keeps what it's given: a year written as text.
		if _, err := s.exec(ctx, "UPDATE movies SET year = '1999' WHERE tmdb_id = 603"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetSetting(ctx, "tmdb_key", "abc"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, SettingMoveError, "an older try failed"); err != nil {
		t.Fatal(err)
	}
	return u.ID, show.ID
}

func TestMove(t *testing.T) {
	moveBatch = 1
	t.Cleanup(func() { moveBatch = 500 })
	for _, c := range []struct{ from, to string }{
		{"sqlite", "sqlite"},
		{"sqlite", "postgres"},
		{"postgres", "postgres"},
		{"postgres", "sqlite"},
	} {
		t.Run(c.from+" to "+c.to, func(t *testing.T) {
			ctx := context.Background()
			fromDSN, toDSN := moveDSN(t, c.from), moveDSN(t, c.to)
			src, err := Open(fromDSN)
			if err != nil {
				t.Fatal(err)
			}
			defer src.Close()
			userID, seriesID := fill(t, src)
			var profiles int
			src.row(ctx, "SELECT COUNT(*) FROM quality_profiles").Scan(&profiles)
			if err := src.CheckMoveTarget(ctx, toDSN); err != nil {
				t.Fatalf("check: %v", err)
			}
			if c.to == "sqlite" {
				if _, err := os.Stat(toDSN); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("checking left a file behind: %v", err)
				}
			}
			var calls int
			report, err := src.MoveTo(ctx, toDSN, func(string, int64, int64) { calls++ })
			if err != nil {
				t.Fatalf("move: %v", err)
			}
			if report.Rows == 0 || calls == 0 {
				t.Errorf("report = %+v after %d progress calls", report, calls)
			}
			if err := src.FinishMove(ctx, fromDSN, toDSN, report); err != nil {
				t.Fatal(err)
			}
			src.Close()

			if got, err := Follow(ctx, fromDSN); err != nil || got != toDSN {
				t.Fatalf("follow = %q, %v", got, err)
			}
			dst, err := Open(toDSN)
			if err != nil {
				t.Fatal(err)
			}
			defer dst.Close()
			if u, err := dst.UserByName(ctx, "mike"); err != nil || u.ID != userID {
				t.Errorf("user = %+v, %v", u, err)
			}
			var n int
			if err := dst.row(ctx, "SELECT COUNT(*) FROM sessions WHERE token_hash = 'tok'").Scan(&n); err != nil || n != 1 {
				t.Errorf("sessions = %d, %v", n, err)
			}
			// No second set of default profiles.
			dst.row(ctx, "SELECT COUNT(*) FROM quality_profiles").Scan(&n)
			if n != profiles {
				t.Errorf("%d profiles, not %d", n, profiles)
			}
			movies, err := dst.Movies(ctx)
			if err != nil || len(movies) != 1 || movies[0].Year != 1999 || movies[0].Rating != 8.2 {
				t.Errorf("movies = %+v, %v", movies, err)
			}
			eps, err := dst.Episodes(ctx, seriesID)
			if err != nil || len(eps) != 3 {
				t.Errorf("episodes = %+v, %v", eps, err)
			}
			show, err := dst.Series(ctx, seriesID)
			if err != nil || len(show.Seasons) != 2 {
				t.Errorf("show = %+v, %v", show, err)
			}
			if v, _ := dst.Setting(ctx, "tmdb_key"); v != "abc" {
				t.Errorf("tmdb key = %q", v)
			}
			for _, k := range []string{SettingMoveError, SettingMovedTo} {
				if v, _ := dst.Setting(ctx, k); v != "" {
					t.Errorf("%s came along: %q", k, v)
				}
			}
			if v, _ := dst.Setting(ctx, SettingMovedFrom); !strings.Contains(v, `"rows"`) {
				t.Errorf("moved from = %q", v)
			}
			// New rows carry on numbering after the copied ones.
			u2, err := dst.CreateUser(ctx, "ellen", "hash")
			if err != nil {
				t.Fatalf("new user: %v", err)
			}
			if u2.ID <= userID {
				t.Errorf("new user got id %d, not after %d", u2.ID, userID)
			}

			// The old database still has everything, and one more move to
			// the same place finds it isn't empty.
			old, err := Open(fromDSN)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			if _, err := old.UserByName(ctx, "mike"); err != nil {
				t.Errorf("old database lost its user: %v", err)
			}
			if err := old.CheckMoveTarget(ctx, toDSN); !errors.Is(err, ErrNotEmpty) {
				t.Errorf("moving onto the copy: %v", err)
			}
			if err := UndoMove(ctx, fromDSN); err != nil {
				t.Fatal(err)
			}
			if got, _ := Follow(ctx, fromDSN); got != fromDSN {
				t.Errorf("after undo, follow = %q", got)
			}
		})
	}
}

// A move to the database in use, or to a folder, is refused without
// changing anything.
func TestMoveRefuses(t *testing.T) {
	ctx := context.Background()
	path := moveDSN(t, "sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	fill(t, s)
	if err := s.CheckMoveTarget(ctx, path); err == nil || !strings.Contains(err.Error(), "using now") {
		t.Errorf("same database: %v", err)
	}
	if err := s.CheckMoveTarget(ctx, t.TempDir()); err == nil || !strings.Contains(err.Error(), "folder") {
		t.Errorf("folder: %v", err)
	}
	if err := s.CheckMoveTarget(ctx, "boxoffice.db"); err == nil {
		t.Error("a relative path was taken")
	}
	if err := s.CheckMoveTarget(ctx, "postgres://boxoffice:x@127.0.0.1:1/boxoffice?connect_timeout=1"); err == nil || !strings.Contains(err.Error(), "reach") {
		t.Errorf("unreachable: %v", err)
	}
}

// A failed copy leaves no file behind.
func TestMoveFailureLeavesNothing(t *testing.T) {
	ctx := context.Background()
	s, err := Open(moveDSN(t, "sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	fill(t, s)
	to := moveDSN(t, "sqlite")
	cctx, cancel := context.WithCancel(ctx)
	_, err = s.MoveTo(cctx, to, func(string, int64, int64) { cancel() })
	if err == nil {
		t.Fatal("a cancelled move worked")
	}
	if _, err := os.Stat(to); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the failed move left %s: %v", to, err)
	}
}

func TestDescribe(t *testing.T) {
	if got := Describe("postgres://boxoffice:secret@tower:5432/boxoffice?sslmode=disable"); strings.Contains(got, "secret") || !strings.Contains(got, "tower:5432/boxoffice") {
		t.Errorf("describe = %q", got)
	}
	if got := Describe("/data/boxoffice.db"); got != "/data/boxoffice.db" {
		t.Errorf("describe = %q", got)
	}
}
