// Package storetest opens a throwaway Box Office database for tests:
// in-memory SQLite by default, or a fresh PostgreSQL database when
// BOXOFFICE_TEST_DATABASE_URL is set.
package storetest

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	"github.com/yeixio/boxoffice/internal/store"
)

// Open opens a fresh database that's dropped when the test ends.
func Open(t testing.TB) *store.Store {
	t.Helper()
	dsn := ":memory:"
	if admin := os.Getenv("BOXOFFICE_TEST_DATABASE_URL"); admin != "" {
		dsn = freshPostgres(t, admin)
	}
	st, err := store.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func freshPostgres(t testing.TB, admin string) string {
	t.Helper()
	b := make([]byte, 6)
	rand.Read(b)
	name := "boxoffice_test_" + hex.EncodeToString(b)
	conn, err := sql.Open("pgx", admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatalf("create test database: %v", err)
	}
	u, err := url.Parse(admin)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	t.Cleanup(func() {
		conn.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)")
		conn.Close()
	})
	return u.String()
}
