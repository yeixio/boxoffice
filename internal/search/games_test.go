package search

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/mediatypes"
	"github.com/yeixio/boxoffice/internal/store"
)

func TestGameSearch(t *testing.T) {
	srv, asked := indexer(t,
		bookItem("Super Metroid (USA) SNES", "snes", 3e6, 1090),
		bookItem("Super.Metroid.1994.USA", "plain", 2e6, 8010),
		bookItem("Super Metroid (USA) GBA", "gba", 3e6, 1090),
		bookItem("No-Intro Super Nintendo Romset 2026", "pack", 2e9, 1090),
		bookItem("Super Metroid OST FLAC", "ost", 3e8, 3040),
		bookItem("Metroid Prime Trilogy WII", "prime", 7e9, 1030),
		bookItem("Super Metroid Redesign (Hack) SNES", "big", 400e6, 1090),
	)
	s, st, _ := setup(t, srv.URL)
	ctx := context.Background()
	w, err := st.AddGameWant(ctx, store.GameWant{System: "snes", Name: "Super Metroid (Japan, USA) (En,Ja)", Title: "Super Metroid", Group: "supermetroid", Size: 3 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Game(ctx, w.ID); err != ErrGamesOff {
		t.Fatalf("searched with Games off: %v", err)
	}
	mediatypes.SetEnabled(ctx, st, mediatypes.Game, true)
	res, err := s.Game(ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	q := (*asked)[len(*asked)-1]
	if !strings.Contains(q, "q=super+metroid") || !strings.Contains(q, "cat=1000%2C8010") && !strings.Contains(q, "cat=1000,8010") {
		t.Fatalf("asked %s", q)
	}
	var accepted []string
	for _, c := range res.Releases {
		if c.Accepted {
			accepted = append(accepted, c.GUID)
		}
	}
	// One that names the system goes before one that doesn't.
	if !slices.Equal(accepted, []string{"snes", "plain"}) {
		t.Fatalf("accepted %v: %+v", accepted, res.Releases)
	}
	if res.Releases[0].Format != "SNES" {
		t.Errorf("format %q", res.Releases[0].Format)
	}
	why := map[string]string{}
	for _, c := range res.Releases {
		why[c.GUID] = strings.Join(c.Rejections, "; ")
	}
	for guid, want := range map[string]string{"gba": "Game Boy Advance", "pack": "set of many games", "ost": "isn't a SNES game", "prime": "doesn't match", "big": "too big"} {
		if !strings.Contains(why[guid], want) {
			t.Errorf("%s: %q, want %q", guid, why[guid], want)
		}
	}

	// It's wanted until it's on disk, and the job grabs the best.
	list, _ := s.WantedList(ctx)
	if !slices.ContainsFunc(list, func(x Wanted) bool {
		return x.Kind == store.GameKind && x.ID == w.ID && x.Subject == "Super Metroid (SNES)"
	}) {
		t.Errorf("wanted %+v", list)
	}
	downloader.SaveSettings(ctx, st, downloader.Settings{Enabled: true})
	r, err := s.jobGame(ctx, GameJob(w.ID))
	if err != nil || r.Status != "done" || !strings.Contains(r.Message, "grabbed Super Metroid (USA) SNES") {
		t.Fatalf("job %+v %v", r, err)
	}
	if p, _ := st.Pending(ctx, store.GameKind, w.ID); len(p) != 1 {
		t.Fatalf("pending %+v", p)
	}
	if r, _ := s.jobGame(ctx, GameJob(w.ID)); r.Status != "skipped" {
		t.Errorf("grabbed twice %+v", r)
	}
	// Moved in: not looked for again while its system is checked.
	st.GameWantGot(ctx, w.ID)
	list, _ = s.WantedList(ctx)
	if slices.ContainsFunc(list, func(x Wanted) bool { return x.Kind == store.GameKind }) {
		t.Errorf("wanted after it was moved in")
	}
}

func TestNamedSystems(t *testing.T) {
	for name, want := range map[string]string{
		"Tetris (World) Game Boy":       "gb",
		"Sonic.the.Hedgehog.Mega.Drive": "genesis",
		"Contra NES":                    "nes",
		"F-Zero SNES":                   "snes",
		"Super Metroid":                 "",
		"Bloodborne.PS4-DUPLEX":         "ps4",
		"Celeste.NSW-VENOM":             "switch",
		"Halo.3.X360-GROUP":             "xbox360",
		"Gran Turismo 4 (USA) PS2":      "ps2",
	} {
		got := strings.Join(namedSystems(name), ",")
		if got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
}

// A Switch game's release is its .nsp; a PC game's or another console's
// isn't it.
func TestModernGameSearch(t *testing.T) {
	srv, asked := indexer(t,
		bookItem("Celeste.NSW-VENOM", "nsw", 1e9, 1000),
		bookItem("Celeste [01002B30028F6000][v0].nsp", "nsp", 1e9, 1000),
		bookItem("Celeste-PLAZA", "pc", 1e9, 4050),
		bookItem("Celeste.PS4-DUPLEX", "ps4", 1e9, 1180),
		bookItem("Celeste OST FLAC", "ost", 3e8, 3040),
	)
	s, st, _ := setup(t, srv.URL)
	ctx := context.Background()
	mediatypes.SetEnabled(ctx, st, mediatypes.Game, true)
	w, _ := st.AddGameWant(ctx, store.GameWant{System: "switch", Name: "Celeste", Title: "Celeste", Group: "celeste", Serial: "01002B30028F6000", Size: 1 << 30})
	res, err := s.Game(ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	var accepted []string
	why := map[string]string{}
	for _, c := range res.Releases {
		if c.Accepted {
			accepted = append(accepted, c.GUID)
		}
		why[c.GUID] = strings.Join(c.Rejections, "; ")
	}
	slices.Sort(accepted)
	if !slices.Equal(accepted, []string{"nsp", "nsw"}) {
		t.Fatalf("accepted %v: %v", accepted, why)
	}
	for guid, want := range map[string]string{"pc": "PC game", "ps4": "PlayStation 4", "ost": "isn't a Switch game"} {
		if !strings.Contains(why[guid], want) {
			t.Errorf("%s: %q, want %q", guid, why[guid], want)
		}
	}

	// A PC game is looked for among PC games.
	w, _ = st.AddGameWant(ctx, store.GameWant{System: "pc", Name: "Celeste", Title: "Celeste", Group: "celeste"})
	res, err = s.Game(ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if q := (*asked)[len(*asked)-1]; !strings.Contains(q, "4050") {
		t.Errorf("asked %s", q)
	}
	for _, c := range res.Releases {
		if c.GUID == "pc" && !c.Accepted || c.GUID == "nsp" && c.Accepted {
			t.Errorf("%s: %v", c.GUID, c.Rejections)
		}
	}
}
