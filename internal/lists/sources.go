package lists

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/yeixio/boxoffice/internal/tmdb"
)

// The places a list can be followed from.
const (
	SourceTMDB  = "tmdb"
	SourceTrakt = "trakt"
	SourcePlex  = "plex"
)

// most is how many titles are read from one list. A longer list is
// followed as far as that.
const most = 2000

// Entry is a title on a list, as the list's site knows it: by TMDB's id
// where it says, else by IMDb's or TheTVDB's.
type Entry struct {
	Kind   string // movie or series; "" when the site doesn't say
	TMDBID int64
	IMDBID string
	TVDBID int64
	Title  string
	Year   int
}

// place is where a list is, worked out from the address typed.
type place struct {
	Source string
	// TMDB: the list's id. Trakt: the user, and the list's slug ("" for
	// the watchlist). Plex: the feed's address.
	ID, User, Slug, URL string
}

var (
	tmdbList   = regexp.MustCompile(`^/list/([0-9]+)(?:-[^/]*)?/?$`)
	traktWatch = regexp.MustCompile(`^/users/([^/]+)/watchlist/?$`)
	traktList  = regexp.MustCompile(`^/users/([^/]+)/lists/([^/]+)/?$`)
)

// Place works out which site an address is a list on. What's typed can be
// the page's address as the browser shows it.
func Place(address string) (place, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return place{}, badf("Paste the list's address.")
	}
	if !strings.Contains(address, "://") {
		address = "https://" + address
	}
	u, err := url.Parse(address)
	if err != nil || u.Host == "" {
		return place{}, badf("That isn't a web address. Paste the list's address as your browser shows it.")
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	switch host {
	case "themoviedb.org":
		if m := tmdbList.FindStringSubmatch(u.Path); m != nil {
			return place{Source: SourceTMDB, ID: m[1]}, nil
		}
		return place{}, badf("That TMDB address isn't a list. A list's address looks like themoviedb.org/list/12345.")
	case "trakt.tv", "app.trakt.tv":
		if m := traktWatch.FindStringSubmatch(u.Path); m != nil {
			return place{Source: SourceTrakt, User: m[1]}, nil
		}
		if m := traktList.FindStringSubmatch(u.Path); m != nil {
			return place{Source: SourceTrakt, User: m[1], Slug: m[2]}, nil
		}
		return place{}, badf("That Trakt address isn't a list. Use trakt.tv/users/NAME/watchlist or trakt.tv/users/NAME/lists/LIST.")
	case "rss.plex.tv":
		if u.Scheme != "https" || u.Path == "" || u.Path == "/" {
			return place{}, badf("That isn't a Plex watchlist feed. It looks like https://rss.plex.tv/ followed by a long code.")
		}
		return place{Source: SourcePlex, URL: "https://rss.plex.tv" + u.Path}, nil
	case "plex.tv", "app.plex.tv", "watch.plex.tv":
		return place{}, badf("Plex needs the watchlist's feed address, not the page's. In Plex, open Account, then Watchlist, turn on the RSS feed and copy its address (it starts with https://rss.plex.tv/).")
	case "imdb.com", "m.imdb.com", "letterboxd.com", "boxd.it":
		return place{}, badf("Box Office can't follow lists on that site yet. It follows TMDB lists, Trakt watchlists and lists, and Plex watchlists.")
	}
	return place{}, badf("Box Office doesn't know lists at %s. It follows TMDB lists, Trakt watchlists and lists, and Plex watchlists.", host)
}

// fetch reads a list: its name there, if it has one, and its titles.
func (s *Service) fetch(ctx context.Context, p place) (string, []Entry, error) {
	switch p.Source {
	case SourceTMDB:
		name, titles, err := s.Catalog.TMDB(ctx).UserList(ctx, p.ID, most)
		if errors.Is(err, tmdb.ErrNotFound) {
			return "", nil, badf("TMDB has no public list %s. A private list can't be followed.", p.ID)
		}
		if errors.Is(err, tmdb.ErrNoKey) {
			return "", nil, badf("Add a TMDB API key under Settings first.")
		}
		if err != nil {
			return "", nil, err
		}
		out := make([]Entry, len(titles))
		for i, t := range titles {
			out[i] = Entry{Kind: t.Kind, TMDBID: t.TMDBID, Title: t.Title, Year: t.Year}
		}
		return name, out, nil
	case SourceTrakt:
		return s.trakt(ctx, p)
	case SourcePlex:
		return s.plex(ctx, p)
	}
	return "", nil, badf("Box Office doesn't know lists of that kind.")
}

// ---- Trakt ----

const traktBase = "https://api.trakt.tv"

// ErrNoTraktKey means Trakt's client ID hasn't been saved yet.
var ErrNoTraktKey = &Error{"Trakt needs a client ID first. Save one below, then check the list again."}

type traktItem struct {
	Type  string      `json:"type"`
	Movie *traktTitle `json:"movie"`
	Show  *traktTitle `json:"show"`
}

type traktTitle struct {
	Title string `json:"title"`
	Year  int    `json:"year"`
	IDs   struct {
		TMDB int64  `json:"tmdb"`
		IMDB string `json:"imdb"`
		TVDB int64  `json:"tvdb"`
	} `json:"ids"`
}

// trakt reads someone's public watchlist or list, movies then shows.
func (s *Service) trakt(ctx context.Context, p place) (string, []Entry, error) {
	key, err := s.Store.Setting(ctx, SettingTraktClient)
	if err != nil {
		return "", nil, err
	}
	if key == "" {
		return "", nil, ErrNoTraktKey
	}
	base := s.TraktBase
	if base == "" {
		base = traktBase
	}
	path := "/users/" + url.PathEscape(p.User) + "/watchlist/"
	name := p.User + "'s watchlist"
	if p.Slug != "" {
		path = "/users/" + url.PathEscape(p.User) + "/lists/" + url.PathEscape(p.Slug) + "/items/"
		name = strings.ReplaceAll(p.Slug, "-", " ")
	}
	var out []Entry
	for _, kind := range []string{"movies", "shows"} {
		for page := 1; len(out) < most; page++ {
			req, err := http.NewRequestWithContext(ctx, "GET", base+path+kind+"?limit=100&page="+strconv.Itoa(page), nil)
			if err != nil {
				return "", nil, err
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("trakt-api-version", "2")
			req.Header.Set("trakt-api-key", key)
			req.Header.Set("User-Agent", s.agent())
			res, err := s.client().Do(req)
			if err != nil {
				return "", nil, fmt.Errorf("couldn't reach Trakt: %w", plain(err))
			}
			var items []traktItem
			switch {
			case res.StatusCode == http.StatusForbidden || res.StatusCode == http.StatusUnauthorized:
				res.Body.Close()
				if p.Slug == "" {
					return "", nil, badf("Trakt wouldn't show that watchlist. Either the client ID is wrong, or %s's profile is private. A private profile can't be followed.", p.User)
				}
				return "", nil, badf("Trakt wouldn't show that list. Either the client ID is wrong, or the list is private. A private list can't be followed.")
			case res.StatusCode == http.StatusNotFound:
				res.Body.Close()
				return "", nil, badf("Trakt has no such list. Check the name in the address.")
			case res.StatusCode >= 300:
				res.Body.Close()
				return "", nil, fmt.Errorf("Trakt answered HTTP %d", res.StatusCode)
			}
			err = json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(&items)
			res.Body.Close()
			if err != nil {
				return "", nil, fmt.Errorf("couldn't read Trakt's answer: %w", err)
			}
			for _, it := range items {
				switch {
				case it.Movie != nil:
					out = append(out, Entry{Kind: tmdb.KindMovie, TMDBID: it.Movie.IDs.TMDB, IMDBID: it.Movie.IDs.IMDB, Title: it.Movie.Title, Year: it.Movie.Year})
				case it.Show != nil:
					out = append(out, Entry{Kind: tmdb.KindSeries, TMDBID: it.Show.IDs.TMDB, IMDBID: it.Show.IDs.IMDB, TVDBID: it.Show.IDs.TVDB, Title: it.Show.Title, Year: it.Show.Year})
				}
			}
			pages, _ := strconv.Atoi(res.Header.Get("X-Pagination-Page-Count"))
			if len(items) == 0 || page >= pages {
				break
			}
		}
	}
	return name, out, nil
}

// ---- Plex ----

type plexFeed struct {
	Channel struct {
		Title string `xml:"title"`
		Items []struct {
			Title    string `xml:"title"`
			GUID     string `xml:"guid"`
			Category string `xml:"category"`
		} `xml:"item"`
	} `xml:"channel"`
}

var titleYear = regexp.MustCompile(`^(.*\S)\s*\((\d{4})\)$`)

// plex reads a watchlist's feed. Each title has one id: IMDb's for most,
// TheTVDB's or TMDB's for the rest.
func (s *Service) plex(ctx context.Context, p place) (string, []Entry, error) {
	feed := p.URL
	if s.PlexFeed != nil {
		feed = s.PlexFeed(feed)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", feed, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("User-Agent", s.agent())
	res, err := s.client().Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("couldn't reach Plex: %w", plain(err))
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusGone {
		return "", nil, badf("Plex has no feed at that address. Copy it again from Account, then Watchlist, in Plex.")
	}
	if res.StatusCode >= 300 {
		return "", nil, fmt.Errorf("Plex answered HTTP %d", res.StatusCode)
	}
	var f plexFeed
	if err := xml.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(&f); err != nil {
		return "", nil, badf("That address didn't answer with a Plex watchlist feed.")
	}
	var out []Entry
	for _, it := range f.Channel.Items {
		e := Entry{Title: strings.TrimSpace(it.Title)}
		if m := titleYear.FindStringSubmatch(e.Title); m != nil {
			e.Title = m[1]
			e.Year, _ = strconv.Atoi(m[2])
		}
		switch strings.ToLower(strings.TrimSpace(it.Category)) {
		case "movie":
			e.Kind = tmdb.KindMovie
		case "show":
			e.Kind = tmdb.KindSeries
		}
		scheme, id, _ := strings.Cut(strings.TrimSpace(it.GUID), "://")
		switch scheme {
		case "imdb":
			e.IMDBID = id
		case "tvdb":
			e.TVDBID, _ = strconv.ParseInt(id, 10, 64)
		case "tmdb":
			e.TMDBID, _ = strconv.ParseInt(id, 10, 64)
		}
		if e.TMDBID == 0 && e.IMDBID == "" && e.TVDBID == 0 {
			continue
		}
		out = append(out, e)
		if len(out) >= most {
			break
		}
	}
	return "Plex watchlist", out, nil
}

// plain is an error without the address it was for, which can hold a key.
func plain(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}
