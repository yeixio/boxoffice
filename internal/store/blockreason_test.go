package store

import (
	"context"
	"strings"
	"testing"
)

func TestBlockReason(t *testing.T) {
	for in, want := range map[string]string{
		"PAR Status: NONE - Unpack Status: NONE - Move Status: NONE - Script Status: {scriptStatus} - Delete Status: HEALTH - Mark Status: NONE": "Missing articles: too much of it was gone from the news servers",
		"PAR Status: SUCCESS - Unpack Status: PASSWORD - Move Status: NONE - Script Status: NONE - Delete Status: NONE - Mark Status: NONE":      "Password protected",
		"PAR Status: FAILURE - Unpack Status: NONE - Move Status: NONE - Script Status: NONE - Delete Status: NONE - Mark Status: NONE":          "Failed repair: too much was damaged or missing to repair",
		"PAR Status: NONE - Unpack Status: NONE - Move Status: NONE - Script Status: {scriptStatus} - Delete Status: DUPE - Mark Status: NONE":   "Duplicate of a release already downloaded",
		"Manually marked as failed": "Manually marked as failed",
		"":                          "",
	} {
		if got := BlockReason(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// Rows brought over before are rewritten when the database opens.
func TestFixBlockReasons(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	raw := "PAR Status: NONE - Unpack Status: NONE - Move Status: NONE - Script Status: {scriptStatus} - Delete Status: HEALTH - Mark Status: NONE"
	if _, err := s.exec(ctx, "INSERT INTO blocklist (kind, title_id, source_title, quality, protocol, indexer, reason, at, source) VALUES ('movie', 1, 'A.Release', 0, 'usenet', '', ?, 0, 'radarr')", raw); err != nil {
		t.Fatal(err)
	}
	if err := s.fixBlockReasons(ctx); err != nil {
		t.Fatal(err)
	}
	list, err := s.Blocklist(ctx, 10)
	if err != nil || len(list) != 1 || !strings.HasPrefix(list[0].Reason, "Missing articles") {
		t.Fatalf("%v %+v", err, list)
	}
}
