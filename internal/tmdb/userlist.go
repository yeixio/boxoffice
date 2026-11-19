package tmdb

import (
	"context"
	"net/url"
	"strconv"
)

// UserList reads a public list someone keeps on TMDB: its name and the
// movies and shows on it, at most limit of them.
func (c *Client) UserList(ctx context.Context, id string, limit int) (string, []Title, error) {
	var name string
	var out []Title
	for page := 1; ; page++ {
		var res struct {
			Name       string       `json:"name"`
			Items      []listResult `json:"items"`
			TotalPages int          `json:"total_pages"`
		}
		if err := c.get(ctx, "/list/"+url.PathEscape(id), url.Values{"page": {strconv.Itoa(page)}}, &res); err != nil {
			return "", nil, err
		}
		name = res.Name
		for _, r := range res.Items {
			t := Title{TMDBID: r.ID, Poster: Image(r.PosterPath, "w342")}
			switch r.MediaType {
			case "movie", "":
				t.Kind, t.Title, t.Date = KindMovie, r.Title, r.ReleaseDate
			case "tv":
				t.Kind, t.Title, t.Date = KindSeries, r.Name, r.FirstAirDate
			default:
				continue
			}
			t.Year = year(t.Date)
			out = append(out, t)
			if len(out) >= limit {
				return name, out, nil
			}
		}
		if page >= res.TotalPages || len(res.Items) == 0 {
			return name, out, nil
		}
	}
}

// Find looks a title up by the id another site knows it by: source is
// imdb_id or tvdb_id. It gives the TMDB id of the movie or of the show,
// whichever TMDB has, or ErrNotFound.
func (c *Client) Find(ctx context.Context, source, id string) (movie, show int64, err error) {
	var res struct {
		Movies []struct {
			ID int64 `json:"id"`
		} `json:"movie_results"`
		TV []struct {
			ID int64 `json:"id"`
		} `json:"tv_results"`
	}
	if err := c.get(ctx, "/find/"+url.PathEscape(id), url.Values{"external_source": {source}}, &res); err != nil {
		return 0, 0, err
	}
	if len(res.Movies) > 0 {
		movie = res.Movies[0].ID
	}
	if len(res.TV) > 0 {
		show = res.TV[0].ID
	}
	if movie == 0 && show == 0 {
		return 0, 0, ErrNotFound
	}
	return movie, show, nil
}
