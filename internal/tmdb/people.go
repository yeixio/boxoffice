package tmdb

import (
	"cmp"
	"context"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// Person is an actor (or director, writer…) as TMDB's person search
// describes them.
type Person struct {
	TMDBID int64  `json:"tmdbId"`
	Name   string `json:"name"`
	Photo  string `json:"photo,omitempty"`
	// Department is what they're known for, such as Acting or Directing.
	Department string `json:"department,omitempty"`
	// KnownFor names a few of their best-known titles.
	KnownFor   []string `json:"knownFor"`
	Popularity float64  `json:"-"`
}

// SearchPeople finds people by name, best known first.
func (c *Client) SearchPeople(ctx context.Context, term string) ([]Person, error) {
	var res struct {
		Results []struct {
			ID                 int64        `json:"id"`
			Name               string       `json:"name"`
			ProfilePath        string       `json:"profile_path"`
			KnownForDepartment string       `json:"known_for_department"`
			Popularity         float64      `json:"popularity"`
			Adult              bool         `json:"adult"`
			KnownFor           []listResult `json:"known_for"`
		} `json:"results"`
	}
	if err := c.get(ctx, "/search/person", url.Values{"query": {term}, "include_adult": {"false"}}, &res); err != nil {
		return nil, err
	}
	out := []Person{}
	for _, r := range res.Results {
		if r.Adult {
			continue
		}
		p := Person{TMDBID: r.ID, Name: r.Name, Photo: Image(r.ProfilePath, "w185"), Department: r.KnownForDepartment,
			KnownFor: []string{}, Popularity: r.Popularity}
		for _, k := range r.KnownFor {
			if k.Adult {
				continue
			}
			p.KnownFor = append(p.KnownFor, cmp.Or(k.Title, k.Name))
		}
		out = append(out, p)
	}
	return out, nil
}

// Filmography is a person's movies and shows.
type Filmography struct {
	Person Person
	// Titles are what they're in (or, for someone known for directing,
	// what they directed), best known first. Talk shows, news and
	// reality TV, and parts as themselves, are left out.
	Titles []Title
}

// Genres of TV credits that aren't parts: talk shows, news, reality.
var notParts = []int64{10763, 10764, 10767}

type credit struct {
	listResult
	Character string  `json:"character"`
	Job       string  `json:"job"`
	VoteCount int     `json:"vote_count"`
	GenreIDs  []int64 `json:"genre_ids"`
}

// Credits is a person's filmography.
func (c *Client) Credits(ctx context.Context, personID int64) (Filmography, error) {
	var res struct {
		ID                 int64  `json:"id"`
		Name               string `json:"name"`
		ProfilePath        string `json:"profile_path"`
		KnownForDepartment string `json:"known_for_department"`
		Credits            struct {
			Cast []credit `json:"cast"`
			Crew []credit `json:"crew"`
		} `json:"combined_credits"`
	}
	if err := c.get(ctx, "/person/"+strconv.FormatInt(personID, 10), url.Values{"append_to_response": {"combined_credits"}}, &res); err != nil {
		return Filmography{}, err
	}
	f := Filmography{Person: Person{TMDBID: res.ID, Name: res.Name, Photo: Image(res.ProfilePath, "w185"),
		Department: res.KnownForDepartment, KnownFor: []string{}}}
	credits := res.Credits.Cast
	if res.KnownForDepartment == "Directing" {
		credits = slices.DeleteFunc(slices.Clone(res.Credits.Crew), func(c credit) bool { return c.Job != "Director" })
	}
	type ranked struct {
		t     Title
		votes int
	}
	var all []ranked
	seen := map[string]bool{}
	for _, cr := range credits {
		if cr.Adult || slices.ContainsFunc(cr.GenreIDs, func(g int64) bool { return slices.Contains(notParts, g) }) || asSelf(cr.Character) {
			continue
		}
		t := Title{TMDBID: cr.ID, Overview: cr.Overview, Poster: Image(cr.PosterPath, "w342"), Backdrop: Image(cr.BackdropPath, "w1280"),
			Rating: cr.VoteAverage, Popularity: cr.Popularity}
		switch cr.MediaType {
		case "movie":
			t.Kind, t.Title, t.Date = KindMovie, cr.Title, cr.ReleaseDate
		case "tv":
			t.Kind, t.Title, t.Date = KindSeries, cr.Name, cr.FirstAirDate
		default:
			continue
		}
		key := t.Kind + ":" + strconv.FormatInt(t.TMDBID, 10)
		if seen[key] {
			continue
		}
		seen[key] = true
		t.Year = year(t.Date)
		all = append(all, ranked{t, cr.VoteCount})
	}
	slices.SortStableFunc(all, func(a, b ranked) int {
		return cmp.Or(cmp.Compare(b.votes, a.votes), cmp.Compare(b.t.Popularity, a.t.Popularity))
	})
	f.Titles = make([]Title, 0, len(all))
	for _, r := range all {
		f.Titles = append(f.Titles, r.t)
	}
	return f, nil
}

// asSelf says a part is someone playing themselves, as in a documentary
// or an awards show.
func asSelf(character string) bool {
	c := strings.ToLower(character)
	for _, w := range []string{"self", "himself", "herself", "themselves", "themself"} {
		if c == w || strings.HasPrefix(c, w+" ") || strings.HasPrefix(c, w+" -") || strings.HasPrefix(c, w+" (") {
			return true
		}
	}
	return false
}
