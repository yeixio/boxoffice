// Package matineearr is a copy of Matinee's Radarr and Sonarr client
// (internal/arr in github.com/yeixio/matinee, as of 95c6b9a). Box Office's
// tests drive it against Box Office's /radarr and /sonarr APIs, to prove
// Matinee works with Box Office unchanged. Keep it in step with Matinee's.
//
// Matinee's own description of it:
//
// Package arr talks to Radarr (movies) and Sonarr (TV), the apps many people
// run next to their media server to find and download what they watch.
// Matinee uses them to add titles people ask for, list what's coming soon,
// change a title's quality profile and replace a bad file.
//
// It's a built-in integration. Everything Matinee needs from these apps goes
// through Client, so the rest of the server doesn't know their APIs, and the
// integration can move behind the plugin framework later without the HTTP
// API changing.
package matineearr

import (
	"bytes"
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

// App is one of the apps Matinee connects to.
type App string

const (
	Radarr App = "radarr" // movies
	Sonarr App = "sonarr" // TV shows
)

// Apps lists the apps Matinee connects to.
var Apps = []App{Radarr, Sonarr}

// ParseApp reads an app's name, as used in URLs.
func ParseApp(s string) (App, bool) {
	switch App(s) {
	case Radarr, Sonarr:
		return App(s), true
	}
	return "", false
}

// Name is the app's name as people know it.
func (a App) Name() string {
	if a == Radarr {
		return "Radarr"
	}
	return "Sonarr"
}

// Config is how Matinee reaches one app, set by an admin. The API key stays
// on the server.
type Config struct {
	URL    string `json:"url"`
	APIKey string `json:"apiKey"`
	// What titles added from Matinee get, unless whoever adds one picks another profile.
	QualityProfileID int    `json:"qualityProfileId"`
	RootFolder       string `json:"rootFolder"`
}

// Client calls one app's API (v3, which current Radarr and Sonarr both serve).
type Client struct {
	App  App
	base *url.URL
	key  string
	http *http.Client
}

// ErrNotFound means the app doesn't have the title.
var ErrNotFound = errors.New("not found")

// Error is a failed call, with a message fit to show an admin.
type Error struct {
	Status  int
	Message string
	// redirect is where the app sent the request instead, unfollowed.
	redirect *url.URL
}

func (e *Error) Error() string { return e.Message }

// NormalizeURL accepts an address the way people type it, such as
// "radarr:7878" or "https://example.com/sonarr/".
func NormalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("enter the address")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("%q isn't an http(s) address", raw)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery, u.Fragment, u.User = "", "", nil
	return u.String(), nil
}

func New(app App, c Config) (*Client, error) {
	raw, err := NormalizeURL(c.URL)
	if err != nil {
		return nil, err
	}
	u, _ := url.Parse(raw)
	if strings.TrimSpace(c.APIKey) == "" {
		return nil, errors.New("enter the API key")
	}
	return &Client{
		App: app, base: u, key: strings.TrimSpace(c.APIKey),
		http: &http.Client{
			Timeout: 20 * time.Second,
			// The API key goes in a header that redirects would carry along,
			// possibly to another host, so redirects aren't followed.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return &redirectError{req.URL}
			},
		},
	}, nil
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	u := *c.base
	u.Path += "/api/v3" + path
	u.RawQuery = query.Encode()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), r)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", c.key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	var rd *redirectError
	if errors.As(err, &rd) {
		base := baseOf(rd.to, u.Path[len(c.base.Path):])
		return &Error{redirect: base, Message: fmt.Sprintf("%s at %s redirects to %s; try that address", c.App.Name(), c.base.Redacted(), base.Redacted())}
	}
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // the URL is in the message already, without it
		}
		return &Error{Message: fmt.Sprintf("couldn't reach %s at %s: %v", c.App.Name(), c.base.Redacted(), err)}
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		return &Error{Status: res.StatusCode, Message: c.App.Name() + " didn't accept the API key"}
	case res.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case res.StatusCode >= 300:
		return &Error{Status: res.StatusCode, Message: c.App.Name() + ": " + errorMessage(res)}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<20)).Decode(out); err != nil {
		if strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") {
			// A web page where the API should be: usually the wrong address or port.
			return &Error{Message: fmt.Sprintf("%s doesn't look like %s's address", c.base.Redacted(), c.App.Name())}
		}
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	return nil
}

// errorMessage pulls the reason out of an error response. Radarr and Sonarr
// answer with {"message": …} or a list of validation failures.
func errorMessage(res *http.Response) string {
	b, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	var one struct{ Message string }
	if json.Unmarshal(b, &one) == nil && one.Message != "" {
		return one.Message
	}
	var list []struct{ ErrorMessage string }
	if json.Unmarshal(b, &list) == nil && len(list) > 0 && list[0].ErrorMessage != "" {
		msgs := make([]string, len(list))
		for i, e := range list {
			msgs[i] = e.ErrorMessage
		}
		return strings.Join(msgs, "; ")
	}
	return "HTTP " + strconv.Itoa(res.StatusCode)
}

type redirectError struct{ to *url.URL }

func (r *redirectError) Error() string { return "redirected to " + r.to.Redacted() }

// baseOf is the address an app redirected a request for path to, without
// the path: Radarr and Sonarr redirect requests that leave out their URL
// base (Settings → General), so /api/v3/x goes to /radarr/api/v3/x.
func baseOf(to *url.URL, path string) *url.URL {
	b := *to
	b.RawQuery, b.Fragment, b.User = "", "", nil
	if p, ok := strings.CutSuffix(b.Path, path); ok {
		b.Path = strings.TrimRight(p, "/")
	} else {
		b.Path = ""
	}
	return &b
}

// URL is the app's address, as Status found it.
func (c *Client) URL() string { return c.base.String() }

// Status is what an app says about itself.
type Status struct {
	AppName string `json:"appName"`
	Version string `json:"version"`
}

// Status checks the address and key reach the app this client is for.
//
// When the app redirects to its own URL base on the same host (or from http
// to https there), the client moves to that address; URL then reports it.
// Redirects elsewhere aren't followed, since they'd carry the API key along.
func (c *Client) Status(ctx context.Context) (Status, error) {
	var s Status
	err := c.do(ctx, "GET", "/system/status", nil, nil, &s)
	var ae *Error
	if errors.As(err, &ae) && ae.redirect != nil && ae.redirect.Host == c.base.Host &&
		(ae.redirect.Scheme == c.base.Scheme || ae.redirect.Scheme == "https") && ae.redirect.String() != c.base.String() {
		c.base = ae.redirect
		err = c.do(ctx, "GET", "/system/status", nil, nil, &s)
	}
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return s, &Error{Message: fmt.Sprintf("%s doesn't look like %s's address", c.base.Redacted(), c.App.Name())}
		}
		return s, err
	}
	if s.AppName != "" && !strings.EqualFold(s.AppName, c.App.Name()) {
		return s, &Error{Message: fmt.Sprintf("that address is %s, not %s", s.AppName, c.App.Name())}
	}
	return s, nil
}

type QualityProfile struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func (c *Client) QualityProfiles(ctx context.Context) ([]QualityProfile, error) {
	out := []QualityProfile{}
	return out, c.do(ctx, "GET", "/qualityprofile", nil, nil, &out)
}

type RootFolder struct {
	Path      string `json:"path"`
	FreeSpace int64  `json:"freeSpace"`
}

func (c *Client) RootFolders(ctx context.Context) ([]RootFolder, error) {
	out := []RootFolder{}
	return out, c.do(ctx, "GET", "/rootfolder", nil, nil, &out)
}

// Title is a movie (Radarr) or show (Sonarr).
type Title struct {
	ID               int    `json:"-"` // the app's own id; 0 when it hasn't added the title
	TMDBID           int    `json:"tmdbId,omitempty"`
	TVDBID           int    `json:"tvdbId,omitempty"`
	Title            string `json:"title"`
	Year             int    `json:"year,omitempty"`
	Overview         string `json:"overview,omitempty"`
	Poster           string `json:"poster,omitempty"`
	QualityProfileID int    `json:"-"`
	HasFile          bool   `json:"-"` // a movie's file is there
	FileID           int    `json:"-"`
}

type image struct {
	CoverType string `json:"coverType"`
	RemoteURL string `json:"remoteUrl"`
	URL       string `json:"url"`
}

type apiTitle struct {
	ID               int     `json:"id"`
	TMDBID           int     `json:"tmdbId"`
	TVDBID           int     `json:"tvdbId"`
	Title            string  `json:"title"`
	Year             int     `json:"year"`
	Overview         string  `json:"overview"`
	Images           []image `json:"images"`
	QualityProfileID int     `json:"qualityProfileId"`
	HasFile          bool    `json:"hasFile"`
	MovieFile        *struct {
		ID int `json:"id"`
	} `json:"movieFile"`
}

func (t apiTitle) title() Title {
	out := Title{
		ID: t.ID, TMDBID: t.TMDBID, TVDBID: t.TVDBID, Title: t.Title, Year: t.Year, Overview: t.Overview,
		Poster: poster(t.Images), QualityProfileID: t.QualityProfileID, HasFile: t.HasFile,
	}
	if t.MovieFile != nil {
		out.FileID = t.MovieFile.ID
	}
	return out
}

// poster is a title's poster on the internet. The app's own copy would need
// the API key to fetch.
func poster(images []image) string {
	for _, im := range images {
		if im.CoverType == "poster" && strings.HasPrefix(im.RemoteURL, "https://") {
			return im.RemoteURL
		}
	}
	return ""
}

func (c *Client) kindPath() string {
	if c.App == Radarr {
		return "/movie"
	}
	return "/series"
}

// Search looks titles up online through the app, the way its own Add New
// page does. Titles the app already has come back with their ID set.
func (c *Client) Search(ctx context.Context, term string) ([]Title, error) {
	var res []apiTitle
	if err := c.do(ctx, "GET", c.kindPath()+"/lookup", url.Values{"term": {term}}, nil, &res); err != nil {
		return nil, err
	}
	out := make([]Title, 0, len(res))
	for _, t := range res {
		out = append(out, t.title())
	}
	return out, nil
}

// lookup finds one title online by its id: TMDB for movies, TVDB for shows.
// It returns the app's own description of it, which is what adding it takes.
func (c *Client) lookup(ctx context.Context, id int) (map[string]any, error) {
	if c.App == Radarr {
		var m map[string]any
		err := c.do(ctx, "GET", "/movie/lookup/tmdb", url.Values{"tmdbId": {strconv.Itoa(id)}}, nil, &m)
		if err == nil && len(m) == 0 {
			err = ErrNotFound
		}
		return m, err
	}
	var list []map[string]any
	if err := c.do(ctx, "GET", "/series/lookup", url.Values{"term": {"tvdb:" + strconv.Itoa(id)}}, nil, &list); err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return list[0], nil
}

// Lookup describes one title, by TMDB id for movies or TVDB id for shows.
func (c *Client) Lookup(ctx context.Context, id int) (Title, error) {
	m, err := c.lookup(ctx, id)
	if err != nil {
		return Title{}, err
	}
	return fromMap(m)
}

func fromMap(m map[string]any) (Title, error) {
	b, _ := json.Marshal(m)
	var t apiTitle
	err := json.Unmarshal(b, &t)
	return t.title(), err
}

// AddOptions say how to add a title.
type AddOptions struct {
	QualityProfileID int
	RootFolder       string
}

// Add adds a title, by TMDB id for movies or TVDB id for shows, and starts
// searching for it. A title the app already has is left as it is.
func (c *Client) Add(ctx context.Context, id int, opt AddOptions) (Title, error) {
	m, err := c.lookup(ctx, id)
	if err != nil {
		return Title{}, err
	}
	if existing, _ := m["id"].(float64); existing > 0 {
		return fromMap(m)
	}
	m["qualityProfileId"] = opt.QualityProfileID
	m["rootFolderPath"] = opt.RootFolder
	m["monitored"] = true
	if c.App == Radarr {
		m["minimumAvailability"] = "released"
		m["addOptions"] = map[string]any{"searchForMovie": true}
	} else {
		m["seasonFolder"] = true
		m["addOptions"] = map[string]any{"monitor": "all", "searchForMissingEpisodes": true}
		if seasons, ok := m["seasons"].([]any); ok {
			for _, s := range seasons {
				if s, ok := s.(map[string]any); ok {
					// Specials (season 0) stay off, as in Sonarr's own default.
					n, _ := s["seasonNumber"].(float64)
					s["monitored"] = n > 0
				}
			}
		}
		// Sonarr v3 wants a language profile; v4 has none and ignores the field.
		var langs []struct{ ID int }
		if c.do(ctx, "GET", "/languageprofile", nil, nil, &langs) == nil && len(langs) > 0 {
			m["languageProfileId"] = langs[0].ID
		}
	}
	var added apiTitle
	if err := c.do(ctx, "POST", c.kindPath(), nil, m, &added); err != nil {
		return Title{}, err
	}
	return added.title(), nil
}

// Find looks for a title the app already has: by TMDB id, else by title and
// year (for libraries looked up without TMDB, and Sonarr versions that don't
// know TMDB ids).
func (c *Client) Find(ctx context.Context, tmdbID int, title string, year int) (Title, error) {
	var list []apiTitle
	q := url.Values{}
	if c.App == Radarr && tmdbID > 0 {
		q.Set("tmdbId", strconv.Itoa(tmdbID))
		if err := c.do(ctx, "GET", "/movie", q, nil, &list); err != nil {
			return Title{}, err
		}
		if len(list) > 0 {
			return list[0].title(), nil
		}
		q = url.Values{}
	}
	if err := c.do(ctx, "GET", c.kindPath(), q, nil, &list); err != nil {
		return Title{}, err
	}
	for _, t := range list {
		if tmdbID > 0 && t.TMDBID == tmdbID {
			return t.title(), nil
		}
	}
	key := matchKey(title)
	for _, t := range list {
		if matchKey(t.Title) == key && (year == 0 || t.Year == 0 || abs(t.Year-year) <= 1) {
			return t.title(), nil
		}
	}
	return Title{}, ErrNotFound
}

func matchKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// SetQualityProfile changes a title's quality profile and, with search, looks
// for copies that match it.
func (c *Client) SetQualityProfile(ctx context.Context, id, profileID int, search bool) error {
	path := c.kindPath() + "/" + strconv.Itoa(id)
	var m map[string]any
	if err := c.do(ctx, "GET", path, nil, nil, &m); err != nil {
		return err
	}
	m["qualityProfileId"] = profileID
	if err := c.do(ctx, "PUT", path, nil, m, nil); err != nil {
		return err
	}
	if !search {
		return nil
	}
	if c.App == Radarr {
		return c.command(ctx, map[string]any{"name": "MoviesSearch", "movieIds": []int{id}})
	}
	return c.command(ctx, map[string]any{"name": "SeriesSearch", "seriesId": id})
}

func (c *Client) command(ctx context.Context, cmd map[string]any) error {
	return c.do(ctx, "POST", "/command", nil, cmd, nil)
}

// ReplaceMovie deletes a movie's file and searches for another copy.
func (c *Client) ReplaceMovie(ctx context.Context, movie Title) error {
	if movie.FileID > 0 {
		if err := c.do(ctx, "DELETE", "/moviefile/"+strconv.Itoa(movie.FileID), nil, nil, nil); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	return c.command(ctx, map[string]any{"name": "MoviesSearch", "movieIds": []int{movie.ID}})
}

// ReplaceEpisode deletes an episode's file and searches for another copy.
func (c *Client) ReplaceEpisode(ctx context.Context, seriesID, season, episode int) error {
	var eps []struct {
		ID            int `json:"id"`
		SeasonNumber  int `json:"seasonNumber"`
		EpisodeNumber int `json:"episodeNumber"`
		EpisodeFileID int `json:"episodeFileId"`
	}
	if err := c.do(ctx, "GET", "/episode", url.Values{"seriesId": {strconv.Itoa(seriesID)}}, nil, &eps); err != nil {
		return err
	}
	for _, ep := range eps {
		if ep.SeasonNumber != season || ep.EpisodeNumber != episode {
			continue
		}
		if ep.EpisodeFileID > 0 {
			if err := c.do(ctx, "DELETE", "/episodefile/"+strconv.Itoa(ep.EpisodeFileID), nil, nil, nil); err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
		}
		return c.command(ctx, map[string]any{"name": "EpisodeSearch", "episodeIds": []int{ep.ID}})
	}
	return ErrNotFound
}

// Upcoming is a movie release or an episode airing.
type Upcoming struct {
	Kind      string    `json:"kind"` // movie or episode
	Title     string    `json:"title"`
	Year      int       `json:"year,omitempty"`
	ShowTitle string    `json:"showTitle,omitempty"`
	Season    *int      `json:"season,omitempty"`
	Episode   *int      `json:"episode,omitempty"`
	Date      time.Time `json:"date"`
	// Release says what happens on Date: for movies cinemas, digital or
	// physical; for episodes airs.
	Release  string `json:"release"`
	Overview string `json:"overview,omitempty"`
	Poster   string `json:"poster,omitempty"`
	TMDBID   int    `json:"tmdbId,omitempty"`
}

// Calendar lists what's due between from and to that the app is waiting
// for: monitored titles without a file yet.
func (c *Client) Calendar(ctx context.Context, from, to time.Time) ([]Upcoming, error) {
	q := url.Values{"start": {from.UTC().Format(time.RFC3339)}, "end": {to.UTC().Format(time.RFC3339)}, "unmonitored": {"false"}}
	out := []Upcoming{}
	inRange := func(t *time.Time) bool { return t != nil && !t.Before(from) && t.Before(to) }
	if c.App == Radarr {
		var movies []struct {
			apiTitle
			InCinemas       *time.Time `json:"inCinemas"`
			DigitalRelease  *time.Time `json:"digitalRelease"`
			PhysicalRelease *time.Time `json:"physicalRelease"`
		}
		if err := c.do(ctx, "GET", "/calendar", q, nil, &movies); err != nil {
			return nil, err
		}
		for _, m := range movies {
			if m.HasFile {
				continue
			}
			t := m.title()
			for _, r := range []struct {
				at   *time.Time
				kind string
			}{{m.InCinemas, "cinemas"}, {m.DigitalRelease, "digital"}, {m.PhysicalRelease, "physical"}} {
				if inRange(r.at) {
					out = append(out, Upcoming{Kind: "movie", Title: t.Title, Year: t.Year, Date: *r.at, Release: r.kind,
						Overview: t.Overview, Poster: t.Poster, TMDBID: t.TMDBID})
				}
			}
		}
		return out, nil
	}
	q.Set("includeSeries", "true")
	var eps []struct {
		Title         string     `json:"title"`
		SeasonNumber  int        `json:"seasonNumber"`
		EpisodeNumber int        `json:"episodeNumber"`
		AirDateUTC    *time.Time `json:"airDateUtc"`
		Overview      string     `json:"overview"`
		HasFile       bool       `json:"hasFile"`
		Series        apiTitle   `json:"series"`
	}
	if err := c.do(ctx, "GET", "/calendar", q, nil, &eps); err != nil {
		return nil, err
	}
	for _, e := range eps {
		if e.HasFile || e.AirDateUTC == nil {
			continue
		}
		show := e.Series.title()
		season, episode := e.SeasonNumber, e.EpisodeNumber
		out = append(out, Upcoming{Kind: "episode", Title: e.Title, ShowTitle: show.Title, Season: &season, Episode: &episode,
			Date: *e.AirDateUTC, Release: "airs", Overview: e.Overview, Poster: show.Poster, TMDBID: show.TMDBID})
	}
	return out, nil
}
