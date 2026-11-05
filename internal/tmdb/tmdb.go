// Package tmdb looks movies and shows up on The Movie Database, which is
// where Box Office gets titles, release dates and episode lists.
package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrNoKey means no TMDB key is set.
var ErrNoKey = errors.New("add a TMDB API key under Settings to look titles up")

// ErrNotFound means TMDB doesn't know the title.
var ErrNotFound = errors.New("not found on TMDB")

const imageBase = "https://image.tmdb.org/t/p/"

// Client calls TMDB's v3 API.
type Client struct {
	// Key is an API key or a read access token; either works.
	Key  string
	Base string // https://api.themoviedb.org/3, or a test server
	HTTP *http.Client
}

func New(key string) *Client {
	return &Client{Key: strings.TrimSpace(key), Base: "https://api.themoviedb.org/3", HTTP: &http.Client{Timeout: 15 * time.Second}}
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	if c == nil || c.Key == "" {
		return ErrNoKey
	}
	if q == nil {
		q = url.Values{}
	}
	req, err := http.NewRequestWithContext(ctx, "GET", "", nil)
	if err != nil {
		return err
	}
	// A read access token is a JWT; a v3 key is 32 hex characters.
	if strings.HasPrefix(c.Key, "eyJ") {
		req.Header.Set("Authorization", "Bearer "+c.Key)
	} else {
		q.Set("api_key", c.Key)
	}
	u, err := url.Parse(c.Base + path + "?" + q.Encode())
	if err != nil {
		return err
	}
	req.URL = u
	req.Header.Set("Accept", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // keeps the key out of the message
		}
		return fmt.Errorf("couldn't reach TMDB: %w", err)
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusUnauthorized:
		return errors.New("TMDB didn't accept the API key")
	case res.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case res.StatusCode >= 300:
		return fmt.Errorf("TMDB answered HTTP %d", res.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(out)
}

// Check tries the key.
func (c *Client) Check(ctx context.Context) error {
	var v struct{}
	return c.get(ctx, "/configuration", nil, &v)
}

// Image is the address of a TMDB image at a size, or "" for none.
func Image(path, size string) string {
	if path == "" {
		return ""
	}
	return imageBase + size + path
}

func year(date string) int {
	if len(date) < 4 {
		return 0
	}
	y, _ := strconv.Atoi(date[:4])
	return y
}

func parseDate(s string) *time.Time {
	if s == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			t = t.UTC()
			return &t
		}
	}
	return nil
}

// Movie is a movie as TMDB describes it.
type Movie struct {
	TMDBID          int64      `json:"tmdbId"`
	IMDBID          string     `json:"imdbId,omitempty"`
	Title           string     `json:"title"`
	Year            int        `json:"year,omitempty"`
	Overview        string     `json:"overview,omitempty"`
	Poster          string     `json:"poster,omitempty"`
	Backdrop        string     `json:"backdrop,omitempty"`
	Runtime         int        `json:"runtime,omitempty"`
	Status          string     `json:"status,omitempty"`
	InCinemas       *time.Time `json:"inCinemas,omitempty"`
	DigitalRelease  *time.Time `json:"digitalRelease,omitempty"`
	PhysicalRelease *time.Time `json:"physicalRelease,omitempty"`
	// Rating is TMDB's average vote, out of 10; 0 when nobody's voted.
	Rating float64 `json:"rating,omitempty"`
}

type apiMovie struct {
	ID           int64   `json:"id"`
	IMDBID       string  `json:"imdb_id"`
	Title        string  `json:"title"`
	ReleaseDate  string  `json:"release_date"`
	Overview     string  `json:"overview"`
	PosterPath   string  `json:"poster_path"`
	BackdropPath string  `json:"backdrop_path"`
	Runtime      int     `json:"runtime"`
	Status       string  `json:"status"`
	Popularity   float64 `json:"popularity"`
	VoteAverage  float64 `json:"vote_average"`
	ReleaseDates struct {
		Results []struct {
			Country string `json:"iso_3166_1"`
			Dates   []struct {
				Date string `json:"release_date"`
				Type int    `json:"type"`
			} `json:"release_dates"`
		} `json:"results"`
	} `json:"release_dates"`
}

func (m apiMovie) movie(country string) Movie {
	out := Movie{
		TMDBID: m.ID, IMDBID: m.IMDBID, Title: m.Title, Year: year(m.ReleaseDate), Overview: m.Overview,
		Poster: Image(m.PosterPath, "w500"), Backdrop: Image(m.BackdropPath, "w1280"), Runtime: m.Runtime, Status: m.Status,
		Rating: m.VoteAverage,
	}
	// Release types: 1 premiere, 2 limited, 3 theatrical, 4 digital, 5 physical, 6 TV.
	// The home country's dates win; elsewhere, the earliest of each type.
	pick := func(types ...int) *time.Time {
		var home, first *time.Time
		for _, r := range m.ReleaseDates.Results {
			for _, d := range r.Dates {
				for _, t := range types {
					if d.Type != t {
						continue
					}
					at := parseDate(d.Date)
					if at == nil {
						continue
					}
					if strings.EqualFold(r.Country, country) && (home == nil || at.Before(*home)) {
						home = at
					}
					if first == nil || at.Before(*first) {
						first = at
					}
				}
			}
		}
		if home != nil {
			return home
		}
		return first
	}
	out.InCinemas = pick(3, 2)
	out.DigitalRelease = pick(4)
	out.PhysicalRelease = pick(5)
	if out.InCinemas == nil {
		out.InCinemas = parseDate(m.ReleaseDate)
	}
	return out
}

// Country is whose release dates come first, such as "US".
var Country = "US"

// SearchMovies finds movies by name. "tmdb:603" finds one by its id.
func (c *Client) SearchMovies(ctx context.Context, term string) ([]Movie, error) {
	if id, ok := idTerm(term, "tmdb"); ok {
		m, err := c.Movie(ctx, id)
		if errors.Is(err, ErrNotFound) {
			return []Movie{}, nil
		}
		return []Movie{m}, err
	}
	q := url.Values{"query": {term}, "include_adult": {"false"}}
	if t, y, ok := splitYear(term); ok {
		q.Set("query", t)
		q.Set("year", strconv.Itoa(y))
	}
	var res struct {
		Results []apiMovie `json:"results"`
	}
	if err := c.get(ctx, "/search/movie", q, &res); err != nil {
		return nil, err
	}
	out := make([]Movie, 0, len(res.Results))
	for _, m := range res.Results {
		out = append(out, m.movie(Country))
	}
	return out, nil
}

// Movie describes one movie, with its release dates.
func (c *Client) Movie(ctx context.Context, id int64) (Movie, error) {
	var m apiMovie
	if err := c.get(ctx, "/movie/"+strconv.FormatInt(id, 10), url.Values{"append_to_response": {"release_dates"}}, &m); err != nil {
		return Movie{}, err
	}
	return m.movie(Country), nil
}

// Show is a TV show as TMDB describes it.
type Show struct {
	TMDBID   int64        `json:"tmdbId"`
	TVDBID   int64        `json:"tvdbId,omitempty"`
	IMDBID   string       `json:"imdbId,omitempty"`
	Title    string       `json:"title"`
	Year     int          `json:"year,omitempty"`
	Overview string       `json:"overview,omitempty"`
	Poster   string       `json:"poster,omitempty"`
	Backdrop string       `json:"backdrop,omitempty"`
	Status   string       `json:"status,omitempty"`
	Network  string       `json:"network,omitempty"`
	Runtime  int          `json:"runtime,omitempty"`
	Rating   float64      `json:"rating,omitempty"`
	Seasons  []SeasonInfo `json:"seasons,omitempty"`
}

// SeasonInfo is a season in a show's description.
type SeasonInfo struct {
	Number   int `json:"number"`
	Episodes int `json:"episodes"`
}

type apiShow struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	FirstAirDate string  `json:"first_air_date"`
	Overview     string  `json:"overview"`
	PosterPath   string  `json:"poster_path"`
	BackdropPath string  `json:"backdrop_path"`
	Status       string  `json:"status"`
	RunTimes     []int   `json:"episode_run_time"`
	VoteAverage  float64 `json:"vote_average"`
	Networks     []struct {
		Name string `json:"name"`
	} `json:"networks"`
	Seasons []struct {
		Number   int `json:"season_number"`
		Episodes int `json:"episode_count"`
	} `json:"seasons"`
	ExternalIDs struct {
		TVDBID int64  `json:"tvdb_id"`
		IMDBID string `json:"imdb_id"`
	} `json:"external_ids"`
}

func (s apiShow) show() Show {
	out := Show{
		TMDBID: s.ID, TVDBID: s.ExternalIDs.TVDBID, IMDBID: s.ExternalIDs.IMDBID, Title: s.Name, Year: year(s.FirstAirDate),
		Overview: s.Overview, Poster: Image(s.PosterPath, "w500"), Backdrop: Image(s.BackdropPath, "w1280"), Status: s.Status,
		Rating: s.VoteAverage,
	}
	if len(s.Networks) > 0 {
		out.Network = s.Networks[0].Name
	}
	if len(s.RunTimes) > 0 {
		out.Runtime = s.RunTimes[0]
	}
	for _, se := range s.Seasons {
		out.Seasons = append(out.Seasons, SeasonInfo{Number: se.Number, Episodes: se.Episodes})
	}
	return out
}

// SearchShows finds shows by name. "tmdb:1399" and "tvdb:121361" find one
// by its id; TVDB ids are how Sonarr's clients ask.
func (c *Client) SearchShows(ctx context.Context, term string) ([]Show, error) {
	if id, ok := idTerm(term, "tmdb"); ok {
		s, err := c.Show(ctx, id)
		if errors.Is(err, ErrNotFound) {
			return []Show{}, nil
		}
		return []Show{s}, err
	}
	if id, ok := idTerm(term, "tvdb"); ok {
		s, err := c.ShowByTVDB(ctx, id)
		if errors.Is(err, ErrNotFound) {
			return []Show{}, nil
		}
		return []Show{s}, err
	}
	var res struct {
		Results []apiShow `json:"results"`
	}
	if err := c.get(ctx, "/search/tv", url.Values{"query": {term}, "include_adult": {"false"}}, &res); err != nil {
		return nil, err
	}
	out := make([]Show, 0, len(res.Results))
	for _, s := range res.Results {
		out = append(out, s.show())
	}
	return out, nil
}

// Show describes one show, with its seasons and external ids.
func (c *Client) Show(ctx context.Context, id int64) (Show, error) {
	var s apiShow
	if err := c.get(ctx, "/tv/"+strconv.FormatInt(id, 10), url.Values{"append_to_response": {"external_ids"}}, &s); err != nil {
		return Show{}, err
	}
	return s.show(), nil
}

// ShowByTVDB finds a show by its TVDB id.
func (c *Client) ShowByTVDB(ctx context.Context, tvdbID int64) (Show, error) {
	var res struct {
		TV []struct {
			ID int64 `json:"id"`
		} `json:"tv_results"`
	}
	if err := c.get(ctx, "/find/"+strconv.FormatInt(tvdbID, 10), url.Values{"external_source": {"tvdb_id"}}, &res); err != nil {
		return Show{}, err
	}
	if len(res.TV) == 0 {
		return Show{}, ErrNotFound
	}
	return c.Show(ctx, res.TV[0].ID)
}

// MovieByIMDB finds a movie by its IMDb id, for imports that lack a TMDB id.
func (c *Client) MovieByIMDB(ctx context.Context, imdbID string) (Movie, error) {
	var res struct {
		Movies []struct {
			ID int64 `json:"id"`
		} `json:"movie_results"`
	}
	if err := c.get(ctx, "/find/"+url.PathEscape(imdbID), url.Values{"external_source": {"imdb_id"}}, &res); err != nil {
		return Movie{}, err
	}
	if len(res.Movies) == 0 {
		return Movie{}, ErrNotFound
	}
	return c.Movie(ctx, res.Movies[0].ID)
}

// Episode is one episode of a season.
type Episode struct {
	Season   int        `json:"season"`
	Number   int        `json:"number"`
	Title    string     `json:"title"`
	Overview string     `json:"overview,omitempty"`
	AirDate  *time.Time `json:"airDate,omitempty"`
}

// Season lists a season's episodes.
func (c *Client) Season(ctx context.Context, showID int64, number int) ([]Episode, error) {
	var res struct {
		Episodes []struct {
			Season   int    `json:"season_number"`
			Number   int    `json:"episode_number"`
			Name     string `json:"name"`
			Overview string `json:"overview"`
			AirDate  string `json:"air_date"`
		} `json:"episodes"`
	}
	if err := c.get(ctx, fmt.Sprintf("/tv/%d/season/%d", showID, number), nil, &res); err != nil {
		return nil, err
	}
	out := make([]Episode, 0, len(res.Episodes))
	for _, e := range res.Episodes {
		out = append(out, Episode{Season: e.Season, Number: e.Number, Title: e.Name, Overview: e.Overview, AirDate: parseDate(e.AirDate)})
	}
	return out, nil
}

func idTerm(term, prefix string) (int64, bool) {
	rest, ok := strings.CutPrefix(strings.ToLower(strings.TrimSpace(term)), prefix+":")
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
	return id, err == nil && id > 0
}

// splitYear reads "Heat 1995" or "Heat (1995)" as a title and a year.
func splitYear(term string) (string, int, bool) {
	term = strings.TrimSpace(term)
	i := strings.LastIndexByte(term, ' ')
	if i < 0 {
		return "", 0, false
	}
	y, err := strconv.Atoi(strings.Trim(term[i+1:], "()"))
	if err != nil || y < 1880 || y > 2100 {
		return "", 0, false
	}
	return strings.TrimSpace(term[:i]), y, true
}
