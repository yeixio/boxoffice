package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/yeixio/boxoffice/internal/gamedat"
	"github.com/yeixio/boxoffice/internal/jobs"
	"github.com/yeixio/boxoffice/internal/mediatypes"
	"github.com/yeixio/boxoffice/internal/store"
)

// JobSearchGame searches for one game wanted and grabs the best release.
const JobSearchGame = "search-game"

// Newznab's console categories, and Other/Misc, where indexers often put
// games for older systems.
const (
	catConsole     = 1000
	catConsoleLast = 1999
	catMisc        = 8010
)

// ErrGamesOff means Games isn't turned on in Settings.
var ErrGamesOff = errors.New("turn on Games in Settings first")

// gameCats are the categories to search an indexer in for a game.
func gameCats(ix store.Indexer) []int {
	return append(cats(ix, catConsole, catConsoleLast, catConsole), catMisc)
}

var (
	// rePack is a release of many games: a whole system's set.
	rePack = regexp.MustCompile(`(?i)\b(rom ?sets?|full ?sets?|complete ?(set|collection|romset)|collection|compilation|\d{2,} ?(games|roms)|all ?games|no ?intro|redump|tosec|goodsets?|merged|non ?merged|split ?set)\b`)
	// reNotROM is a release of something else: a PC or modern console
	// game, a soundtrack, a guide or a film.
	reNotROM = regexp.MustCompile(`(?i)\b(ost|soundtrack|flac|mp3|guide|walkthrough|artbook|1080p|720p|2160p|x264|x265|bluray|web ?dl|repack|fitgirl|codex|gog|steam|nsp|xci|pkg|iso)\b`)
)

// gameMaxSize is the most a release of one game can be: a little over
// twice its file on the list, or 8 GB for a MAME set (its CHDs aren't
// sized on the list).
func gameMaxSize(w store.GameWant, arcade bool) int64 {
	if arcade || w.Size <= 0 {
		return 8 << 30
	}
	return 2*w.Size + 8<<20
}

// systemWords are the words a release name gives a system by: its key,
// short name and usual folder names, long enough not to be other words.
func systemWords(sys gamedat.System) []string {
	out := []string{sys.Key}
	add := func(w string) {
		w = strings.ToLower(w)
		if len(w) >= 3 && !slices.Contains(out, w) && !slices.Contains([]string{"roms", "arcade", "arcades", "nintendo", "sega", "atari"}, w) {
			out = append(out, w)
		}
	}
	add(strings.ReplaceAll(sys.Short, " ", ""))
	for _, f := range sys.Folders {
		add(f)
	}
	return out
}

// namedSystems are the systems a release name says it's for, read from
// its words and from pairs of them ("game boy", "mega drive").
func namedSystems(release string) []string {
	ws := strings.Fields(normalize(release))
	tokens := map[string]bool{}
	for i, w := range ws {
		tokens[w] = true
		if i+1 < len(ws) {
			tokens[w+ws[i+1]] = true
		}
		if i+2 < len(ws) {
			tokens[w+ws[i+1]+ws[i+2]] = true
		}
	}
	var out []string
	for _, sys := range gamedat.Systems {
		if slices.ContainsFunc(systemWords(sys), func(w string) bool { return tokens[w] }) {
			out = append(out, sys.Key)
		}
	}
	return out
}

// gameQuery is a game's title as an indexer is asked for it.
func gameQuery(w store.GameWant) string {
	return normalize(gamedat.Title(w.Title))
}

// gameMatches says whether a release is named for a game: every word of
// its title in order, or for MAME its set name.
func gameMatches(release string, w store.GameWant, arcade bool) bool {
	rel := " " + normalize(release) + " "
	if inOrder(rel, words(gamedat.Title(w.Title)), 0) >= 0 {
		return true
	}
	return arcade && strings.Contains(rel, " "+strings.ToLower(w.Name)+" ")
}

func (s *Service) gamesOn(ctx context.Context) error {
	on, err := mediatypes.Enabled(ctx, s.Store, mediatypes.Game)
	if err != nil {
		return err
	}
	if !on {
		return ErrGamesOff
	}
	return nil
}

// Game searches for a game wanted and judges each release.
func (s *Service) Game(ctx context.Context, id int64) (Results, error) {
	if err := s.gamesOn(ctx); err != nil {
		return Results{}, err
	}
	w, err := s.Store.GameWant(ctx, id)
	if err != nil {
		return Results{}, err
	}
	if err := s.Store.CheckUnlocked(ctx, store.GameKind, w.ID); err != nil {
		return Results{}, err
	}
	sys, ok := gamedat.LookupSystem(w.System)
	if !ok {
		return Results{}, fmt.Errorf("Box Office doesn't know a system called %q", w.System)
	}
	text := gameQuery(w)
	rs, notes, n, err := s.ask(ctx, func(ix store.Indexer) Query {
		return Query{Type: "search", Text: text, Cats: gameCats(ix)}
	})
	if err != nil {
		return Results{}, err
	}
	blocked, err := s.blocked(ctx, store.GameKind, w.ID)
	if err != nil {
		return Results{}, err
	}
	out := Results{Errors: notes, Searched: n}
	for _, r := range rs {
		out.Releases = append(out.Releases, s.judgeGame(r, w, sys, blocked))
	}
	s.rank(ctx, &out)
	s.markSearched(ctx, store.GameKind, w.ID, out)
	s.logResults(GameSubject(w), out)
	return out, nil
}

// judgeGame says whether a release is the game wanted, for its system.
// What a download holds is checked against the system's list before it
// goes in, so this only passes over what plainly isn't it.
func (s *Service) judgeGame(r Release, w store.GameWant, sys gamedat.System, blocked map[string]bool) Candidate {
	c := Candidate{Release: r, Accepted: true, Key: key(r), kind: store.GameKind, titleID: w.ID}
	if r.Protocol != "usenet" {
		c.reject("torrent downloads aren't supported yet")
	}
	if blocked[strings.ToLower(r.Title)] {
		c.reject("it's on the blocklist")
	}
	if lockedNoPassword(r) {
		c.reject(whyLocked)
	}
	if !gameMatches(r.Title, w, sys.Arcade) {
		c.reject("its name doesn't match the game's")
	}
	// A system named in the game's own title ("Game Boy Gallery") says
	// nothing.
	inTitle := namedSystems(w.Title)
	named := slices.DeleteFunc(namedSystems(r.Title), func(k string) bool { return k != sys.Key && slices.Contains(inTitle, k) })
	switch {
	case slices.Contains(named, sys.Key):
		c.Format, c.formatRank = sys.Short, 1
	case len(named) > 0:
		other, _ := gamedat.LookupSystem(named[0])
		c.reject(fmt.Sprintf("it's for %s, not %s", other.Name, sys.Name))
	}
	if rePack.MatchString(r.Title) && !rePack.MatchString(w.Title) {
		c.reject("it's a set of many games, not this one")
	}
	if reNotROM.MatchString(r.Title) && !reNotROM.MatchString(w.Title) {
		c.reject(fmt.Sprintf("it isn't a %s game", sys.Short))
	}
	if limit := gameMaxSize(w, sys.Arcade); r.Size > limit {
		c.reject(fmt.Sprintf("at %s it's too big to be one %s game", humanSize(r.Size), sys.Short))
	}
	return c
}

// GameSubject is how a game is named in jobs and logs: "Super Metroid
// (SNES)".
func GameSubject(w store.GameWant) string {
	short := w.System
	if sys, ok := gamedat.LookupSystem(w.System); ok {
		short = sys.Short
	}
	return fmt.Sprintf("%s (%s)", w.Title, short)
}

// GameJob is the payload of a search job for a game wanted.
func GameJob(id int64) string {
	p, _ := json.Marshal(struct {
		ID int64 `json:"id"`
	}{id})
	return string(p)
}

// QueueGame queues a search for a game wanted.
func QueueGame(r *jobs.Runner) func(ctx context.Context, w store.GameWant) error {
	return func(ctx context.Context, w store.GameWant) error {
		_, err := r.Queue(ctx, JobSearchGame, GameSubject(w), GameJob(w.ID))
		return err
	}
}

func (s *Service) jobGame(ctx context.Context, payload string) (jobs.Result, error) {
	id, err := payloadID(payload)
	if err != nil {
		return jobs.Result{}, err
	}
	if b, err := s.busy(ctx, store.GameKind, id, 0, ""); err != nil {
		return jobs.Result{}, err
	} else if b {
		return jobs.Skipped("it's downloading already"), nil
	}
	res, err := s.Game(ctx, id)
	if errors.Is(err, ErrNoIndexers) || errors.Is(err, store.ErrLocked) || errors.Is(err, ErrGamesOff) {
		return jobs.Skipped("%s", err.Error()), nil
	}
	if errors.Is(err, store.ErrNotFound) {
		return jobs.Skipped("it's no longer wanted"), nil
	}
	if err != nil {
		return jobs.Result{}, err
	}
	return s.pick(ctx, res, store.GameKind, id, 0)
}

// gotWait is how long a game just moved in isn't looked for again,
// while its system is checked.
const gotWait = 24 * time.Hour

// wantedGames are the games wanted, while Games is on.
func (s *Service) wantedGames(ctx context.Context, now time.Time) ([]Wanted, error) {
	if on, err := mediatypes.Enabled(ctx, s.Store, mediatypes.Game); err != nil || !on {
		return nil, err
	}
	list, err := s.Store.GameWants(ctx)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	searched, err := s.Store.Searched(ctx, store.GameKind)
	if err != nil {
		return nil, err
	}
	var out []Wanted
	for _, w := range list {
		if !w.GotAt.IsZero() && now.Sub(w.GotAt) < gotWait {
			continue
		}
		out = append(out, Wanted{Kind: store.GameKind, ID: w.ID, Subject: GameSubject(w),
			Searched: searched[w.ID].At, Misses: searched[w.ID].Misses})
	}
	return out, nil
}
