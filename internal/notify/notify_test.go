package notify

import (
	"context"
	"testing"

	"github.com/yeixio/boxoffice/internal/alerts"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
)

func TestMediaName(t *testing.T) {
	for _, c := range []struct {
		m    store.Media
		want string
	}{
		{store.Media{Kind: "movie", Title: "Dune", Year: 2021}, "Dune (2021)"},
		{store.Media{Kind: "movie", Title: "Dune"}, "Dune"},
		{store.Media{Kind: "series", Title: "Severance", Season: 2, Episodes: []int{3}}, "Severance S02E03"},
		{store.Media{Kind: "series", Title: "Severance", Season: 2, Episodes: []int{1, 2, 4}}, "Severance S02E01–E04"},
		{store.Media{Kind: "series", Title: "Severance", Season: 2}, "Severance season 2"},
	} {
		if got := mediaName(c.m); got != c.want {
			t.Errorf("%+v: %q, want %q", c.m, got, c.want)
		}
	}
}

func TestAlert(t *testing.T) {
	st := storetest.Open(t)
	r := &Recorder{Store: st}
	ctx := context.Background()
	r.Alert(ctx, alerts.Alert{Rule: "disk", Level: "critical", Title: "Disk nearly full", Message: "2 GB left on /data."})
	r.Alert(ctx, alerts.Alert{Rule: "disk", Level: "critical", Title: "Disk has room again", Resolved: true})
	list, err := st.Notifications(ctx, 0, 10)
	if err != nil || len(list) != 2 {
		t.Fatalf("%+v %v", list, err)
	}
	if n := list[0]; n.Event != "alert.disk" || n.Level != "critical" || n.Audience != store.AudienceAdmins || n.Body != "2 GB left on /data." {
		t.Fatalf("alert: %+v", n)
	}
	if list[1].Level != store.LevelInfo {
		t.Fatalf("resolved: %+v", list[1])
	}
}
