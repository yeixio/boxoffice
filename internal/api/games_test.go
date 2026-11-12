package api

import (
	"context"
	"fmt"
	"testing"

	"github.com/yeixio/boxoffice/internal/games"
	"github.com/yeixio/boxoffice/internal/mediatypes"
	"github.com/yeixio/boxoffice/internal/store"
)

// The Games pages answer only once Games is on, and a system's games come
// grouped (clones with their parent, a console game's versions together),
// filtered and a page at a time.
func TestGamePages(t *testing.T) {
	s, hs, _ := monitorServer(t)
	ctx := context.Background()
	s.Games = &games.Service{Store: s.Store, Dir: t.TempDir(), Art: []string{"http://127.0.0.1:1/{name}"}}
	if code := call(t, hs, "GET", "/api/v1/gamesystems", "", nil); code != 409 {
		t.Fatalf("games off: %d", code)
	}
	mediatypes.SetEnabled(ctx, s.Store, mediatypes.Game, true)
	mame, err := s.Store.AddGameSystem(ctx, store.GameSystem{System: "mame", Path: "/games/mame", RootFolder: "/games"})
	if err != nil {
		t.Fatal(err)
	}
	snes, _ := s.Store.AddGameSystem(ctx, store.GameSystem{System: "snes", Path: "/games/snes", RootFolder: "/games"})
	var arcade []store.Game
	for i := range 70 {
		arcade = append(arcade, store.Game{Name: fmt.Sprintf("game%02d", i), Title: fmt.Sprintf("Game %02d", i), Year: "1990", Genre: "Shooter", Status: "complete", Path: "/games/mame/x.zip"})
	}
	arcade = append(arcade,
		store.Game{Name: "puckman", Title: "Puck Man", Parent: "puckman", ParentTitle: "Puck Man", Year: "1980", Maker: "Namco", Genre: "Maze", Status: "complete", Path: "/games/mame/puckman.zip"},
		store.Game{Name: "pacman", Title: "Pac-Man (Midway)", Parent: "puckman", ParentTitle: "Puck Man", Year: "1980", Maker: "Namco (Midway license)", Genre: "Maze", Status: "missing",
			Problems: []store.GameProblem{{Kind: "missing", Name: "pacman.6e"}}, Path: "/games/mame/pacman.zip"},
	)
	if err := s.Store.SaveGameCheck(ctx, mame.ID, store.GameCheck{List: "MAME 0.289", Format: "split", Games: arcade}); err != nil {
		t.Fatal(err)
	}
	s.Store.SaveGameCheck(ctx, snes.ID, store.GameCheck{List: "No-Intro", Games: []store.Game{
		{Name: "Super Metroid (Europe)", Title: "Super Metroid", Parent: "supermetroid", Status: "complete", Path: "/games/snes/e.sfc"},
		{Name: "Super Metroid (Japan, USA)", Title: "Super Metroid", Parent: "supermetroid", Status: "complete", Path: "/games/snes/u.sfc"},
	}})

	var systems []gameSystemView
	if code := call(t, hs, "GET", "/api/v1/gamesystems", "", &systems); code != 200 || len(systems) != 2 || systems[0].System != "mame" ||
		systems[0].Games != 72 || systems[0].Groups != 71 || systems[0].Problems != 1 || systems[0].Name != "Arcade (MAME)" {
		t.Fatalf("%d %+v", code, systems)
	}
	var page gamePage
	call(t, hs, "GET", fmt.Sprintf("/api/v1/games?system=%d", mame.ID), "", &page)
	if page.Total != 71 || len(page.Groups) != 60 || page.All != 71 || len(page.Genres) != 2 || page.Decades[0] != "1980s" {
		t.Fatalf("page 1: %d of %d, %v %v", len(page.Groups), page.Total, page.Genres, page.Decades)
	}
	call(t, hs, "GET", fmt.Sprintf("/api/v1/games?system=%d&problems=1", mame.ID), "", &page)
	if page.Total != 1 || page.Groups[0].Game.Name != "puckman" || page.Groups[0].Versions != 2 || page.Groups[0].Problems != 1 {
		t.Fatalf("problems: %+v", page.Groups)
	}
	call(t, hs, "GET", fmt.Sprintf("/api/v1/games?system=%d&q=midway&sort=year&page=1&size=10", mame.ID), "", &page)
	if page.Total != 1 || page.Groups[0].Game.Title != "Puck Man" {
		t.Fatalf("search: %+v", page.Groups)
	}
	call(t, hs, "GET", fmt.Sprintf("/api/v1/games?system=%d&genre=Shooter&page=2&size=50", mame.ID), "", &page)
	if page.Total != 70 || len(page.Groups) != 20 {
		t.Fatalf("genre, page 2: %d of %d", len(page.Groups), page.Total)
	}
	// A console game is shown by its USA version.
	call(t, hs, "GET", fmt.Sprintf("/api/v1/games?system=%d", snes.ID), "", &page)
	if page.Total != 1 || page.Groups[0].Game.Name != "Super Metroid (Japan, USA)" || page.Groups[0].Versions != 2 {
		t.Fatalf("snes: %+v", page.Groups)
	}

	var g gameView
	pac := page.Groups[0].Game
	call(t, hs, "GET", fmt.Sprintf("/api/v1/games/%d", pac.ID), "", &g)
	if g.System.Name != "Super Nintendo" || len(g.Versions) != 1 || g.Versions[0].Name != "Super Metroid (Europe)" {
		t.Fatalf("game %+v", g)
	}
	// A game with no name to look its picture up by has none.
	if code := call(t, hs, "GET", fmt.Sprintf("/api/v1/games/%d/art", pac.ID), "", nil); code != 404 {
		t.Errorf("art: %d", code)
	}

	var sys gameSystemView
	if code := call(t, hs, "PATCH", fmt.Sprintf("/api/v1/gamesystems/%d", snes.ID), `{"locked":true}`, &sys); code != 200 || !sys.Locked {
		t.Errorf("lock: %d %+v", code, sys)
	}
	if code := call(t, hs, "PUT", "/api/v1/games/settings", `{"mameVersion":"0.12"}`, nil); code != 400 {
		t.Errorf("bad version: %d", code)
	}
}
