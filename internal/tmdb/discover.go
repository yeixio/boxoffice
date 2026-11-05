package tmdb

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// Title is a movie or show in one of TMDB's lists, with only what a poster
// needs.
type Title struct {
	Kind     string  `json:"kind"` // movie or series
	TMDBID   int64   `json:"tmdbId"`
	Title    string  `json:"title"`
	Year     int     `json:"year,omitempty"`
	Overview string  `json:"overview,omitempty"`
	Poster   string  `json:"poster,omitempty"`
	Backdrop string  `json:"backdrop,omitempty"`
	Rating   float64 `json:"rating,omitempty"`
	// Date is when it comes out or first airs, as TMDB writes it.
	Date       string  `json:"date,omitempty"`
	Popularity float64 `json:"-"`
}

type listResult struct {
	ID           int64   `json:"id"`
	MediaType    string  `json:"media_type"`
	Title        string  `json:"title"`
	Name         string  `json:"name"`
	ReleaseDate  string  `json:"release_date"`
	FirstAirDate string  `json:"first_air_date"`
	Overview     string  `json:"overview"`
	PosterPath   string  `json:"poster_path"`
	BackdropPath string  `json:"backdrop_path"`
	VoteAverage  float64 `json:"vote_average"`
	Popularity   float64 `json:"popularity"`
	Adult        bool    `json:"adult"`
}

// Page is one page of a list, and how many pages the list has.
type Page struct {
	Titles []Title `json:"titles"`
	Pages  int     `json:"pages"`
}

// List kinds: what a list holds. Trending lists hold both, and say which
// each title is.
const (
	KindMovie  = "movie"
	KindSeries = "series"
	kindBoth   = ""
)

// list reads a page (from 1) of one of TMDB's lists at path.
func (c *Client) list(ctx context.Context, path, kind string, q url.Values, page int) (Page, error) {
	if q == nil {
		q = url.Values{}
	}
	q.Set("page", strconv.Itoa(max(page, 1)))
	var res struct {
		Results    []listResult `json:"results"`
		TotalPages int          `json:"total_pages"`
	}
	if err := c.get(ctx, path, q, &res); err != nil {
		return Page{}, err
	}
	out := Page{Titles: []Title{}, Pages: res.TotalPages}
	for _, r := range res.Results {
		k := kind
		if k == kindBoth {
			switch r.MediaType {
			case "movie":
				k = KindMovie
			case "tv":
				k = KindSeries
			default:
				continue // people
			}
		}
		if r.Adult {
			continue
		}
		t := Title{Kind: k, TMDBID: r.ID, Overview: r.Overview, Poster: Image(r.PosterPath, "w342"), Backdrop: Image(r.BackdropPath, "w1280"),
			Rating: r.VoteAverage, Popularity: r.Popularity}
		if k == KindMovie {
			t.Title, t.Date = r.Title, r.ReleaseDate
		} else {
			t.Title, t.Date = r.Name, r.FirstAirDate
		}
		t.Year = year(t.Date)
		out.Titles = append(out.Titles, t)
	}
	return out, nil
}

func apiKind(kind string) string {
	if kind == KindSeries {
		return "tv"
	}
	return "movie"
}

// Trending is what's trending this week, movies and shows together.
func (c *Client) Trending(ctx context.Context, page int) (Page, error) {
	return c.list(ctx, "/trending/all/week", kindBoth, nil, page)
}

// Popular is what's popular now, movies or shows.
func (c *Client) Popular(ctx context.Context, kind string, page int) (Page, error) {
	q := url.Values{}
	if kind == KindMovie {
		q.Set("region", Country)
	}
	return c.list(ctx, "/"+apiKind(kind)+"/popular", kind, q, page)
}

// Upcoming is what's coming out from today, most anticipated first: movies
// in cinemas in Country, or shows' first episodes.
func (c *Client) Upcoming(ctx context.Context, kind string, page int, today time.Time) (Page, error) {
	from := today.Format("2006-01-02")
	q := url.Values{"sort_by": {"popularity.desc"}, "include_adult": {"false"}}
	if kind == KindMovie {
		q.Set("region", Country)
		q.Set("with_release_type", "2|3")
		q.Set("release_date.gte", from)
	} else {
		q.Set("first_air_date.gte", from)
	}
	return c.list(ctx, "/discover/"+apiKind(kind), kind, q, page)
}

// Recommendations are titles TMDB says go with a movie or show.
func (c *Client) Recommendations(ctx context.Context, kind string, tmdbID int64, page int) (Page, error) {
	return c.list(ctx, fmt.Sprintf("/%s/%d/recommendations", apiKind(kind), tmdbID), kind, nil, page)
}

// ByGenre is the popular titles of a genre.
func (c *Client) ByGenre(ctx context.Context, kind string, genre int64, page int) (Page, error) {
	q := url.Values{"sort_by": {"popularity.desc"}, "include_adult": {"false"}, "with_genres": {strconv.FormatInt(genre, 10)},
		"vote_count.gte": {"50"}}
	return c.list(ctx, "/discover/"+apiKind(kind), kind, q, page)
}

// ByNetwork is the popular shows of a TV network or streaming service.
func (c *Client) ByNetwork(ctx context.Context, network int64, page int) (Page, error) {
	q := url.Values{"sort_by": {"popularity.desc"}, "include_adult": {"false"}, "with_networks": {strconv.FormatInt(network, 10)}}
	return c.list(ctx, "/discover/tv", KindSeries, q, page)
}

// Genre is one of TMDB's genres.
type Genre struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Genres lists the genres of movies or shows.
func (c *Client) Genres(ctx context.Context, kind string) ([]Genre, error) {
	var res struct {
		Genres []Genre `json:"genres"`
	}
	if err := c.get(ctx, "/genre/"+apiKind(kind)+"/list", nil, &res); err != nil {
		return nil, err
	}
	return res.Genres, nil
}

// Network is a TV network or streaming service.
type Network struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Logo string `json:"logo,omitempty"`
}

// Network describes a network, with its logo.
func (c *Client) Network(ctx context.Context, id int64) (Network, error) {
	var res struct {
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		LogoPath string `json:"logo_path"`
	}
	if err := c.get(ctx, "/network/"+strconv.FormatInt(id, 10), nil, &res); err != nil {
		return Network{}, err
	}
	return Network{ID: res.ID, Name: res.Name, Logo: Image(res.LogoPath, "w300")}, nil
}
