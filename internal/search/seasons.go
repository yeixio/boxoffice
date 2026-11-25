package search

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/yeixio/boxoffice/internal/quality"
	"github.com/yeixio/boxoffice/internal/store"
)

// seasonSearchFrom is how many wanted episodes of one season make Box Office
// search for the season as a whole, which finds season packs and costs one
// search instead of one for each episode.
const seasonSearchFrom = 3

// episodeCovers is which of the wanted episodes a release would bring, or
// better: the episodes it holds (all of the season for a pack) that have
// no copy, or one the profile still wants upgraded to its quality.
func episodeCovers(c Candidate, p quality.Profile, season int, eps []int, want []store.Episode) []int64 {
	var out []int64
	for _, e := range want {
		if e.Season != season || (len(eps) > 0 && !slices.Contains(eps, e.Number)) {
			continue
		}
		has, have := e.File != nil, 0
		if has {
			have = e.File.Quality
		}
		// A second copy of the first copy's quality isn't taken: the two
		// are kept to be different (want is seen as copyOf gives it).
		if c.mode == ModeSecond && e.Second != nil && e.Second.Quality == c.Quality {
			continue
		}
		if p.Wants(has, have, c.Quality) {
			out = append(out, e.ID)
		}
	}
	return out
}

// judgeEpisodes judges a release for a show, against the wanted episodes:
// a release of one or more episodes, or (packs true) a whole season.
// byID says the release came from a search by the show's TVDB id, so its
// name needn't match the show's.
func (s *Service) judgeEpisodes(ctx context.Context, r Release, t store.Series, p quality.Profile, want []store.Episode, blocked map[string]bool, packs, byID bool) Candidate {
	c := s.judge(ctx, r, p, false, 0, blocked)
	c.kind, c.titleID = "series", t.ID
	if isSecond(ctx) {
		// For the show's second copies, with its own key so a release
		// shown for the first copies isn't grabbed by hand in their place.
		c.mode = ModeSecond
		c.Key += "2"
	}
	// A show whose releases follow another episode order is judged by
	// each episode's place in it.
	want = s.inOrder(ctx, t, want)
	season, eps, ok := releaseEpisode(r.Title)
	if !ok {
		// Named by the day it aired, as talk shows and news are.
		season, eps, ok = s.datedEpisode(ctx, t, r.Title)
	}
	absolute := t.Numbering == store.NumberingAbsolute
	named := false // its name was read as the show's, numbered straight through
	if !ok && absolute {
		// Found by the show's id, it may go by the show's name in another
		// language, so "Any Name - 137" is read too.
		trusted := t.TVDBID > 0 && (byID || r.TVDBID == t.TVDBID)
		season, eps, ok, named = s.absoluteEpisode(ctx, t, r.Title, trusted)
	}
	switch {
	case r.TVDBID > 0 && t.TVDBID > 0:
		if r.TVDBID != t.TVDBID {
			c.reject("it's for another show")
		}
	case byID && t.TVDBID > 0:
	case named:
	case absolute && titleMatches(quality.StripLeadTags(r.Title), t.Title):
	case !titleMatches(r.Title, t.Title):
		c.reject("its name doesn't match the show's")
	}
	switch dubbed, original := quality.Dubbing(r.Title); {
	case t.Audio == store.AudioDubbed && !dubbed:
		c.reject("its name doesn't say it's dubbed, and the show is wanted dubbed")
	case t.Audio == store.AudioOriginal && dubbed && !original:
		c.reject("it's dubbed only, and the show is wanted with its original sound")
	}
	switch {
	case !ok:
		c.reject("its name doesn't say which episode it is")
		return c
	case len(eps) == 0 && !packs:
		c.reject(fmt.Sprintf("it's a whole season; Box Office takes those when %d or more episodes of a season are wanted", seasonSearchFrom))
		return c
	}
	c.Pack, c.Episodes = len(eps) == 0, eps
	if p.Sizes.Any() && t.Runtime > 0 {
		// A release of several episodes, or of a season, is as long as
		// all of them.
		n := len(eps)
		if c.Pack {
			n = s.seasonLength(ctx, t, season)
		}
		c.sized(p, t.Runtime*n)
	}
	c.episodes = episodeCovers(c, p, season, eps, want)
	if len(c.episodes) == 0 && c.Accepted {
		seasons := map[int]bool{}
		for _, e := range want {
			seasons[e.Season] = true
		}
		switch {
		case !seasons[season]:
			c.reject(fmt.Sprintf("it's season %d", season))
		case len(eps) > 0 && !slices.ContainsFunc(want, func(e store.Episode) bool { return e.Season == season && slices.Contains(eps, e.Number) }):
			c.reject(fmt.Sprintf("it's episode %d", eps[0]))
		default:
			c.reject("it isn't better than the copies there already")
		}
	}
	return c
}

// absoluteEpisode reads a release that numbers the show's episodes
// straight through ("Show - 137") as the season and episodes those are.
// named says the number followed the show's own title. Numbers the show
// doesn't have, or that fall in more than one season, aren't read.
func (s *Service) absoluteEpisode(ctx context.Context, t store.Series, name string, loose bool) (season int, eps []int, ok, named bool) {
	nums := quality.AbsoluteEpisodes(name, t.Title, false)
	named = nums != nil
	if nums == nil && loose {
		nums = quality.AbsoluteEpisodes(name, t.Title, true)
	}
	if nums == nil {
		return 0, nil, false, false
	}
	byNumber := s.absoluteNumbers(ctx, t.ID)
	for i, n := range nums {
		e, has := byNumber[n]
		if !has || (i > 0 && e.Season != season) {
			return 0, nil, false, false
		}
		season = e.Season
		eps = append(eps, e.Number)
	}
	return season, eps, true, named
}

// absoluteNumbers is a show's episodes by their number counted straight
// through, kept a minute: every release of a search is read against it.
func (s *Service) absoluteNumbers(ctx context.Context, seriesID int64) map[int]store.Episode {
	s.mu.Lock()
	have, ok := s.absolute[seriesID]
	s.mu.Unlock()
	if ok && time.Now().Before(have.expires) {
		return have.byNumber
	}
	eps, err := s.Store.Episodes(ctx, seriesID)
	if err != nil {
		s.logger().Warn("couldn't read a show's episodes to number them", "show", seriesID, "err", err)
		return nil
	}
	m := store.AbsoluteNumbers(eps)
	s.mu.Lock()
	if s.absolute == nil {
		s.absolute = map[int64]absoluteCache{}
	}
	s.absolute[seriesID] = absoluteCache{m, time.Now().Add(time.Minute)}
	s.mu.Unlock()
	return m
}

// datedEpisode reads a release named by the day it aired
// ("Show.2026.11.19") as the season and episode that aired that day, as
// the show's releases number them. A day when none or several of the
// show's episodes aired isn't read.
func (s *Service) datedEpisode(ctx context.Context, t store.Series, name string) (season int, eps []int, ok bool) {
	day, has := quality.AirDate(name)
	if !has {
		return 0, nil, false
	}
	e, has := airedOn(s.showEpisodes(ctx, t.ID), day)
	if !has {
		return 0, nil, false
	}
	in := s.inOrder(ctx, t, []store.Episode{e})[0]
	if in.Season < 0 {
		return 0, nil, false
	}
	return in.Season, []int{in.Number}, true
}

// airedOn is the one episode of a show (specials aside) that aired on a
// day.
func airedOn(eps []store.Episode, day time.Time) (store.Episode, bool) {
	var found store.Episode
	n := 0
	for _, e := range eps {
		if e.Season > 0 && e.AirDate != nil && quality.SameDay(*e.AirDate, day) {
			found = e
			n++
		}
	}
	return found, n == 1
}

// dailySeason is how many episodes a season has from which its show is
// taken for a daily one, whose releases are named by date.
const dailySeason = 40

// daily says whether an episode is one of a daily show's: one of a
// season of dailySeason episodes or more.
func (s *Service) daily(ctx context.Context, e store.Episode) bool {
	if e.Season <= 0 || e.AirDate == nil {
		return false
	}
	n := 0
	for _, x := range s.showEpisodes(ctx, e.SeriesID) {
		if x.Season == e.Season {
			n++
		}
	}
	return n >= dailySeason
}

// showEpisodes is a show's episodes, kept a minute: every release of a
// search may be read against them.
func (s *Service) showEpisodes(ctx context.Context, seriesID int64) []store.Episode {
	s.mu.Lock()
	have, ok := s.shows[seriesID]
	s.mu.Unlock()
	if ok && time.Now().Before(have.expires) {
		return have.eps
	}
	eps, err := s.Store.Episodes(ctx, seriesID)
	if err != nil {
		s.logger().Warn("couldn't read a show's episodes", "show", seriesID, "err", err)
		return nil
	}
	s.mu.Lock()
	if s.shows == nil {
		s.shows = map[int64]episodesCache{}
	}
	s.shows[seriesID] = episodesCache{eps, time.Now().Add(time.Minute)}
	s.mu.Unlock()
	return eps
}

type episodesCache struct {
	eps     []store.Episode
	expires time.Time
}

// inOrder is some of a show's episodes numbered the way its releases
// are (see store.InOrder); as they are, for a show that follows no other
// episode order.
func (s *Service) inOrder(ctx context.Context, t store.Series, eps []store.Episode) []store.Episode {
	if _, ok := t.Order(); !ok || len(eps) == 0 {
		return eps
	}
	return store.InOrderOf(eps, s.placesTaken(ctx, t.ID))
}

// placesTaken is the places a show's chosen episode order gives its
// episodes, kept a minute: every release of a search is read against it.
func (s *Service) placesTaken(ctx context.Context, seriesID int64) map[store.Place]bool {
	s.mu.Lock()
	have, ok := s.places[seriesID]
	s.mu.Unlock()
	if ok && time.Now().Before(have.expires) {
		return have.taken
	}
	eps, err := s.Store.Episodes(ctx, seriesID)
	if err != nil {
		s.logger().Warn("couldn't read a show's episodes to place them", "show", seriesID, "err", err)
		return nil
	}
	taken := store.PlacesTaken(eps)
	s.mu.Lock()
	if s.places == nil {
		s.places = map[int64]placesCache{}
	}
	s.places[seriesID] = placesCache{taken, time.Now().Add(time.Minute)}
	s.mu.Unlock()
	return taken
}

type placesCache struct {
	taken   map[store.Place]bool
	expires time.Time
}

// orderSeasons is the seasons to ask indexers for when season's wanted
// episodes are looked for: season itself, or for a show whose releases
// follow another episode order, the seasons those episodes have in it
// (three at most, so a search stays a few questions).
func (s *Service) orderSeasons(ctx context.Context, t store.Series, season int, want []store.Episode) []int {
	if _, ok := t.Order(); !ok {
		return []int{season}
	}
	var out []int
	for _, e := range s.inOrder(ctx, t, want) {
		if e.Season >= 0 && !slices.Contains(out, e.Season) {
			out = append(out, e.Season)
		}
	}
	slices.Sort(out)
	if len(out) == 0 {
		return []int{season}
	}
	return out[:min(len(out), 3)]
}

type absoluteCache struct {
	byNumber map[int]store.Episode
	expires  time.Time
}

// Season searches for a season of a show, judging packs and single
// episodes against the wanted episodes of it.
func (s *Service) Season(ctx context.Context, t store.Series, season int, want []store.Episode) (Results, error) {
	if err := s.Store.CheckUnlocked(ctx, "series", t.ID); err != nil {
		return Results{}, err
	}
	// A show whose releases follow another episode order is asked for by
	// the seasons the wanted episodes have in it.
	var rs []Release
	var notes []string
	n := 0
	for i, in := range s.orderSeasons(ctx, t, season, want) {
		got, more, asked, err := s.ask(ctx, func(ix store.Indexer) Query {
			q := Query{Type: "tvsearch", Season: in, Cats: cats(ix, 5000, 5999, 5000)}
			if t.TVDBID > 0 {
				q.TVDBID = t.TVDBID
			} else {
				q.Text = searchText(t.Title)
			}
			return q
		})
		if err != nil && i == 0 {
			return Results{}, err
		}
		if err != nil {
			notes = append(notes, err.Error())
			continue
		}
		rs, notes, n = append(rs, got...), append(notes, more...), max(n, asked)
	}
	p, err := s.showProfile(ctx, t)
	if err != nil {
		return Results{}, err
	}
	blocked, err := s.blocked(ctx, "series", t.ID)
	if err != nil {
		return Results{}, err
	}
	out := Results{Errors: notes, Searched: n}
	seen := map[string]bool{}
	for _, r := range rs {
		if seen[key(r)] {
			continue
		}
		seen[key(r)] = true
		out.Releases = append(out.Releases, s.judgeEpisodes(ctx, r, t, p, want, blocked, true, true))
	}
	if t.Numbering == store.NumberingAbsolute && season > 0 {
		// Anime numbered straight through isn't filed by season: ask for
		// the show by name too, and read each release's number.
		more, notes, _, err := s.ask(ctx, func(ix store.Indexer) Query {
			return Query{Type: "search", Text: searchText(t.Title), Cats: cats(ix, 5000, 5999, 5000)}
		})
		if err != nil {
			out.Errors = append(out.Errors, err.Error())
		} else {
			out.Errors = append(out.Errors, notes...)
			for _, r := range more {
				if !seen[key(r)] {
					seen[key(r)] = true
					out.Releases = append(out.Releases, s.judgeEpisodes(ctx, r, t, p, want, blocked, true, false))
				}
			}
		}
	}
	s.rank(ctx, &out)
	if out.Searched > 0 {
		for _, e := range want {
			found := slices.ContainsFunc(out.Releases, func(c Candidate) bool { return (c.Accepted || c.held) && slices.Contains(c.episodes, e.ID) })
			if err := s.Store.MarkSearched(ctx, "episode", e.ID, found); err != nil {
				s.logger().Warn("couldn't note a search", "kind", "episode", "id", e.ID, "err", err)
			}
		}
	}
	s.logResults(fmt.Sprintf("%s season %d", t.Title, season), out)
	return out, nil
}

// ErrNothingAired means a season has no episodes out yet to search for.
var ErrNothingAired = errors.New("none of its episodes have aired yet")

// SeasonReleases is Season for choosing by hand: every aired episode of
// the season counts as wanted, so packs and singles are judged against
// all of them.
func (s *Service) SeasonReleases(ctx context.Context, seriesID int64, season int) (Results, error) {
	t, err := s.Store.Series(ctx, seriesID)
	if err != nil {
		return Results{}, err
	}
	all, err := s.Store.Episodes(ctx, seriesID)
	if err != nil {
		return Results{}, err
	}
	now := time.Now()
	var want []store.Episode
	for _, e := range all {
		if e.Season == season && e.AirDate != nil && e.AirDate.Before(now) {
			want = append(want, e)
		}
	}
	if len(want) == 0 {
		return Results{}, fmt.Errorf("season %d: %w", season, ErrNothingAired)
	}
	return s.Season(ctx, t, season, want)
}

func isPack(c Candidate) bool {
	_, eps, ok := releaseEpisode(c.Title)
	return ok && len(eps) == 0
}

// outcome adds up what searching for some episodes did.
type outcome struct {
	searches, found, grabbed, busy int
	got, problems                  []string
	off, noIndexers                bool
}

func (o *outcome) add(b outcome) {
	o.searches += b.searches
	o.found += b.found
	o.grabbed += b.grabbed
	o.busy += b.busy
	o.got = append(o.got, b.got...)
	o.problems = append(o.problems, b.problems...)
	o.off = o.off || b.off
	o.noIndexers = o.noIndexers || b.noIndexers
}

// take grabs a candidate, noting how it went.
func (s *Service) take(ctx context.Context, c Candidate, what string, o *outcome) {
	if _, err := s.grab(ctx, c); errors.Is(err, ErrOff) {
		o.off = true
	} else if err != nil {
		o.problems = append(o.problems, fmt.Sprintf("%s: %v", what, err))
	} else {
		o.grabbed++
		o.got = append(o.got, what)
	}
}

// findEpisodes searches for wanted episodes of one season of a show and
// grabs the best of what's found: as a season when enough of it is wanted
// (a season pack, or else single episodes from the same results), or
// episode by episode.
func (s *Service) findEpisodes(ctx context.Context, t store.Series, season int, want []store.Episode) (outcome, error) {
	var o outcome
	var free []store.Episode
	for _, e := range want {
		if b, err := s.busy(ctx, "series", t.ID, e.ID, ""); err != nil {
			return o, err
		} else if b {
			o.busy++
			continue
		}
		free = append(free, e)
	}
	if len(free) == 0 {
		return o, nil
	}
	code := func(e store.Episode) string { return fmt.Sprintf("%s S%02dE%02d", t.Title, e.Season, e.Number) }
	// Specials are looked for one by one: indexers file them as season 0,
	// and there's no pack of them to find.
	if len(free) < seasonSearchFrom || season == 0 {
		for _, e := range free {
			if ctx.Err() != nil {
				return o, ctx.Err()
			}
			res, err := s.Episode(ctx, e.ID)
			if errors.Is(err, ErrNoIndexers) {
				o.noIndexers = true
				return o, nil
			}
			if err != nil {
				o.problems = append(o.problems, fmt.Sprintf("%s: %v", code(e), err))
				continue
			}
			if res.Searched > 0 {
				o.searches++
			}
			i := slices.IndexFunc(res.Releases, func(c Candidate) bool { return c.Accepted })
			if i < 0 {
				o.problems = append(o.problems, res.Errors...)
				continue
			}
			o.found++
			if b, err := s.busy(ctx, "series", t.ID, e.ID, res.Releases[i].Title); err != nil {
				return o, err
			} else if b {
				continue
			}
			s.take(ctx, res.Releases[i], code(e), &o)
		}
		return o, nil
	}

	res, err := s.Season(ctx, t, season, free)
	if errors.Is(err, ErrNoIndexers) {
		o.noIndexers = true
		return o, nil
	}
	if err != nil {
		o.problems = append(o.problems, fmt.Sprintf("%s season %d: %v", t.Title, season, err))
		return o, nil
	}
	if res.Searched > 0 {
		o.searches++
	}
	o.problems = append(o.problems, res.Errors...)
	covered := map[int64]bool{}
	for _, c := range res.Releases {
		if c.Accepted {
			for _, id := range c.episodes {
				covered[id] = true
			}
		}
	}
	o.found = len(covered)
	// A season pack, unless single episodes come in a better quality.
	bestPack := slices.IndexFunc(res.Releases, func(c Candidate) bool { return c.Accepted && isPack(c) })
	bestOne := slices.IndexFunc(res.Releases, func(c Candidate) bool { return c.Accepted && !isPack(c) })
	taken := map[int64]bool{}
	if bestPack >= 0 && (bestOne < 0 || quality.Rank(res.Releases[bestPack].Quality) >= quality.Rank(res.Releases[bestOne].Quality)) {
		c := res.Releases[bestPack]
		if b, err := s.Store.GrabbedBefore(ctx, "series", t.ID, c.Title); err != nil {
			return o, err
		} else if !b {
			before := o.grabbed
			s.take(ctx, c, fmt.Sprintf("%s season %d", t.Title, season), &o)
			if o.grabbed > before {
				// One grab, for several episodes.
				for _, id := range c.episodes {
					taken[id] = true
				}
			}
		}
	}
	for _, e := range free {
		if taken[e.ID] || o.off {
			continue
		}
		i := slices.IndexFunc(res.Releases, func(c Candidate) bool { return c.Accepted && !isPack(c) && slices.Contains(c.episodes, e.ID) })
		if i < 0 {
			continue
		}
		c := res.Releases[i]
		if b, err := s.busy(ctx, "series", t.ID, e.ID, c.Title); err != nil {
			return o, err
		} else if b {
			continue
		}
		s.take(ctx, c, code(e), &o)
		for _, id := range c.episodes {
			taken[id] = true
		}
	}
	return o, nil
}

// bySeason groups episodes by season, in season order.
func bySeason(eps []store.Episode) [][]store.Episode {
	idx := map[int]int{}
	var out [][]store.Episode
	for _, e := range eps {
		i, ok := idx[e.Season]
		if !ok {
			i = len(out)
			idx[e.Season] = i
			out = append(out, nil)
		}
		out[i] = append(out[i], e)
	}
	slices.SortStableFunc(out, func(a, b []store.Episode) int { return a[0].Season - b[0].Season })
	return out
}

func compactProblems(ps []string) string {
	slices.Sort(ps)
	return strings.Join(slices.Compact(ps), "; ")
}

// seasonLength is how many episodes a season has (as the show's releases
// number them), or 0 when that can't be told.
func (s *Service) seasonLength(ctx context.Context, t store.Series, season int) int {
	eps, err := s.Store.Episodes(ctx, t.ID)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range store.InOrder(eps) {
		if e.Season == season {
			n++
		}
	}
	return n
}
