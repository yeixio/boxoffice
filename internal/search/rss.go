package search

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/quality"
	"github.com/yeixio/boxoffice/internal/store"
)

// RSS sync reads each indexer's newest releases every few minutes and
// grabs any that a wanted movie or episode takes: the cheap way to catch
// new episodes and movies as they're posted, at one API call an indexer
// (more after a long gap), whatever the size of the library.

// rssPages caps how many pages of 100 one sync reads from an indexer, to
// catch up after a gap without using up its limit. A first sync reads one.
const rssPages = 5

// RSSResult is how the last sync went.
type RSSResult struct {
	At       time.Time `json:"at"`
	Releases int       `json:"releases"` // new releases read
	Grabbed  []string  `json:"grabbed,omitempty"`
	Problems []string  `json:"problems,omitempty"`
	Skipped  string    `json:"skipped,omitempty"` // why it didn't run
}

type rssState struct {
	mu   sync.Mutex
	last *RSSResult
	busy bool
}

// LastRSS is how the last sync went, if one ran since Box Office started.
func (s *Service) LastRSS() *RSSResult {
	s.rss.mu.Lock()
	defer s.rss.mu.Unlock()
	return s.rss.last
}

// RSSDue says whether RSS sync should run, and why not.
func (s *Service) RSSDue(ctx context.Context) (bool, string) {
	a, err := LoadAutoSettings(ctx, s.Store)
	if err != nil {
		return false, err.Error()
	}
	if !a.RSS {
		return false, "RSS sync is off"
	}
	d, err := downloader.LoadSettings(ctx, s.Store)
	if err != nil {
		return false, err.Error()
	}
	if !d.Enabled {
		return false, "downloading is off, so there's nothing to grab with"
	}
	return true, ""
}

// RSSEvery syncs every few minutes (as set), starting a couple of minutes
// after Box Office does.
func (s *Service) RSSEvery(ctx context.Context) {
	wait := 2 * time.Minute
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		a, err := LoadAutoSettings(ctx, s.Store)
		if err != nil {
			wait = DefaultRSSMinutes * time.Minute
			continue
		}
		wait = time.Duration(a.RSSMinutes) * time.Minute
		s.SyncRSS(ctx)
	}
}

// SyncRSS reads the indexers' new releases and grabs what's wanted.
func (s *Service) SyncRSS(ctx context.Context) RSSResult {
	s.rss.mu.Lock()
	if s.rss.busy {
		s.rss.mu.Unlock()
		return RSSResult{At: time.Now(), Skipped: "a sync is running already"}
	}
	s.rss.busy = true
	s.rss.mu.Unlock()
	res := s.syncRSS(automatic(ctx))
	res.At = time.Now().UTC()
	s.rss.mu.Lock()
	s.rss.busy = false
	s.rss.last = &res
	s.rss.mu.Unlock()
	if res.Skipped == "" {
		s.logger().Info("RSS sync finished", "releases", res.Releases, "grabbed", strings.Join(res.Grabbed, ", "), "problems", strings.Join(res.Problems, "; "))
	} else {
		s.logger().Debug("RSS sync skipped", "why", res.Skipped)
	}
	return res
}

func (s *Service) syncRSS(ctx context.Context) RSSResult {
	if ok, why := s.RSSDue(ctx); !ok {
		return RSSResult{Skipped: why}
	}
	c, err := downloader.LoadCap(ctx, s.Store)
	if err != nil {
		return RSSResult{Skipped: err.Error()}
	}
	if c.Level == downloader.CapMissing || c.Level == downloader.CapFull {
		return RSSResult{Skipped: c.Why()}
	}
	all, err := s.Store.Indexers(ctx)
	if err != nil {
		return RSSResult{Skipped: err.Error()}
	}
	sts, err := s.Store.IndexerStatuses(ctx)
	if err != nil {
		return RSSResult{Skipped: err.Error()}
	}
	var res RSSResult
	var releases []Release
	now := time.Now()
	asked := 0
	for _, ix := range all {
		if !ix.EnableRSS || ix.Protocol != "usenet" || ix.APIKey == "" {
			continue
		}
		if sts[ix.ID].Resting(now) {
			res.Problems = append(res.Problems, restingNote(ix, sts[ix.ID]))
			continue
		}
		asked++
		rs, err := s.readFeed(ctx, ix, sts[ix.ID])
		if err != nil {
			res.Problems = append(res.Problems, err.Error())
		}
		releases = append(releases, rs...)
	}
	if asked == 0 && len(res.Problems) == 0 {
		return RSSResult{Skipped: "no indexer has RSS on"}
	}
	res.Releases = len(releases)
	if len(releases) == 0 || ctx.Err() != nil {
		return res
	}
	got, problems, err := s.takeNew(ctx, c, releases)
	if err != nil {
		problems = append(problems, err.Error())
	}
	res.Grabbed = got
	res.Problems = append(res.Problems, problems...)
	return res
}

// takeNew grabs what's wanted among new releases (RSS sync's, or held
// ones now old enough): missing titles, upgrades when they're allowed,
// and fixes of copies on disk while the data cap allows upgrades.
//
// movies are missing movies the releases were held for: they're looked
// at even if they aren't out by their dates yet, as the search that found
// the release (when the movie was added, say) would have grabbed it.
func (s *Service) takeNew(ctx context.Context, c downloader.CapState, releases []Release, movies ...int64) ([]string, []string, error) {
	if len(releases) == 0 {
		return nil, nil, nil
	}
	a, err := LoadAutoSettings(ctx, s.Store)
	if err != nil {
		return nil, nil, err
	}
	left, _, err := s.UpgradesLeft(ctx, a)
	if err != nil {
		return nil, nil, err
	}
	skip := func(w Wanted) bool {
		return w.Upgrade && (c.Level == downloader.CapUpgrades || left == 0 || !a.upgradeOn(w.AutoUpgrade))
	}
	got, problems, err := s.takeFromFeed(ctx, a, releases, skip, movies...)
	if err != nil {
		problems = append(problems, err.Error())
	}
	if c.Level == downloader.CapOK {
		more, morep, err := s.takeFixes(ctx, releases)
		if err != nil {
			morep = append(morep, err.Error())
		}
		got = append(got, more...)
		problems = append(problems, morep...)
	}
	return got, problems, nil
}

// readFeed reads an indexer's releases since the last sync, newest first.
func (s *Service) readFeed(ctx context.Context, ix store.Indexer, st store.IndexerStatus) ([]Release, error) {
	cs := append(cats(ix, 2000, 2999, 2000), cats(ix, 5000, 5999, 5000)...)
	pages := rssPages
	if st.RSSGUID == "" {
		pages = 1
	}
	var out []Release
	for page := 0; page < pages; page++ {
		rs, err := s.query(ctx, ix, Query{Type: "search", Cats: cs, Offset: page * 100})
		if err != nil {
			return out, err
		}
		seen := false
		for _, r := range rs {
			if r.GUID == st.RSSGUID || (!st.RSSAt.IsZero() && !r.PublishedAt.IsZero() && !r.PublishedAt.After(st.RSSAt)) {
				seen = true
				break
			}
			out = append(out, r)
		}
		if seen || len(rs) < 100 {
			break
		}
	}
	if len(out) > 0 {
		newest := out[0]
		for _, r := range out {
			if r.PublishedAt.After(newest.PublishedAt) {
				newest = r
			}
		}
		if err := s.Store.MarkRSS(ctx, ix.ID, newest.GUID, newest.PublishedAt); err != nil {
			return out, err
		}
	} else if err := s.Store.MarkRSS(ctx, ix.ID, "", time.Time{}); err != nil {
		return out, err
	}
	return out, nil
}

// takeFromFeed matches releases to the wanted movies and episodes, and
// grabs the best for each.
// Missing movies named in also are looked at too, whether out by their
// dates or not (see takeNew).
//
// Movies and episodes that aren't out yet but soon will be (earlyWants)
// are looked at too, unless a.Early is off; a release of one is only
// taken when earlyMiss lets it.
func (s *Service) takeFromFeed(ctx context.Context, a AutoSettings, releases []Release, skip func(Wanted) bool, also ...int64) ([]string, []string, error) {
	list, err := s.WantedList(ctx)
	if err != nil {
		return nil, nil, err
	}
	if a.Early != EarlyOff {
		more, err := s.earlyWants(ctx, time.Now())
		if err != nil {
			return nil, nil, err
		}
		list = append(list, more...)
	}
	earlyMovies, earlyEps := map[int64]bool{}, map[int64]bool{}
	ps, err := s.Store.Profiles(ctx)
	if err != nil {
		return nil, nil, err
	}
	profiles := map[int]quality.Profile{}
	for _, p := range ps {
		profiles[p.ID] = p
	}
	type showWant struct {
		t    store.Series
		eps  []store.Episode
		per  map[int]int // wanted episodes by season
		p    quality.Profile
		norm string
		ctx  context.Context // asSecond for the show's second copies
	}
	type showKey struct {
		id   int64
		copy string
	}
	movies := map[int64]store.Movie{}
	upgrades := map[int64]bool{} // movies whose copies on disk may be upgraded
	shows := map[showKey]*showWant{}
	for _, w := range list {
		if skip(w) {
			continue
		}
		switch w.Kind {
		case "movie":
			m, err := s.Store.Movie(ctx, w.ID)
			if err != nil {
				continue
			}
			movies[m.ID] = m
			upgrades[m.ID] = w.Upgrade
			earlyMovies[m.ID] = w.Early
		case "episode":
			// A show's second copies are matched as their own want, by
			// their own profile.
			ctx := ctx
			if w.Copy == ModeSecond {
				ctx = asSecond(ctx)
			}
			sw, ok := shows[showKey{w.SeriesID, w.Copy}]
			if !ok {
				t, err := s.Store.Series(ctx, w.SeriesID)
				if err != nil {
					continue
				}
				sw = &showWant{t: t, per: map[int]int{}, p: profiles[t.ProfileID], norm: normalize(t.Title), ctx: ctx}
				if w.Copy == ModeSecond {
					sw.p = profiles[t.SecondProfileID]
				}
				shows[showKey{w.SeriesID, w.Copy}] = sw
			}
			e, err := s.episode(ctx, w.ID)
			if err != nil {
				continue
			}
			sw.eps = append(sw.eps, e)
			if w.Early {
				// Not counted toward a season pack: one isn't out yet.
				earlyEps[e.ID] = true
			} else {
				sw.per[e.Season]++
			}
		}
	}
	for _, id := range also {
		if _, ok := movies[id]; ok {
			continue
		}
		if m, err := s.Store.Movie(ctx, id); err == nil && m.Monitored && m.File == nil {
			movies[m.ID] = m
		}
	}
	if len(movies) == 0 && len(shows) == 0 {
		return nil, nil, nil
	}
	blockedFor := map[string]map[string]bool{}
	blocked := func(kind string, id int64) map[string]bool {
		k := fmt.Sprintf("%s/%d", kind, id)
		if b, ok := blockedFor[k]; ok {
			return b
		}
		b, _ := s.blocked(ctx, kind, id)
		blockedFor[k] = b
		return b
	}
	var out Results
	for _, r := range releases {
		r.Protocol = "usenet"
		if season, eps, ok := releaseEpisode(r.Title); ok {
			var matched int64 // the one show it's taken for
			for _, sw := range shows {
				if matched != 0 && sw.t.ID != matched {
					continue
				}
				if r.TVDBID > 0 && sw.t.TVDBID > 0 {
					if r.TVDBID != sw.t.TVDBID {
						continue
					}
				} else if !titleMatches(r.Title, sw.t.Title) &&
					!(sw.t.Numbering == store.NumberingAbsolute && titleMatches(quality.StripLeadTags(r.Title), sw.t.Title)) {
					continue
				}
				packs := len(eps) == 0 && sw.per[season] >= seasonSearchFrom
				c := s.judgeEpisodes(sw.ctx, r, sw.t, sw.p, sw.eps, blocked("series", sw.t.ID), packs, false)
				if c.Accepted && slices.ContainsFunc(c.episodes, func(id int64) bool { return earlyEps[id] }) {
					if why := earlyMiss(a.Early, sw.p, c); why != "" {
						c.reject(why)
					}
				}
				if c.Accepted {
					out.Releases = append(out.Releases, c)
				}
				// Its second copies, when it keeps them, may want it too.
				matched = sw.t.ID
			}
			continue
		}
		// Anime numbered straight through ("Show - 137"), for the shows
		// that say their releases are.
		taken := false
		for _, sw := range shows {
			if sw.t.Numbering != store.NumberingAbsolute {
				continue
			}
			if r.TVDBID > 0 && sw.t.TVDBID > 0 {
				if r.TVDBID != sw.t.TVDBID {
					continue
				}
			} else if quality.AbsoluteEpisodes(r.Title, sw.t.Title, false) == nil {
				continue
			}
			c := s.judgeEpisodes(sw.ctx, r, sw.t, sw.p, sw.eps, blocked("series", sw.t.ID), false, false)
			if c.Accepted && slices.ContainsFunc(c.episodes, func(id int64) bool { return earlyEps[id] }) {
				if why := earlyMiss(a.Early, sw.p, c); why != "" {
					c.reject(why)
				}
			}
			if c.Accepted {
				out.Releases = append(out.Releases, c)
			}
			taken = true
		}
		if taken {
			continue
		}
		year := releaseYear(r.Title)
		for _, m := range movies {
			switch {
			case r.IMDBID != "" && m.IMDBID != "":
				if r.IMDBID != m.IMDBID {
					continue
				}
			case r.TMDBID > 0 && m.TMDBID > 0:
				if r.TMDBID != m.TMDBID {
					continue
				}
			case !titleMatches(r.Title, m.Title) || year == 0 || m.Year == 0 || year < m.Year-1 || year > m.Year+1:
				continue
			}
			has, have := m.File != nil, 0
			if has {
				have = m.File.Quality
			}
			if !has || upgrades[m.ID] {
				c := s.judge(ctx, r, profiles[m.ProfileID], has, have, blocked("movie", m.ID))
				c.kind, c.titleID = "movie", m.ID
				c.sized(profiles[m.ProfileID], m.Runtime)
				if c.Accepted && earlyMovies[m.ID] {
					if why := earlyMiss(a.Early, profiles[m.ProfileID], c); why != "" {
						c.reject(why)
					}
				}
				if c.Accepted {
					out.Releases = append(out.Releases, c)
				}
			}
			// Its second copy, when it keeps one (not before it's out).
			if p2, ok := profiles[m.SecondProfileID]; ok && m.SecondProfileID != 0 && !p2.Locked && !earlyMovies[m.ID] && (m.Second == nil || upgrades[m.ID]) {
				if c := s.judgeSecond(ctx, r, m, p2, blocked("movie", m.ID)); c.Accepted {
					out.Releases = append(out.Releases, c)
				}
			}
			break
		}
	}
	if len(out.Releases) == 0 {
		return nil, nil, nil
	}
	s.rank(ctx, &out)
	var got, problems []string
	taken := map[string]bool{}
	for _, c := range out.Releases {
		if ctx.Err() != nil {
			break
		}
		if !c.Accepted {
			continue // too new yet, and held back (holdYoung)
		}
		if len(c.Avoided) > 0 {
			// The profile avoids it. RSS sync sees releases a few at a
			// time, so it's left for a search, which can tell whether
			// there's anything better.
			continue
		}
		var keys []string
		if c.kind == "movie" {
			keys = []string{fmt.Sprintf("m%d%s", c.titleID, c.mode)}
		} else {
			for _, id := range c.episodes {
				keys = append(keys, fmt.Sprintf("e%d%s", id, c.mode))
			}
		}
		if slices.ContainsFunc(keys, func(k string) bool { return taken[k] }) {
			continue
		}
		busy := false
		if c.kind == "movie" {
			b, err := s.busyFor(ctx, "movie", c.titleID, 0, c.Title, c.mode == ModeSecond)
			if err != nil {
				return got, problems, err
			}
			busy = b
		} else {
			for _, id := range c.episodes {
				b, err := s.busyFor(ctx, "series", c.titleID, id, c.Title, c.mode == ModeSecond)
				if err != nil {
					return got, problems, err
				}
				busy = busy || b
			}
		}
		if busy {
			continue
		}
		if _, err := s.grab(ctx, c); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", c.Title, err))
			if err == ErrOff {
				break
			}
			continue
		}
		for _, k := range keys {
			taken[k] = true
		}
		got = append(got, c.Title)
	}
	return got, problems, nil
}
