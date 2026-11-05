package notify

import (
	"testing"

	"github.com/yeixio/boxoffice/internal/store"
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
