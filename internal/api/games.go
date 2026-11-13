package api

import (
	"cmp"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/yeixio/boxoffice/internal/gamedat"
	"github.com/yeixio/boxoffice/internal/games"
	"github.com/yeixio/boxoffice/internal/mediatypes"
	"github.com/yeixio/boxoffice/internal/search"
	"github.com/yeixio/boxoffice/internal/store"
)

// The Games section: each system's folder in the games root folders, the
// games found in them, and how complete each one is. See package games.
func (s *Server) gameRoutes() []route {
	return []route{
		{"GET /api/v1/gamesystems", true, s.gameSystems},
		{"GET /api/v1/gamesystems/{id}", true, s.gameSystem},
		{"PATCH /api/v1/gamesystems/{id}", true, s.editGameSystem},
		{"GET /api/v1/games/check", true, s.gameCheck},
		{"POST /api/v1/games/check", true, s.startGameCheck},
		{"GET /api/v1/games/settings", true, s.gameSettings},
		{"PUT /api/v1/games/settings", true, s.saveGameSettings},
		{"GET /api/v1/games", true, s.gameList},
		{"GET /api/v1/games/{id}", true, s.game},
		{"GET /api/v1/games/{id}/art", true, s.gameArt},
		{"GET /api/v1/games/systems", true, s.allGameSystems},
		{"GET /api/v1/games/lookup", true, s.lookupGames},
		{"GET /api/v1/games/lookup/art", true, s.lookupGameArt},
		{"GET /api/v1/gamewants", true, s.gameWants},
		{"POST /api/v1/gamewants", true, s.wantGame},
		{"DELETE /api/v1/gamewants/{id}", true, s.unwantGame},
		{"POST /api/v1/gamewants/{id}/search", true, s.searchGame},
		{"GET /api/v1/gamewants/{id}/releases", true, s.gameReleases},
		{"GET /api/v1/gamewants/{id}/art", true, s.gameWantArt},
	}
}

func (s *Server) gamesOn(r *http.Request) error {
	if s.Games == nil {
		return errorf(http.StatusConflict, games.ErrOff.Error())
	}
	on, err := mediatypes.Enabled(r.Context(), s.Store, mediatypes.Game)
	if err != nil {
		return err
	}
	if !on {
		return errorf(http.StatusConflict, games.ErrOff.Error())
	}
	return nil
}

func gamesErr(err error) error {
	switch {
	case errors.Is(err, games.ErrOff), errors.Is(err, games.ErrBusy), errors.Is(err, games.ErrOnDisk),
		errors.Is(err, search.ErrGamesOff), errors.Is(err, store.ErrLocked):
		return errorf(http.StatusConflict, err.Error())
	case errors.Is(err, search.ErrNoIndexers), errors.As(err, new(games.NotOnListError)):
		return errorf(http.StatusBadRequest, err.Error())
	}
	return err
}

// gameSystemView is a system's folder with what Box Office knows of the
// system.
type gameSystemView struct {
	store.GameSystem
	Name   string `json:"name"`
	Short  string `json:"short"`
	Maker  string `json:"maker"`
	Arcade bool   `json:"arcade"`
	// Hash says its games are checked byte for byte against their list.
	Hash bool `json:"hash"`
}

func systemView(g store.GameSystem) gameSystemView {
	sys, _ := gamedat.LookupSystem(g.System)
	return gameSystemView{GameSystem: g, Name: cmp.Or(sys.Name, g.System), Short: cmp.Or(sys.Short, g.System), Maker: sys.Maker, Arcade: sys.Arcade, Hash: sys.Hash}
}

func (s *Server) gameSystems(w http.ResponseWriter, r *http.Request) error {
	if err := s.gamesOn(r); err != nil {
		return err
	}
	all, err := s.Store.GameSystems(r.Context())
	if err != nil {
		return err
	}
	out := make([]gameSystemView, 0, len(all))
	for _, g := range all {
		out = append(out, systemView(g))
	}
	// MAME first, then the consoles in Box Office's order.
	order := func(v gameSystemView) int {
		return slices.IndexFunc(gamedat.Systems, func(x gamedat.System) bool { return x.Key == v.System })
	}
	slices.SortStableFunc(out, func(a, b gameSystemView) int { return cmp.Compare(order(a), order(b)) })
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) gameSystem(w http.ResponseWriter, r *http.Request) error {
	if err := s.gamesOn(r); err != nil {
		return err
	}
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	g, err := s.Store.GameSystem(r.Context(), id)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, systemView(g))
}

func (s *Server) editGameSystem(w http.ResponseWriter, r *http.Request) error {
	if err := s.gamesOn(r); err != nil {
		return err
	}
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in struct {
		Locked *bool `json:"locked"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.Locked != nil {
		if err := s.Store.LockGameSystem(r.Context(), id, *in.Locked); err != nil {
			return err
		}
	}
	return s.gameSystem(w, r)
}

func (s *Server) gameCheck(w http.ResponseWriter, r *http.Request) error {
	if err := s.gamesOn(r); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, s.Games.Progress())
}

func (s *Server) startGameCheck(w http.ResponseWriter, r *http.Request) error {
	if err := s.gamesOn(r); err != nil {
		return err
	}
	var in struct {
		RootID int    `json:"rootId"`
		System string `json:"system"`
	}
	if r.ContentLength != 0 {
		if err := readJSON(r, &in); err != nil {
			return err
		}
	}
	p, err := s.Games.Start(r.Context(), in.RootID, in.System)
	if err != nil {
		return gamesErr(err)
	}
	return writeJSON(w, http.StatusAccepted, p)
}

func (s *Server) gameSettings(w http.ResponseWriter, r *http.Request) error {
	if err := s.gamesOn(r); err != nil {
		return err
	}
	set, err := s.Games.Settings(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, set)
}

func (s *Server) saveGameSettings(w http.ResponseWriter, r *http.Request) error {
	if err := s.gamesOn(r); err != nil {
		return err
	}
	var in struct {
		MAMEVersion *string `json:"mameVersion"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.MAMEVersion != nil {
		if err := s.Games.SetMAMEVersion(r.Context(), *in.MAMEVersion); err != nil {
			if errors.Is(err, games.ErrOff) {
				return gamesErr(err)
			}
			return errorf(http.StatusBadRequest, err.Error())
		}
	}
	return s.gameSettings(w, r)
}

// gameGroup is a game with its other versions: a MAME parent with its
// clones, or one console game in every region and revision. Game is the
// one shown: the parent, or the first version.
type gameGroup struct {
	Game     store.Game `json:"game"`
	Versions int        `json:"versions"`
	// Problems counts the versions with something wrong: for a console
	// game, only when none of its versions is good.
	Problems int `json:"problems"`
}

type gamePage struct {
	System gameSystemView `json:"system"`
	Groups []gameGroup    `json:"groups"`
	// Total is how many groups match, of All.
	Total int `json:"total"`
	All   int `json:"all"`
	// Genres and Decades are what the filters can pick from.
	Genres  []string `json:"genres"`
	Decades []string `json:"decades"`
}

// gameList is one system's games, grouped, filtered and a page at a
// time: a full MAME set is tens of thousands.
func (s *Server) gameList(w http.ResponseWriter, r *http.Request) error {
	if err := s.gamesOn(r); err != nil {
		return err
	}
	q := r.URL.Query()
	sysID, err := strconv.ParseInt(q.Get("system"), 10, 64)
	if err != nil {
		return errorf(http.StatusBadRequest, "say which system's games")
	}
	sys, err := s.Store.GameSystem(r.Context(), sysID)
	if err != nil {
		return err
	}
	all, err := s.Store.Games(r.Context(), sysID)
	if err != nil {
		return err
	}
	type group struct {
		key   string
		games []store.Game
	}
	var groups []*group
	byKey := map[string]*group{}
	genres := map[string]bool{}
	decades := map[string]bool{}
	for _, g := range all {
		k := cmp.Or(g.Parent, g.Name)
		gr := byKey[k]
		if gr == nil {
			gr = &group{key: k}
			byKey[k] = gr
			groups = append(groups, gr)
		}
		gr.games = append(gr.games, g)
		if g.Genre != "" {
			genres[g.Genre] = true
		}
		if d := decade(g.Year); d != "" {
			decades[d] = true
		}
	}
	text := strings.ToLower(strings.TrimSpace(q.Get("q")))
	genre, dec, problems := q.Get("genre"), q.Get("decade"), q.Get("problems") == "1"
	var out []gameGroup
	for _, gr := range groups {
		main := mainVersion(gr.key, gr.games)
		v := gameGroup{Game: main, Versions: len(gr.games)}
		good := false
		match := text == ""
		for _, g := range gr.games {
			if g.Status != gamedat.Complete && g.Status != gamedat.Identified {
				v.Problems++
			} else {
				good = true
			}
			if !match && (strings.Contains(strings.ToLower(g.Title), text) || strings.Contains(strings.ToLower(g.Name), text) || strings.Contains(strings.ToLower(g.Maker), text)) {
				match = true
			}
		}
		if good && sys.System != "mame" {
			// A console game with a good copy is playable; a bad dump or
			// hack beside it isn't the game's problem, and its page lists
			// each version as it is.
			v.Problems = 0
		}
		if !match || (genre != "" && !slices.ContainsFunc(gr.games, func(g store.Game) bool { return g.Genre == genre })) ||
			(dec != "" && !slices.ContainsFunc(gr.games, func(g store.Game) bool { return decade(g.Year) == dec })) ||
			(problems && v.Problems == 0) {
			continue
		}
		if main.Parent != "" && main.Name != main.Parent && main.ParentTitle != "" && sys.System == "mame" {
			// A clone shown for its parent goes by the parent's name.
			v.Game.Title = main.ParentTitle
		}
		out = append(out, v)
	}
	switch q.Get("sort") {
	case "year":
		slices.SortStableFunc(out, func(a, b gameGroup) int {
			return cmp.Or(cmp.Compare(yearOrLast(a.Game.Year), yearOrLast(b.Game.Year)), cmp.Compare(strings.ToLower(a.Game.Title), strings.ToLower(b.Game.Title)))
		})
	case "added":
		slices.SortStableFunc(out, func(a, b gameGroup) int { return b.Game.AddedAt.Compare(a.Game.AddedAt) })
	default:
		slices.SortStableFunc(out, func(a, b gameGroup) int { return cmp.Compare(sortTitle(a.Game.Title), sortTitle(b.Game.Title)) })
	}
	page, _ := strconv.Atoi(q.Get("page"))
	size, _ := strconv.Atoi(q.Get("size"))
	page, size = max(page, 1), min(max(size, 1), 200)
	if q.Get("size") == "" {
		size = 60
	}
	res := gamePage{System: systemView(sys), Total: len(out), All: len(groups), Genres: sortedKeys(genres), Decades: sortedKeys(decades), Groups: []gameGroup{}}
	if from := (page - 1) * size; from < len(out) {
		res.Groups = out[from:min(from+size, len(out))]
	}
	return writeJSON(w, http.StatusOK, res)
}

// mainVersion is the version a group of games is shown by: MAME's
// parent set; for a console game, a complete copy, the USA's or the
// world's first, then Europe's, then any.
func mainVersion(key string, versions []store.Game) store.Game {
	score := func(g store.Game) int {
		n := 0
		if g.Name == key {
			n += 100
		}
		if g.Status == gamedat.Complete || g.Status == gamedat.Identified {
			n += 10
		}
		// An update or DLC isn't the game.
		if slices.ContainsFunc(g.Problems, func(p store.GameProblem) bool { return p.Kind == "update" || p.Kind == "dlc" }) {
			n -= 50
		}
		tags := strings.Join(gamedat.Tags(g.Name), ",")
		switch {
		case strings.Contains(tags, "USA"), strings.Contains(tags, "World"):
			n += 3
		case strings.Contains(tags, "Europe"):
			n += 2
		}
		if strings.Contains(tags, "Rev") || strings.Contains(tags, "Beta") || strings.Contains(tags, "Proto") {
			n--
		}
		return n
	}
	// Of two as good, the later version: v1.4 before v1.1.
	best := versions[0]
	for _, g := range versions[1:] {
		if a, b := score(g), score(best); a > b || (a == b && key != best.Name && g.Name > best.Name) {
			best = g
		}
	}
	return best
}

func decade(year string) string {
	if len(year) < 4 {
		return ""
	}
	y, err := strconv.Atoi(year[:4])
	if err != nil || y < 1950 {
		return ""
	}
	return strconv.Itoa(y/10*10) + "s"
}

func yearOrLast(year string) string {
	if len(year) >= 4 && year[0] >= '1' && year[0] <= '9' {
		return year[:4]
	}
	return "9999"
}

// sortTitle sorts "The King of Fighters" with K.
func sortTitle(t string) string {
	t = strings.ToLower(t)
	for _, a := range []string{"the ", "a "} {
		if strings.HasPrefix(t, a) {
			return t[len(a):]
		}
	}
	return t
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// gameFile is a file of a game, as it is on disk now.
type gameFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	// Parent says the file is the parent's set, which holds this clone.
	Parent bool `json:"parent,omitempty"`
}

type gameView struct {
	store.Game
	System gameSystemView `json:"system"`
	Files  []gameFile     `json:"files"`
	// Versions are the game's other versions: its parent and clones, or
	// other regions and revisions.
	Versions []store.Game `json:"versions"`
}

func (s *Server) game(w http.ResponseWriter, r *http.Request) error {
	if err := s.gamesOn(r); err != nil {
		return err
	}
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	g, err := s.Store.Game(r.Context(), id)
	if err != nil {
		return err
	}
	sys, err := s.Store.GameSystem(r.Context(), g.SystemID)
	if err != nil {
		return err
	}
	all, err := s.Store.Games(r.Context(), g.SystemID)
	if err != nil {
		return err
	}
	v := gameView{Game: g, System: systemView(sys), Files: []gameFile{}, Versions: []store.Game{}}
	key := cmp.Or(g.Parent, g.Name)
	for _, o := range all {
		if o.ID != g.ID && cmp.Or(o.Parent, o.Name) == key {
			v.Versions = append(v.Versions, o)
		}
	}
	slices.SortStableFunc(v.Versions, func(a, b store.Game) int {
		// The parent first.
		return cmp.Or(cmp.Compare(b2n(a.Name != key), b2n(b.Name != key)), cmp.Compare(a.Title, b.Title))
	})
	if st, err := os.Stat(g.Path); err == nil {
		if st.IsDir() {
			// A game kept as a folder.
			v.Files = append(v.Files, gameFile{Path: g.Path + string(filepath.Separator), Size: g.Size})
		} else {
			v.Files = append(v.Files, gameFile{Path: g.Path, Size: st.Size(), Parent: g.InParent})
		}
	}
	for _, p := range games.Parts(g.Path) {
		if st, err := os.Stat(p); err == nil {
			v.Files = append(v.Files, gameFile{Path: p, Size: st.Size()})
		}
	}
	if sys.System == "mame" {
		// The CHDs in the folder named for the set.
		dir := filepath.Join(sys.Path, g.Name)
		if entries, err := os.ReadDir(dir); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				if info, err := e.Info(); err == nil {
					v.Files = append(v.Files, gameFile{Path: filepath.Join(dir, e.Name()), Size: info.Size()})
				}
			}
		}
	}
	return writeJSON(w, http.StatusOK, v)
}

func b2n(b bool) int {
	if b {
		return 1
	}
	return 0
}

// gameArt is a game's picture, kept small in the data folder.
func (s *Server) gameArt(w http.ResponseWriter, r *http.Request) error {
	if err := s.gamesOn(r); err != nil {
		return err
	}
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	g, err := s.Store.Game(r.Context(), id)
	if err != nil {
		return err
	}
	sys, err := s.Store.GameSystem(r.Context(), g.SystemID)
	if err != nil {
		return err
	}
	p, err := s.Games.ArtFile(r.Context(), g, sys.System)
	if errors.Is(err, games.ErrNoArt) {
		return errorf(http.StatusNotFound, err.Error())
	}
	if err != nil {
		s.Log.Debug("fetching a game's picture", "game", g.Title, "err", err)
		return errorf(http.StatusBadGateway, "couldn't fetch the game's picture: "+err.Error())
	}
	w.Header().Set("Cache-Control", "private, max-age=604800")
	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeFile(w, r, p)
	return nil
}

// allGameSystems are the systems Box Office knows, to pick from.
func (s *Server) allGameSystems(w http.ResponseWriter, r *http.Request) error {
	return writeJSON(w, http.StatusOK, gamedat.Systems)
}

// lookupGames finds games on the systems' lists to want: q is the title,
// system one system's key, or none for the systems with folders.
func (s *Server) lookupGames(w http.ResponseWriter, r *http.Request) error {
	if err := s.gamesOn(r); err != nil {
		return err
	}
	var systems []string
	if sys := r.URL.Query().Get("system"); sys != "" {
		if _, ok := gamedat.LookupSystem(sys); !ok {
			return errorf(http.StatusBadRequest, "Box Office doesn't know a system called "+strconv.Quote(sys))
		}
		systems = []string{sys}
	}
	list, err := s.Games.LookupGames(r.Context(), r.URL.Query().Get("q"), systems)
	if err != nil {
		return gamesErr(err)
	}
	return writeJSON(w, http.StatusOK, list)
}

// sendArt serves a game's picture from ArtFile and the like.
func (s *Server) sendArt(w http.ResponseWriter, r *http.Request, p string, err error, what string) error {
	if errors.Is(err, games.ErrNoArt) || errors.Is(err, store.ErrNotFound) {
		return errorf(http.StatusNotFound, games.ErrNoArt.Error())
	}
	if err != nil {
		s.Log.Debug("fetching a game's picture", "game", what, "err", err)
		return errorf(http.StatusBadGateway, "couldn't fetch the game's picture: "+err.Error())
	}
	w.Header().Set("Cache-Control", "private, max-age=604800")
	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeFile(w, r, p)
	return nil
}

func (s *Server) lookupGameArt(w http.ResponseWriter, r *http.Request) error {
	if err := s.gamesOn(r); err != nil {
		return err
	}
	system, name := r.URL.Query().Get("system"), r.URL.Query().Get("name")
	p, err := s.Games.LookupArt(r.Context(), system, name)
	return s.sendArt(w, r, p, err, name)
}

func (s *Server) gameWantArt(w http.ResponseWriter, r *http.Request) error {
	if err := s.gamesOn(r); err != nil {
		return err
	}
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	p, err := s.Games.WantArt(r.Context(), id)
	return s.sendArt(w, r, p, err, strconv.FormatInt(id, 10))
}

// gameWants lists the games wanted, and how looking for each is going.
func (s *Server) gameWants(w http.ResponseWriter, r *http.Request) error {
	if err := s.gamesOn(r); err != nil {
		return err
	}
	list, err := s.Games.Wants(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, list)
}

// wantGame wants a version of a game from its system's list, and looks
// for it.
func (s *Server) wantGame(w http.ResponseWriter, r *http.Request) error {
	if err := s.gamesOn(r); err != nil {
		return err
	}
	var in struct {
		System string `json:"system"`
		Name   string `json:"name"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.System == "" || in.Name == "" {
		return errorf(http.StatusBadRequest, "say which game, and for which system")
	}
	want, err := s.Games.Want(r.Context(), in.System, in.Name)
	if err != nil {
		return gamesErr(err)
	}
	return writeJSON(w, http.StatusCreated, want)
}

// unwantGame forgets a game wanted, and stops its downloads. A game in
// its system's folder already stays there.
func (s *Server) unwantGame(w http.ResponseWriter, r *http.Request) error {
	if err := s.gamesOn(r); err != nil {
		return err
	}
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	want, err := s.Store.GameWant(r.Context(), id)
	if err != nil {
		return err
	}
	if err := s.Store.DeleteGameWant(r.Context(), id); err != nil {
		return err
	}
	s.cancelDownloads(r.Context(), store.GameKind, []int64{id}, "game "+want.Name)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// searchGame searches for a game wanted in the background, and grabs the
// best release.
func (s *Server) searchGame(w http.ResponseWriter, r *http.Request) error {
	if err := s.gamesOn(r); err != nil {
		return err
	}
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := s.Games.LookFor(r.Context(), id); err != nil {
		return gamesErr(err)
	}
	w.WriteHeader(http.StatusAccepted)
	return nil
}

// gameReleases searches the indexers for a game wanted, for picking a
// release by hand.
func (s *Server) gameReleases(w http.ResponseWriter, r *http.Request) error {
	if err := s.gamesOn(r); err != nil {
		return err
	}
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	res, err := s.Search.Game(r.Context(), id)
	if err != nil {
		return gamesErr(err)
	}
	return writeJSON(w, http.StatusOK, res)
}
