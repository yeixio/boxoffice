package store

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// A password pasted as typed, with characters a URL reserves, still reaches
// the database named in the address, not one named after the user.
func TestCleanDSN(t *testing.T) {
	for _, c := range []struct{ in, user, pass, db string }{
		{"postgresql://thebeast:pw@tower:5432/central_boxoffice?sslmode=disable", "thebeast", "pw", "central_boxoffice"},
		{"  postgresql://thebeast:p/w@tower:5432/central_boxoffice \n", "thebeast", "p/w", "central_boxoffice"},
		{"postgresql://thebeast:p@w@tower:5432/central_boxoffice", "thebeast", "p@w", "central_boxoffice"},
		{"postgresql://thebeast:p?w#x@tower:5432/central_boxoffice?sslmode=disable", "thebeast", "p?w#x", "central_boxoffice"},
		{"postgresql://thebeast:100%@tower:5432/central_boxoffice", "thebeast", "100%", "central_boxoffice"},
		{"postgresql://thebeast:a%40b@tower:5432/central_boxoffice", "thebeast", "a@b", "central_boxoffice"},
		{"postgres://thebeast:pw@tower:5432/central_boxoffice/", "thebeast", "pw", "central_boxoffice"},
		{"host=tower user=thebeast password=pw dbname=central_boxoffice", "thebeast", "pw", "central_boxoffice"},
	} {
		dsn := CleanDSN(c.in)
		if !IsPostgres(dsn) {
			t.Errorf("%q isn't PostgreSQL", c.in)
			continue
		}
		cfg, err := pgconn.ParseConfig(dsn)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if cfg.User != c.user || cfg.Password != c.pass || cfg.Database != c.db || cfg.Host != "tower" {
			t.Errorf("%q read as user %q password %q database %q host %q", c.in, cfg.User, cfg.Password, cfg.Database, cfg.Host)
		}
		if d := Describe(c.in); strings.Contains(d, c.pass) || !strings.Contains(d, c.db) {
			t.Errorf("describe %q = %q", c.in, d)
		}
	}
	if got := CleanDSN(" /data/boxoffice.db "); got != "/data/boxoffice.db" {
		t.Errorf("sqlite path = %q", got)
	}
}

// A database that isn't there is named in the answer, as it was asked for.
func TestMoveNamesMissingDatabase(t *testing.T) {
	admin := os.Getenv("BOXOFFICE_TEST_DATABASE_URL")
	if admin == "" {
		t.Skip("BOXOFFICE_TEST_DATABASE_URL isn't set")
	}
	s, err := Open(filepath.Join(t.TempDir(), "boxoffice.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	u, _ := url.Parse(admin)
	u.Path = "/Central_Missing"
	to := u.String()
	err = s.CheckMoveTarget(context.Background(), to)
	if err == nil || !strings.Contains(err.Error(), `"Central_Missing"`) || !strings.Contains(err.Error(), `CREATE DATABASE "Central_Missing"`) {
		t.Errorf("missing database: %v", err)
	}
}
