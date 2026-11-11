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
	season, eps, ok := releaseEpisode(r.Title)
	switch {
	case r.TVDBID > 0 && t.TVDBID > 0:
		if r.TVDBID != t.TVDBID {
			c.reject("it's for another show")
		}
	case byID && t.TVDBID > 0:
	case !titleMatches(r.Title, t.Title):
		c.reject("its name doesn't match the show's")
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

// Season searches for a season of a show, judging packs and single
// episodes against the wanted episodes of it.
func (s *Service) Season(ctx context.Context, t store.Series, season int, want []store.Episode) (Results, error) {
	if err := s.Store.CheckUnlocked(ctx, "series", t.ID); err != nil {
		return Results{}, err
	}
	rs, notes, n, err := s.ask(ctx, func(ix store.Indexer) Query {
		q := Query{Type: "tvsearch", Season: season, Cats: cats(ix, 5000, 5999, 5000)}
		if t.TVDBID > 0 {
			q.TVDBID = t.TVDBID
		} else {
			q.Text = searchText(t.Title)
		}
		return q
	})
	if err != nil {
		return Results{}, err
	}
	p, err := s.profile(ctx, t.ProfileID)
	if err != nil {
		return Results{}, err
	}
	blocked, err := s.blocked(ctx, "series", t.ID)
	if err != nil {
		return Results{}, err
	}
	out := Results{Errors: notes, Searched: n}
	for _, r := range rs {
		out.Releases = append(out.Releases, s.judgeEpisodes(ctx, r, t, p, want, blocked, true, true))
	}
	s.rank(ctx, &out)
	if out.Searched > 0 {
		for _, e := range want {
			found := slices.ContainsFunc(out.Releases, func(c Candidate) bool { return c.Accepted && slices.Contains(c.episodes, e.ID) })
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
