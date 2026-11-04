package search

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/jobs"
	"github.com/yeixio/boxoffice/internal/quality"
	"github.com/yeixio/boxoffice/internal/store"
)

// Service searches the indexers for titles on the wanted list.
type Service struct {
	Store  *store.Store
	Engine *downloader.Engine
	Client *Client
	Log    *slog.Logger

	mu    sync.Mutex
	found map[string]cached // releases shown in a search, by key, to grab by hand
}

type cached struct {
	c       Candidate
	expires time.Time
}

// Register runs the search jobs the catalog queues.
func (s *Service) Register(r *jobs.Runner) {
	r.Handle(catalog.JobSearchMovie, s.jobMovie)
	r.Handle(catalog.JobSearchSeries, s.jobSeries)
	r.Handle(catalog.JobSearchEpisode, s.jobEpisode)
}

// Candidate is a release and what Box Office makes of it for a title.
type Candidate struct {
	Release
	Key        string   `json:"key"` // to grab it
	Quality    int      `json:"quality"`
	Accepted   bool     `json:"accepted"`
	Rejections []string `json:"rejections,omitempty"`

	kind     string
	titleID  int64
	episodes []int64
	priority int
}

// Results are a search's releases, best first, and the indexers that
// couldn't be asked.
type Results struct {
	Releases []Candidate `json:"releases"`
	Errors   []string    `json:"errors,omitempty"`
	Searched int         `json:"searched"` // indexers asked
}

// ErrNoIndexers means there's nowhere to search.
var ErrNoIndexers = errors.New("there are no Usenet indexers to search; add one in Settings")

func (s *Service) indexers(ctx context.Context) ([]store.Indexer, []string, error) {
	all, err := s.Store.Indexers(ctx)
	if err != nil {
		return nil, nil, err
	}
	var use []store.Indexer
	var notes []string
	for _, ix := range all {
		switch {
		case !ix.EnableSearch:
		case ix.Protocol != "usenet":
		case ix.APIKey == "" && ix.MissingSecret:
			notes = append(notes, fmt.Sprintf("%s needs its API key; add it in Settings", ix.Name))
		default:
			use = append(use, ix)
		}
	}
	if len(use) == 0 && len(notes) == 0 {
		return nil, nil, ErrNoIndexers
	}
	return use, notes, nil
}

func cats(ix store.Indexer, lo, hi, def int) []int {
	var out []int
	for _, c := range ix.Categories {
		if c >= lo && c <= hi {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		out = []int{def}
	}
	return out
}

// ask asks every indexer at once.
func (s *Service) ask(ctx context.Context, query func(store.Indexer) Query) ([]Release, []string, int, error) {
	list, notes, err := s.indexers(ctx)
	if err != nil {
		return nil, nil, 0, err
	}
	var mu sync.Mutex
	var out []Release
	var wg sync.WaitGroup
	for _, ix := range list {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rs, err := s.Client.Search(ctx, ix, query(ix))
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				notes = append(notes, err.Error())
				return
			}
			for i := range rs {
				rs[i].Protocol = ix.Protocol
			}
			out = append(out, rs...)
		}()
	}
	wg.Wait()
	return out, notes, len(list), nil
}

// ---- movies ----

// Movie searches for a movie and judges each release.
func (s *Service) Movie(ctx context.Context, id int64) (Results, error) {
	m, err := s.Store.Movie(ctx, id)
	if err != nil {
		return Results{}, err
	}
	rs, notes, n, err := s.ask(ctx, func(ix store.Indexer) Query {
		q := Query{Type: "movie", TMDBID: m.TMDBID, Cats: cats(ix, 2000, 2999, 2000)}
		if m.IMDBID != "" {
			q.IMDBID = m.IMDBID
		} else {
			q.Text = fmt.Sprintf("%s %d", m.Title, m.Year)
		}
		return q
	})
	if err != nil {
		return Results{}, err
	}
	p, err := s.profile(ctx, m.ProfileID)
	if err != nil {
		return Results{}, err
	}
	has, have := m.File != nil, 0
	if has {
		have = m.File.Quality
	}
	blocked, err := s.blocked(ctx, "movie", m.ID)
	if err != nil {
		return Results{}, err
	}
	out := Results{Errors: notes, Searched: n}
	for _, r := range rs {
		c := s.judge(ctx, r, p, has, have, blocked)
		c.kind, c.titleID = "movie", m.ID
		if y := releaseYear(r.Title); y > 0 && m.Year > 0 && (y < m.Year-1 || y > m.Year+1) {
			c.reject(fmt.Sprintf("it's from %d, and the movie's from %d", y, m.Year))
		}
		if m.IMDBID == "" && !titleMatches(r.Title, m.Title) {
			c.reject("its name doesn't match the movie's")
		}
		out.Releases = append(out.Releases, c)
	}
	s.rank(ctx, &out)
	return out, nil
}

// ---- episodes ----

// Episode searches for one episode.
func (s *Service) Episode(ctx context.Context, id int64) (Results, error) {
	e, err := s.Store.Episode(ctx, id)
	if err != nil {
		return Results{}, err
	}
	t, err := s.Store.Series(ctx, e.SeriesID)
	if err != nil {
		return Results{}, err
	}
	rs, notes, n, err := s.ask(ctx, func(ix store.Indexer) Query {
		q := Query{Type: "tvsearch", Season: e.Season, Episode: e.Number, Cats: cats(ix, 5000, 5999, 5000)}
		if t.TVDBID > 0 {
			q.TVDBID = t.TVDBID
		} else {
			q.Text = t.Title
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
	has, have := e.File != nil, 0
	if has {
		have = e.File.Quality
	}
	blocked, err := s.blocked(ctx, "series", t.ID)
	if err != nil {
		return Results{}, err
	}
	out := Results{Errors: notes, Searched: n}
	for _, r := range rs {
		c := s.judge(ctx, r, p, has, have, blocked)
		c.kind, c.titleID, c.episodes = "series", t.ID, []int64{e.ID}
		season, eps, ok := releaseEpisode(r.Title)
		switch {
		case !ok:
			c.reject("its name doesn't say which episode it is")
		case season != e.Season:
			c.reject(fmt.Sprintf("it's season %d", season))
		case len(eps) == 0:
			c.reject("it's a whole season; Box Office grabs single episodes for now")
		case !slices.Contains(eps, e.Number):
			c.reject(fmt.Sprintf("it's episode %d", eps[0]))
		}
		if t.TVDBID == 0 && !titleMatches(r.Title, t.Title) {
			c.reject("its name doesn't match the show's")
		}
		out.Releases = append(out.Releases, c)
	}
	s.rank(ctx, &out)
	return out, nil
}

// ---- judging ----

func (c *Candidate) reject(why string) {
	c.Accepted = false
	c.Rejections = append(c.Rejections, why)
}

func (s *Service) profile(ctx context.Context, id int) (quality.Profile, error) {
	ps, err := s.Store.Profiles(ctx)
	if err != nil {
		return quality.Profile{}, err
	}
	for _, p := range ps {
		if p.ID == id {
			return p, nil
		}
	}
	return quality.Profile{}, fmt.Errorf("the title's quality profile is gone")
}

func (s *Service) blocked(ctx context.Context, kind string, titleID int64) (map[string]bool, error) {
	list, err := s.Store.BlockedFor(ctx, kind, titleID)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, b := range list {
		out[strings.ToLower(b.SourceTitle)] = true
	}
	return out, nil
}

func (s *Service) judge(ctx context.Context, r Release, p quality.Profile, has bool, have int, blocked map[string]bool) Candidate {
	q := quality.Parse(r.Title)
	c := Candidate{Release: r, Quality: q.ID, Accepted: true}
	c.Key = key(r)
	if r.Protocol != "usenet" {
		c.reject("torrent downloads aren't supported yet")
	}
	if blocked[strings.ToLower(r.Title)] {
		c.reject("it's on the blocklist")
	}
	if !p.Accepts(q.ID) {
		c.reject(fmt.Sprintf("the %s profile doesn't take %s", p.Name, q.Name))
	} else if !p.Wants(has, have, q.ID) {
		hq, _ := quality.ByID(have)
		c.reject(fmt.Sprintf("there's a %s copy already", hq.Name))
	}
	return c
}

func key(r Release) string {
	h := sha256.Sum256([]byte(strconv.FormatInt(r.IndexerID, 10) + "\x00" + r.GUID))
	return hex.EncodeToString(h[:12])
}

// rank puts the best first: accepted, then quality, the indexer's
// priority, how many grabbed it, and newest.
func (s *Service) rank(ctx context.Context, out *Results) {
	pri := map[int64]int{}
	if list, err := s.Store.Indexers(ctx); err == nil {
		for _, ix := range list {
			pri[ix.ID] = ix.Priority
		}
	}
	// The same release from two indexers is shown once.
	seen := map[string]bool{}
	out.Releases = slices.DeleteFunc(out.Releases, func(c Candidate) bool {
		k := strings.ToLower(c.Title) + "\x00" + strconv.FormatInt(c.Size/(1<<20), 10)
		if seen[k] {
			return true
		}
		seen[k] = true
		return false
	})
	for i := range out.Releases {
		out.Releases[i].priority = pri[out.Releases[i].IndexerID]
	}
	slices.SortStableFunc(out.Releases, func(a, b Candidate) int {
		switch {
		case a.Accepted != b.Accepted:
			if a.Accepted {
				return -1
			}
			return 1
		case a.Quality != b.Quality:
			return quality.Rank(b.Quality) - quality.Rank(a.Quality)
		case a.priority != b.priority:
			return a.priority - b.priority
		case a.Grabs != b.Grabs:
			return b.Grabs - a.Grabs
		}
		return b.PublishedAt.Compare(a.PublishedAt)
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.found == nil {
		s.found = map[string]cached{}
	}
	now := time.Now()
	for k, c := range s.found {
		if now.After(c.expires) {
			delete(s.found, k)
		}
	}
	for _, c := range out.Releases {
		s.found[c.Key] = cached{c, now.Add(time.Hour)}
	}
}

var (
	reYear    = regexp.MustCompile(`(?:^|[ ._(\[-])((?:19|20)\d\d)(?:$|[ ._)\]-])`)
	reEpisode = regexp.MustCompile(`(?i)\bS(\d{1,2})[ ._-]?((?:E\d{1,3}[ ._-]?)*)\b`)
	reEpNum   = regexp.MustCompile(`(?i)E(\d{1,3})`)
	reNonWord = regexp.MustCompile(`[^a-z0-9]+`)
)

func releaseYear(title string) int {
	m := reYear.FindAllStringSubmatch(title, -1)
	if len(m) == 0 {
		return 0
	}
	// The last year-like number is the year; an earlier one can be part of
	// the title (2001 A Space Odyssey 1968).
	y, _ := strconv.Atoi(m[len(m)-1][1])
	return y
}

// releaseEpisode reads S01E02 (or S01E02E03, or S01 for a season).
func releaseEpisode(title string) (int, []int, bool) {
	m := reEpisode.FindStringSubmatch(title)
	if m == nil {
		return 0, nil, false
	}
	season, _ := strconv.Atoi(m[1])
	var eps []int
	for _, e := range reEpNum.FindAllStringSubmatch(m[2], -1) {
		n, _ := strconv.Atoi(e[1])
		eps = append(eps, n)
	}
	if len(eps) == 2 && eps[1] > eps[0] {
		for n := eps[0] + 1; n < eps[1]; n++ {
			eps = append(eps, n)
		}
	}
	return season, eps, true
}

func normalize(s string) string {
	s = strings.ToLower(strings.ReplaceAll(s, "&", "and"))
	return strings.TrimSpace(reNonWord.ReplaceAllString(s, " "))
}

func titleMatches(release, title string) bool {
	return strings.HasPrefix(normalize(release), normalize(title)+" ") || normalize(release) == normalize(title)
}

// ---- grabbing ----

// ErrOff means downloading is off.
var ErrOff = errors.New("downloading is off; turn it on in Settings, under Downloads")

// Grab downloads a release shown in a recent search.
func (s *Service) Grab(ctx context.Context, key string) (store.Download, error) {
	s.mu.Lock()
	c, ok := s.found[key]
	s.mu.Unlock()
	if !ok || time.Now().After(c.expires) {
		return store.Download{}, errors.New("that search is out of date; search again")
	}
	return s.grab(ctx, c.c)
}

func (s *Service) grab(ctx context.Context, c Candidate) (store.Download, error) {
	set, err := downloader.LoadSettings(ctx, s.Store)
	if err != nil {
		return store.Download{}, err
	}
	if !set.Enabled {
		return store.Download{}, ErrOff
	}
	if c.Protocol != "usenet" {
		return store.Download{}, errors.New("torrent downloads aren't supported yet")
	}
	ix, err := s.Store.Indexer(ctx, c.IndexerID)
	if err != nil {
		return store.Download{}, errors.New("the indexer that found it is gone")
	}
	data, err := s.Client.FetchNZB(ctx, ix, c.Release)
	if err != nil {
		return store.Download{}, err
	}
	return s.Engine.Add(ctx, store.Download{Name: c.Title, Kind: c.kind, TitleID: c.titleID, EpisodeIDs: c.episodes, Quality: c.Quality,
		Indexer: c.Indexer, Source: "search", Password: c.Password}, data)
}

// ---- the jobs ----

func payloadID(p string) (int64, error) {
	var v struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(p), &v); err != nil || v.ID == 0 {
		return 0, errors.New("the job doesn't say which title")
	}
	return v.ID, nil
}

// pick grabs the best release of a search, if there's one to take.
func (s *Service) pick(ctx context.Context, res Results, kind string, titleID int64) (jobs.Result, error) {
	note := ""
	if len(res.Errors) > 0 {
		note = " (" + strings.Join(res.Errors, "; ") + ")"
	}
	if res.Searched == 0 {
		return jobs.Skipped("no indexer could be searched%s", note), nil
	}
	i := slices.IndexFunc(res.Releases, func(c Candidate) bool { return c.Accepted })
	if i < 0 {
		if len(res.Releases) == 0 {
			return jobs.Done("found nothing on %d %s%s", res.Searched, plural(res.Searched, "indexer", "indexers"), note), nil
		}
		return jobs.Done("found %d %s, none of them right%s", len(res.Releases), plural(len(res.Releases), "release", "releases"), note), nil
	}
	best := res.Releases[i]
	if busy, err := s.Store.GrabbedRecently(ctx, kind, titleID, best.Title); err != nil {
		return jobs.Result{}, err
	} else if busy {
		return jobs.Skipped("it's downloading already"), nil
	}
	q, _ := quality.ByID(best.Quality)
	if _, err := s.grab(ctx, best); errors.Is(err, ErrOff) {
		return jobs.Skipped("found %s (%s) on %s, but downloading is off, so it wasn't grabbed", best.Title, q.Name, best.Indexer), nil
	} else if err != nil {
		return jobs.Result{}, err
	}
	return jobs.Done("grabbed %s (%s) from %s%s", best.Title, q.Name, best.Indexer, note), nil
}

func (s *Service) jobMovie(ctx context.Context, payload string) (jobs.Result, error) {
	id, err := payloadID(payload)
	if err != nil {
		return jobs.Result{}, err
	}
	res, err := s.Movie(ctx, id)
	if errors.Is(err, ErrNoIndexers) {
		return jobs.Skipped("%s", err.Error()), nil
	}
	if err != nil {
		return jobs.Result{}, err
	}
	return s.pick(ctx, res, "movie", id)
}

func (s *Service) jobEpisode(ctx context.Context, payload string) (jobs.Result, error) {
	id, err := payloadID(payload)
	if err != nil {
		return jobs.Result{}, err
	}
	e, err := s.Store.Episode(ctx, id)
	if err != nil {
		return jobs.Result{}, err
	}
	res, err := s.Episode(ctx, id)
	if errors.Is(err, ErrNoIndexers) {
		return jobs.Skipped("%s", err.Error()), nil
	}
	if err != nil {
		return jobs.Result{}, err
	}
	return s.pick(ctx, res, "series", e.SeriesID)
}

// maxEpisodes caps how many episodes one show search looks for, to be kind
// to the indexers' daily limits.
const maxEpisodes = 25

// jobSeries searches for a show's missing episodes: monitored, aired, and
// with no file.
func (s *Service) jobSeries(ctx context.Context, payload string) (jobs.Result, error) {
	id, err := payloadID(payload)
	if err != nil {
		return jobs.Result{}, err
	}
	t, err := s.Store.Series(ctx, id)
	if err != nil {
		return jobs.Result{}, err
	}
	eps, err := s.Store.Episodes(ctx, id)
	if err != nil {
		return jobs.Result{}, err
	}
	seasons := map[int]bool{}
	for _, se := range t.Seasons {
		seasons[se.Number] = se.Monitored
	}
	now := time.Now()
	var want []store.Episode
	for _, e := range eps {
		if e.Monitored && seasons[e.Season] && e.Season > 0 && e.File == nil && e.AirDate != nil && e.AirDate.Before(now) {
			want = append(want, e)
		}
	}
	if len(want) == 0 {
		return jobs.Done("no episodes are missing"), nil
	}
	more := len(want) > maxEpisodes
	want = want[:min(len(want), maxEpisodes)]
	grabbed, found := 0, 0
	var problems []string
	off := false
	for _, e := range want {
		if ctx.Err() != nil {
			return jobs.Result{}, ctx.Err()
		}
		res, err := s.Episode(ctx, e.ID)
		if errors.Is(err, ErrNoIndexers) {
			return jobs.Skipped("%s", err.Error()), nil
		}
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		i := slices.IndexFunc(res.Releases, func(c Candidate) bool { return c.Accepted })
		if i < 0 {
			continue
		}
		found++
		if _, err := s.grab(ctx, res.Releases[i]); errors.Is(err, ErrOff) {
			off = true
		} else if err != nil {
			problems = append(problems, err.Error())
		} else {
			grabbed++
		}
	}
	msg := fmt.Sprintf("looked for %d missing %s; found %d", len(want), plural(len(want), "episode", "episodes"), found)
	if off && found > 0 {
		msg += ", but downloading is off, so none were grabbed"
	} else {
		msg += fmt.Sprintf(", grabbed %d", grabbed)
	}
	if more {
		msg += fmt.Sprintf(" (the first %d; search again for the rest)", maxEpisodes)
	}
	if len(problems) > 0 {
		slices.Sort(problems)
		msg += " (" + strings.Join(slices.Compact(problems), "; ") + ")"
	}
	return jobs.Done("%s", msg), nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
