package search

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/quality"
	"github.com/yeixio/boxoffice/internal/store"
)

// Propers and repacks: when a release turns out flawed (out-of-sync
// sound, a missing scene, the wrong episode), it's put out again as a
// PROPER, REPACK, REAL or v2. Box Office takes the fixed one in place of a
// copy on disk that came from the flawed one, at the copy's own quality:
// it's a fix, not an upgrade, so it doesn't need automatic upgrades on and
// isn't counted against their monthly limit. It's caught by RSS sync,
// which costs no searches, and by searches made anyway; never by searches
// of its own. A title whose automatic upgrades are turned off is left
// alone, and so is anything on Do not touch.

// SettingPropers is "off" to stop grabbing fixed releases; on otherwise.
const SettingPropers = "search_propers"

// ErrFixPaused means a fixed release isn't grabbed by itself because the
// month's data cap has paused upgrades.
var ErrFixPaused = errors.New("the data cap has paused upgrades, and fixed releases with them")

// propersOn says whether Box Office takes fixed releases by itself.
func (s *Service) propersOn(ctx context.Context) bool {
	v, err := s.Store.Setting(ctx, SettingPropers)
	return err == nil && v != "off"
}

// fixCheck says whether releases fix a copy on disk: one named for the
// release it came from (store.ReleaseOf), read once.
type fixCheck struct {
	f       *store.File
	release string
	season  int
	episode int // the copy's episode, for a show; 0 for a movie
}

// fixes makes a fixCheck for copy f, or nil when fixed releases are off
// or there's no copy. For an episode, season and number say which.
func (s *Service) fixes(ctx context.Context, f *store.File, season, number int) *fixCheck {
	if f == nil || !s.propersOn(ctx) {
		return nil
	}
	return &fixCheck{f: f, release: s.Store.ReleaseOf(ctx, *f), season: season, episode: number}
}

// of says whether release r fixes the copy: at the copy's quality (as
// its name gave it, or as the quality check found it), for that one
// episode, posted after the copy came in, and a fix of what it came from
// (quality.FixOf).
func (fc *fixCheck) of(r Release) bool {
	if fc == nil {
		return false
	}
	q := quality.Parse(r.Title).ID
	if q != fc.f.Quality && (fc.f.Guessed == nil || q != *fc.f.Guessed) {
		return false
	}
	if fc.episode > 0 {
		season, eps, ok := releaseEpisode(r.Title)
		if !ok || season != fc.season || len(eps) != 1 || eps[0] != fc.episode {
			return false
		}
	}
	if !r.PublishedAt.IsZero() && !fc.f.AddedAt.IsZero() && r.PublishedAt.Before(fc.f.AddedAt) {
		// The copy came in after it was posted: it's that fix, or the
		// flawed one was picked over it by hand.
		return false
	}
	ok, _ := quality.FixOf(fc.release, r.Title)
	return ok
}

// markFix notes on a candidate that it fixes the copy there, so it's
// grabbed as a fix rather than an upgrade.
func markFix(c *Candidate) {
	c.Fix = quality.RevisionOf(c.Title).Word()
	c.fix = true
}

// fixAllowed is grab's check for a fixed release grabbed by itself: the
// title's automatic upgrades aren't turned off, and the data cap hasn't
// paused upgrades.
func (s *Service) fixAllowed(ctx context.Context, c Candidate) error {
	pref := ""
	switch c.kind {
	case "movie":
		m, err := s.Store.Movie(ctx, c.titleID)
		if err != nil {
			return err
		}
		pref = m.AutoUpgrade
	case "series":
		t, err := s.Store.Series(ctx, c.titleID)
		if err != nil {
			return err
		}
		pref = t.AutoUpgrade
	}
	if pref == "off" {
		return ErrUpgradeOff
	}
	cp, err := downloader.LoadCap(ctx, s.Store)
	if err != nil {
		return err
	}
	if cp.Level != downloader.CapOK {
		return ErrFixPaused
	}
	return nil
}

// takeFixes grabs, from RSS sync's new releases, those that fix a copy
// on disk. Only releases whose name says they're a fix are looked at, so
// the library is only read when one turns up.
func (s *Service) takeFixes(ctx context.Context, releases []Release) ([]string, []string, error) {
	var fixes []Release
	for _, r := range releases {
		if quality.RevisionOf(r.Title).Fix() {
			fixes = append(fixes, r)
		}
	}
	if len(fixes) == 0 || !s.propersOn(ctx) {
		return nil, nil, nil
	}
	ps, err := s.Store.Profiles(ctx)
	if err != nil {
		return nil, nil, err
	}
	profiles := map[int]quality.Profile{}
	for _, p := range ps {
		profiles[p.ID] = p
	}
	var movies []store.Movie
	var shows []store.Series
	episodes := map[int64][]store.Episode{}
	loaded := false
	load := func() error {
		if loaded {
			return nil
		}
		loaded = true
		var err error
		if movies, err = s.Store.Movies(ctx); err != nil {
			return err
		}
		shows, err = s.Store.SeriesAll(ctx)
		return err
	}
	var out Results
	for _, r := range fixes {
		r.Protocol = "usenet"
		if err := load(); err != nil {
			return nil, nil, err
		}
		if season, eps, ok := releaseEpisode(r.Title); ok {
			if len(eps) != 1 {
				continue // a whole season, or several episodes: not taken as a fix
			}
			for _, t := range shows {
				if r.TVDBID > 0 && t.TVDBID > 0 {
					if r.TVDBID != t.TVDBID {
						continue
					}
				} else if !titleMatches(r.Title, t.Title) {
					continue
				}
				if t.AutoUpgrade == "off" {
					break
				}
				list, ok := episodes[t.ID]
				if !ok {
					if list, err = s.Store.Episodes(ctx, t.ID); err != nil {
						return nil, nil, err
					}
					episodes[t.ID] = list
				}
				i := slices.IndexFunc(list, func(e store.Episode) bool { return e.Season == season && e.Number == eps[0] })
				if i < 0 || list[i].File == nil {
					break
				}
				e := list[i]
				if !s.fixes(ctx, e.File, e.Season, e.Number).of(r) {
					break
				}
				blocked, err := s.blocked(ctx, "series", t.ID)
				if err != nil {
					return nil, nil, err
				}
				e.File = nil // judged as if it weren't there: the fix takes its place
				c := s.judgeEpisodes(ctx, r, t, profiles[t.ProfileID], []store.Episode{e}, blocked, false, false)
				if c.Accepted {
					markFix(&c)
					out.Releases = append(out.Releases, c)
				}
				break
			}
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
			if m.File == nil || m.AutoUpgrade == "off" || !s.fixes(ctx, m.File, 0, 0).of(r) {
				break
			}
			if tw, err := s.Store.TitleWant(ctx, "movie", m.ID); err == nil && tw.Edition != "" && !quality.SameEdition(quality.Edition(r.Title), tw.Edition) {
				break
			}
			blocked, err := s.blocked(ctx, "movie", m.ID)
			if err != nil {
				return nil, nil, err
			}
			c := s.judge(ctx, r, profiles[m.ProfileID], false, 0, blocked)
			c.kind, c.titleID = "movie", m.ID
			if c.Accepted {
				markFix(&c)
				out.Releases = append(out.Releases, c)
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
		keys := []string{fmt.Sprintf("m%d", c.titleID)}
		episodeID := int64(0)
		if c.kind == "series" {
			if len(c.episodes) != 1 {
				continue
			}
			episodeID = c.episodes[0]
			keys = []string{fmt.Sprintf("e%d", episodeID)}
		}
		if taken[keys[0]] {
			continue
		}
		busy, err := s.busy(ctx, c.kind, c.titleID, episodeID, c.Title)
		if err != nil {
			return got, problems, err
		}
		if busy {
			continue
		}
		if _, err := s.grab(ctx, c); err != nil {
			if errors.Is(err, ErrUpgradeOff) || errors.Is(err, ErrFixPaused) {
				s.logger().Debug("fixed release not grabbed", "release", c.Title, "why", err)
				if errors.Is(err, ErrFixPaused) {
					problems = append(problems, fmt.Sprintf("%s: %v", c.Title, err))
					break
				}
				continue
			}
			problems = append(problems, fmt.Sprintf("%s: %v", c.Title, err))
			if err == ErrOff {
				break
			}
			continue
		}
		taken[keys[0]] = true
		got = append(got, c.Title)
	}
	return got, problems, nil
}
