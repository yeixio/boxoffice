// Package catalog is Box Office's wanted list: adding movies and shows from
// TMDB, keeping their details and episodes current, and queuing searches.
// Both the native API and the Radarr- and Sonarr-compatible API go through
// it, so they behave the same.
package catalog

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/yeixio/boxoffice/internal/jobs"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/tmdb"
)

// SettingTMDBKey is where an admin's TMDB key is kept.
const SettingTMDBKey = "tmdb_key"

// Job kinds.
const (
	JobRefreshMovie  = "refresh-movie"
	JobRefreshSeries = "refresh-series"
	JobRefreshAll    = "refresh-all"
	JobSearchMovie   = "search-movie"
	JobSearchSeries  = "search-series"
	JobSearchEpisode = "search-episodes"
)

// Service is the wanted list.
type Service struct {
	Store *store.Store
	Jobs  *jobs.Runner
	Log   *slog.Logger
	// EnvTMDBKey is the key set on the server, used when none is saved.
	EnvTMDBKey string
	// TMDBBase and TMDBHTTP point at a test server.
	TMDBBase string
	TMDBHTTP *http.Client
}

// Register adds the service's job handlers to its runner. Searches are
// run by the search package.
func (s *Service) Register() {
	s.Jobs.Handle(JobRefreshMovie, s.jobRefreshMovie)
	s.Jobs.Handle(JobRefreshSeries, s.jobRefreshSeries)
	s.Jobs.Handle(JobRefreshAll, s.jobRefreshAll)
}

// TMDB is a client with the current key.
func (s *Service) TMDB(ctx context.Context) *tmdb.Client {
	key, _ := s.Store.Setting(ctx, SettingTMDBKey)
	c := tmdb.New(cmp.Or(key, s.EnvTMDBKey))
	if s.TMDBBase != "" {
		c.Base = s.TMDBBase
	}
	if s.TMDBHTTP != nil {
		c.HTTP = s.TMDBHTTP
	}
	return c
}

// Error is a mistake in what was asked, fit to show whoever asked.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

func badf(format string, a ...any) error { return &Error{fmt.Sprintf(format, a...)} }

// FolderName is a title's folder: "Title (Year)", without characters that
// some filesystems refuse.
func FolderName(title string, year int) string {
	name := strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return -1
		}
		if r < 32 {
			return -1
		}
		return r
	}, title)
	name = strings.Join(strings.Fields(name), " ")
	name = strings.TrimRight(name, ". ")
	if name == "" {
		name = "Untitled"
	}
	if year > 0 {
		name = fmt.Sprintf("%s (%d)", name, year)
	}
	return name
}

// AddOptions say how a title is added.
type AddOptions struct {
	ProfileID  int    `json:"profileId"`
	RootFolder string `json:"rootFolder"`
	// Path is the title's own folder; empty puts it in RootFolder by name.
	Path      string `json:"path,omitempty"`
	Monitored bool   `json:"monitored"`
	// Search starts looking for it once added.
	Search bool `json:"search"`
	// Monitor picks a show's episodes: all, future (not yet aired), none,
	// or latest (the newest season). Specials are never monitored.
	Monitor string `json:"monitor,omitempty"`
}

// resolve fills in a profile and root folder, and checks the ones given.
func (s *Service) resolve(ctx context.Context, kind string, o *AddOptions) error {
	if o.ProfileID == 0 {
		ps, err := s.Store.Profiles(ctx)
		if err != nil {
			return err
		}
		if len(ps) == 0 {
			return badf("add a quality profile first")
		}
		o.ProfileID = ps[0].ID
	} else if _, err := s.Store.Profile(ctx, o.ProfileID); errors.Is(err, store.ErrNotFound) {
		return badf("there's no quality profile %d", o.ProfileID)
	} else if err != nil {
		return err
	}
	folders, err := s.Store.RootFolders(ctx, kind)
	if err != nil {
		return err
	}
	if o.RootFolder == "" {
		if len(folders) == 0 {
			return badf("add a root folder for %s under Settings first", plural(kind))
		}
		o.RootFolder = folders[0].Path
		return nil
	}
	o.RootFolder = filepath.Clean(o.RootFolder)
	for _, f := range folders {
		if f.Path == o.RootFolder {
			return nil
		}
	}
	return badf("%s isn't one of the root folders for %s", o.RootFolder, plural(kind))
}

func plural(kind string) string {
	if kind == "movie" {
		return "movies"
	}
	return "shows"
}

// ---- movies ----

// LookupMovie is a movie found online, with its id here if it's added.
type LookupMovie struct {
	tmdb.Movie
	ID int64 `json:"id,omitempty"`
}

// LookupMovies searches TMDB.
func (s *Service) LookupMovies(ctx context.Context, term string) ([]LookupMovie, error) {
	found, err := s.TMDB(ctx).SearchMovies(ctx, term)
	if err != nil {
		return nil, err
	}
	out := make([]LookupMovie, 0, len(found))
	for _, m := range found {
		l := LookupMovie{Movie: m}
		if have, err := s.Store.MovieByTMDB(ctx, m.TMDBID); err == nil {
			l.ID = have.ID
		}
		out = append(out, l)
	}
	return out, nil
}

// ErrExists means the title is already on the wanted list.
var ErrExists = errors.New("already added")

func fromTMDBMovie(m tmdb.Movie) store.Movie {
	return store.Movie{
		TMDBID: m.TMDBID, IMDBID: m.IMDBID, Title: m.Title, Year: m.Year, Overview: m.Overview, Poster: m.Poster, Backdrop: m.Backdrop,
		Runtime: m.Runtime, Status: m.Status, InCinemas: m.InCinemas, DigitalRelease: m.DigitalRelease, PhysicalRelease: m.PhysicalRelease,
		Rating: m.Rating,
	}
}

// AddMovie adds a movie by its TMDB id. One that's there already comes
// back with ErrExists.
func (s *Service) AddMovie(ctx context.Context, tmdbID int64, o AddOptions) (store.Movie, error) {
	if have, err := s.Store.MovieByTMDB(ctx, tmdbID); err == nil {
		return have, ErrExists
	}
	if err := s.resolve(ctx, "movie", &o); err != nil {
		return store.Movie{}, err
	}
	info, err := s.TMDB(ctx).Movie(ctx, tmdbID)
	if errors.Is(err, tmdb.ErrNotFound) {
		return store.Movie{}, badf("TMDB has no movie %d", tmdbID)
	}
	if err != nil {
		return store.Movie{}, err
	}
	return s.AddMovieInfo(ctx, info, o)
}

// AddMovieInfo adds a movie already looked up.
func (s *Service) AddMovieInfo(ctx context.Context, info tmdb.Movie, o AddOptions) (store.Movie, error) {
	if err := s.resolve(ctx, "movie", &o); err != nil {
		return store.Movie{}, err
	}
	m := fromTMDBMovie(info)
	m.Monitored, m.ProfileID, m.RootFolder = o.Monitored, o.ProfileID, o.RootFolder
	m.Path = cmp.Or(o.Path, filepath.Join(o.RootFolder, FolderName(m.Title, m.Year)))
	added, err := s.Store.AddMovie(ctx, m)
	if errors.Is(err, store.ErrExists) {
		have, _ := s.Store.MovieByTMDB(ctx, info.TMDBID)
		return have, ErrExists
	}
	if err != nil {
		return added, err
	}
	s.Log.Info("movie added", "title", added.Title, "year", added.Year)
	if o.Search && added.Monitored {
		s.SearchMovies(ctx, added.ID)
	}
	return added, nil
}

// RefreshMovie updates a movie's details from TMDB.
func (s *Service) RefreshMovie(ctx context.Context, id int64) error {
	m, err := s.Store.Movie(ctx, id)
	if err != nil {
		return err
	}
	info, err := s.TMDB(ctx).Movie(ctx, m.TMDBID)
	if err != nil {
		return err
	}
	n := fromTMDBMovie(info)
	n.ID = m.ID
	// A rating brought from Radarr stays until TMDB has one.
	n.Rating = cmp.Or(n.Rating, m.Rating)
	return s.Store.RefreshMovie(ctx, n)
}

// SearchMovies queues a search for each movie.
func (s *Service) SearchMovies(ctx context.Context, ids ...int64) error {
	for _, id := range ids {
		m, err := s.Store.Movie(ctx, id)
		if err != nil {
			return err
		}
		if _, err := s.Jobs.Queue(ctx, JobSearchMovie, FolderName(m.Title, m.Year), idPayload(id)); err != nil {
			return err
		}
	}
	return nil
}

// ---- shows ----

// LookupShow is a show found online, with its id here if it's added.
type LookupShow struct {
	tmdb.Show
	ID int64 `json:"id,omitempty"`
}

func (s *Service) LookupShows(ctx context.Context, term string) ([]LookupShow, error) {
	found, err := s.TMDB(ctx).SearchShows(ctx, term)
	if err != nil {
		return nil, err
	}
	out := make([]LookupShow, 0, len(found))
	for _, t := range found {
		l := LookupShow{Show: t}
		if have, err := s.Store.SeriesByTMDB(ctx, t.TMDBID); err == nil {
			l.ID = have.ID
		}
		out = append(out, l)
	}
	return out, nil
}

func fromTMDBShow(t tmdb.Show) store.Series {
	return store.Series{
		TMDBID: t.TMDBID, TVDBID: t.TVDBID, IMDBID: t.IMDBID, Title: t.Title, Year: t.Year, Overview: t.Overview,
		Poster: t.Poster, Backdrop: t.Backdrop, Status: t.Status, Network: t.Network, Runtime: t.Runtime,
		Rating: t.Rating,
	}
}

// AddSeries adds a show by its TMDB id, with every season and episode.
func (s *Service) AddSeries(ctx context.Context, tmdbID int64, o AddOptions) (store.Series, error) {
	if have, err := s.Store.SeriesByTMDB(ctx, tmdbID); err == nil {
		return have, ErrExists
	}
	if err := s.resolve(ctx, "series", &o); err != nil {
		return store.Series{}, err
	}
	info, err := s.TMDB(ctx).Show(ctx, tmdbID)
	if errors.Is(err, tmdb.ErrNotFound) {
		return store.Series{}, badf("TMDB has no show %d", tmdbID)
	}
	if err != nil {
		return store.Series{}, err
	}
	return s.AddSeriesInfo(ctx, info, o, nil)
}

// AddSeriesInfo adds a show already looked up. seasons, when given, says
// which seasons are monitored (by number) instead of o.Monitor.
func (s *Service) AddSeriesInfo(ctx context.Context, info tmdb.Show, o AddOptions, seasons map[int]bool) (store.Series, error) {
	if err := s.resolve(ctx, "series", &o); err != nil {
		return store.Series{}, err
	}
	t := fromTMDBShow(info)
	t.Monitored, t.ProfileID, t.RootFolder = o.Monitored, o.ProfileID, o.RootFolder
	t.Path = cmp.Or(o.Path, filepath.Join(o.RootFolder, FolderName(t.Title, t.Year)))
	latest := 0
	for _, se := range info.Seasons {
		latest = max(latest, se.Number)
	}
	for _, se := range info.Seasons {
		mon := se.Number > 0 && (o.Monitor == "" || o.Monitor == "all" || o.Monitor == "future" || (o.Monitor == "latest" && se.Number == latest))
		if seasons != nil {
			mon = seasons[se.Number]
		}
		t.Seasons = append(t.Seasons, store.Season{Number: se.Number, Monitored: mon})
	}
	added, err := s.Store.AddSeries(ctx, t)
	if errors.Is(err, store.ErrExists) {
		have, _ := s.Store.SeriesByTMDB(ctx, info.TMDBID)
		return have, ErrExists
	}
	if err != nil {
		return added, err
	}
	if err := s.syncEpisodes(ctx, added, info, o.Monitor); err != nil {
		s.Log.Warn("couldn't fetch a show's episodes; they'll come with the next refresh", "title", added.Title, "err", err)
	}
	s.Log.Info("show added", "title", added.Title, "year", added.Year)
	if o.Search && added.Monitored {
		s.SearchSeries(ctx, added.ID)
	}
	return s.Store.Series(ctx, added.ID)
}

// syncEpisodes brings a show's episodes in line with TMDB. New episodes
// are monitored unless monitor says otherwise: "future" leaves out those
// that already aired, "none" leaves out all.
func (s *Service) syncEpisodes(ctx context.Context, t store.Series, info tmdb.Show, monitor string) error {
	c := s.TMDB(ctx)
	now := time.Now()
	for _, se := range info.Seasons {
		if err := s.Store.EnsureSeason(ctx, t.ID, store.Season{Number: se.Number, Monitored: se.Number > 0 && t.Monitored && monitor != "none"}); err != nil {
			return err
		}
		eps, err := c.Season(ctx, info.TMDBID, se.Number)
		if errors.Is(err, tmdb.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		for _, e := range eps {
			mon := monitor != "none" && !(monitor == "future" && e.AirDate != nil && e.AirDate.Before(now))
			if err := s.Store.UpsertEpisode(ctx, store.Episode{SeriesID: t.ID, Season: e.Season, Number: e.Number, Title: e.Title,
				Overview: e.Overview, AirDate: e.AirDate, Monitored: mon}); err != nil {
				return err
			}
		}
	}
	return nil
}

// RefreshSeries updates a show's details, seasons and episodes from TMDB.
func (s *Service) RefreshSeries(ctx context.Context, id int64) error {
	t, err := s.Store.Series(ctx, id)
	if err != nil {
		return err
	}
	info, err := s.TMDB(ctx).Show(ctx, t.TMDBID)
	if err != nil {
		return err
	}
	n := fromTMDBShow(info)
	n.ID = t.ID
	n.Rating = cmp.Or(n.Rating, t.Rating)
	if err := s.Store.RefreshSeries(ctx, n); err != nil {
		return err
	}
	return s.syncEpisodes(ctx, t, info, "")
}

// SearchSeries queues a search for each show's missing episodes.
func (s *Service) SearchSeries(ctx context.Context, ids ...int64) error {
	for _, id := range ids {
		t, err := s.Store.Series(ctx, id)
		if err != nil {
			return err
		}
		if _, err := s.Jobs.Queue(ctx, JobSearchSeries, t.Title, idPayload(id)); err != nil {
			return err
		}
	}
	return nil
}

// SearchEpisodes queues a search for some episodes.
func (s *Service) SearchEpisodes(ctx context.Context, ids ...int64) error {
	for _, id := range ids {
		e, err := s.Store.Episode(ctx, id)
		if err != nil {
			return err
		}
		t, err := s.Store.Series(ctx, e.SeriesID)
		if err != nil {
			return err
		}
		subject := fmt.Sprintf("%s S%02dE%02d", t.Title, e.Season, e.Number)
		if _, err := s.Jobs.Queue(ctx, JobSearchEpisode, subject, idPayload(id)); err != nil {
			return err
		}
	}
	return nil
}

// ---- calendar ----

// Upcoming is a movie release or an episode airing.
type Upcoming struct {
	Kind     string    `json:"kind"`    // movie or episode
	Release  string    `json:"release"` // cinemas, digital, physical or airs
	Date     time.Time `json:"date"`
	Title    string    `json:"title"`
	Year     int       `json:"year,omitempty"`
	MovieID  int64     `json:"movieId,omitempty"`
	SeriesID int64     `json:"seriesId,omitempty"`
	Show     string    `json:"show,omitempty"`
	Season   int       `json:"season,omitempty"`
	Episode  int       `json:"episode,omitempty"`
	Poster   string    `json:"poster,omitempty"`
	HasFile  bool      `json:"hasFile"`
}

// Calendar lists what's due in [from, to), soonest first.
func (s *Service) Calendar(ctx context.Context, from, to time.Time) ([]Upcoming, error) {
	out := []Upcoming{}
	movies, err := s.Store.MoviesReleasing(ctx, from, to)
	if err != nil {
		return nil, err
	}
	in := func(t *time.Time) bool { return t != nil && !t.Before(from) && t.Before(to) }
	for _, m := range movies {
		for _, r := range []struct {
			at   *time.Time
			kind string
		}{{m.InCinemas, "cinemas"}, {m.DigitalRelease, "digital"}, {m.PhysicalRelease, "physical"}} {
			if in(r.at) {
				out = append(out, Upcoming{Kind: "movie", Release: r.kind, Date: *r.at, Title: m.Title, Year: m.Year, MovieID: m.ID, Poster: m.Poster, HasFile: m.File != nil})
			}
		}
	}
	eps, err := s.Store.EpisodesAiring(ctx, from, to)
	if err != nil {
		return nil, err
	}
	shows := map[int64]store.Series{}
	for _, e := range eps {
		t, ok := shows[e.SeriesID]
		if !ok {
			if t, err = s.Store.Series(ctx, e.SeriesID); err != nil {
				return nil, err
			}
			shows[e.SeriesID] = t
		}
		out = append(out, Upcoming{Kind: "episode", Release: "airs", Date: *e.AirDate, Title: e.Title, SeriesID: t.ID, Show: t.Title,
			Season: e.Season, Episode: e.Number, Poster: t.Poster, HasFile: e.File != nil})
	}
	sortUpcoming(out)
	return out, nil
}

// ---- jobs ----

func idPayload(id int64) string {
	b, _ := json.Marshal(map[string]int64{"id": id})
	return string(b)
}

func payloadID(p string) (int64, error) {
	var v struct{ ID int64 }
	if err := json.Unmarshal([]byte(p), &v); err != nil || v.ID == 0 {
		return 0, fmt.Errorf("bad job payload %q", p)
	}
	return v.ID, nil
}

func (s *Service) jobRefreshMovie(ctx context.Context, p string) (jobs.Result, error) {
	id, err := payloadID(p)
	if err != nil {
		return jobs.Result{}, err
	}
	if err := s.RefreshMovie(ctx, id); err != nil {
		return jobs.Result{}, err
	}
	return jobs.Done("details updated from TMDB"), nil
}

func (s *Service) jobRefreshSeries(ctx context.Context, p string) (jobs.Result, error) {
	id, err := payloadID(p)
	if err != nil {
		return jobs.Result{}, err
	}
	if err := s.RefreshSeries(ctx, id); err != nil {
		return jobs.Result{}, err
	}
	return jobs.Done("details and episodes updated from TMDB"), nil
}

// jobRefreshAll refreshes every title whose details are a day old.
func (s *Service) jobRefreshAll(ctx context.Context, _ string) (jobs.Result, error) {
	if s.TMDB(ctx).Key == "" {
		return jobs.Skipped("no TMDB key yet"), nil
	}
	stale := time.Now().Add(-20 * time.Hour)
	movies, err := s.Store.Movies(ctx)
	if err != nil {
		return jobs.Result{}, err
	}
	n, failed := 0, 0
	for _, m := range movies {
		if m.RefreshedAt.After(stale) {
			continue
		}
		if err := s.RefreshMovie(ctx, m.ID); err != nil {
			failed++
			s.Log.Warn("couldn't refresh a movie", "title", m.Title, "err", err)
			continue
		}
		n++
	}
	shows, err := s.Store.SeriesAll(ctx)
	if err != nil {
		return jobs.Result{}, err
	}
	for _, t := range shows {
		if t.RefreshedAt.After(stale) {
			continue
		}
		if err := s.RefreshSeries(ctx, t.ID); err != nil {
			failed++
			s.Log.Warn("couldn't refresh a show", "title", t.Title, "err", err)
			continue
		}
		n++
	}
	if failed > 0 {
		return jobs.Done("refreshed %d titles; %d couldn't be refreshed", n, failed), nil
	}
	return jobs.Done("refreshed %d titles", n), nil
}

// RefreshEvery queues a refresh of stale titles now and then every interval.
func (s *Service) RefreshEvery(ctx context.Context, every time.Duration) {
	for {
		s.Jobs.Queue(ctx, JobRefreshAll, "All titles", "{}")
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

func sortUpcoming(u []Upcoming) {
	slices.SortStableFunc(u, func(a, b Upcoming) int {
		if c := a.Date.Compare(b.Date); c != 0 {
			return c
		}
		return cmp.Compare(a.Title, b.Title)
	})
}
