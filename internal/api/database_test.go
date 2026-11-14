package api

import (
	"path/filepath"
	"strings"
	"testing"
)

// Settings → Database says where the database is, checks a move without
// making one, and keeps a move asked for until the next start or until
// it's cancelled. A server that can't restart itself says so.
func TestDatabaseMove(t *testing.T) {
	s, hs := portalServer(t)
	s.DataDir = t.TempDir()
	s.DatabaseInfo = DatabaseInfo{DSN: ":memory:", Configured: ":memory:"}

	var v databaseView
	if code := call(t, hs, "GET", "/api/v1/admin/database", "", &v); code != 200 || v.Kind == "" || v.CanRestart || v.SuggestedFile != filepath.Join(s.DataDir, "boxoffice.db") {
		t.Fatalf("database = %d %+v", code, v)
	}
	var e struct{ Error string }
	if code := call(t, hs, "POST", "/api/v1/admin/database/check", `{"to":"relative.db"}`, &e); code != 400 || !strings.Contains(e.Error, "full path") {
		t.Fatalf("relative file: %d %q", code, e.Error)
	}
	to := filepath.Join(t.TempDir(), "new.db")
	var ok struct{ Where string }
	if code := call(t, hs, "POST", "/api/v1/admin/database/check", `{"to":"`+to+`"}`, &ok); code != 200 || ok.Where != to {
		t.Fatalf("check: %d %+v", code, ok)
	}
	var moved moveAnswer
	if code := call(t, hs, "POST", "/api/v1/admin/database/move", `{"to":"`+to+`"}`, &moved); code != 202 || moved.Restarting || moved.Pending != to {
		t.Fatalf("move: %d %+v", code, moved)
	}
	if code := call(t, hs, "DELETE", "/api/v1/admin/database/move", "", &v); code != 200 || v.Pending != "" {
		t.Fatalf("cancel: %d %+v", code, v)
	}
}
