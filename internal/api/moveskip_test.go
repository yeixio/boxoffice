package api

import (
	"slices"
	"testing"
)

func TestSkipMoveIn(t *testing.T) {
	_, hs, _ := monitorServer(t)
	var list []string
	if code := call(t, hs, "GET", "/api/v1/migrations/skipped", "", &list); code != 200 || len(list) != 0 {
		t.Fatalf("at first: %d %v", code, list)
	}
	call(t, hs, "PUT", "/api/v1/migrations/skipped/lidarr", "", nil)
	call(t, hs, "PUT", "/api/v1/migrations/skipped/nzbget", "", nil)
	call(t, hs, "PUT", "/api/v1/migrations/skipped/lidarr", "", nil)
	if call(t, hs, "GET", "/api/v1/migrations/skipped", "", &list); !slices.Equal(list, []string{"nzbget", "lidarr"}) {
		t.Fatalf("skipped: %v", list)
	}
	call(t, hs, "DELETE", "/api/v1/migrations/skipped/lidarr", "", nil)
	if call(t, hs, "GET", "/api/v1/migrations/skipped", "", &list); !slices.Equal(list, []string{"nzbget"}) {
		t.Fatalf("after undo: %v", list)
	}
	if code := call(t, hs, "PUT", "/api/v1/migrations/skipped/plex", "", nil); code != 404 {
		t.Fatalf("an unknown app: %d", code)
	}
}
