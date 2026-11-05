// Package tmdbtest is a pretend TMDB for tests, with a few movies and shows.
package tmdbtest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Key is the API key the fake accepts.
const Key = "0123456789abcdef0123456789abcdef"

// Fake serves the TMDB API from fixed data.
type Fake struct {
	URL      string
	Movies   map[int64]map[string]any
	Shows    map[int64]map[string]any
	Episodes map[string][]map[string]any // "show/season"
	// Raw answers any other path (without the leading slash) as it is,
	// such as "trending/all/week" or "genre/movie/list".
	Raw map[string]any

	mu   sync.Mutex
	hits map[string]int
}

// Hits is how many times a path was asked for.
func (f *Fake) Hits(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[strings.Trim(path, "/")]
}

// List is a list's answer: these titles, on one of pages pages.
func List(pages int, results ...map[string]any) map[string]any {
	if results == nil {
		results = []map[string]any{}
	}
	return map[string]any{"results": results, "total_pages": pages, "page": 1}
}

// Date is a day relative to today, as TMDB writes dates.
func Date(days int) string { return time.Now().AddDate(0, 0, days).Format("2006-01-02") }

// New starts a fake with The Matrix (603), a movie out in a week (1001),
// and Severance (95396, TVDB 371980) with two seasons, the second airing.
func New(t *testing.T) *Fake {
	f := &Fake{
		Movies: map[int64]map[string]any{
			603: {"id": 603, "imdb_id": "tt0133093", "title": "The Matrix", "release_date": "1999-03-30", "overview": "A hacker learns the truth.",
				"poster_path": "/matrix.jpg", "runtime": 136, "status": "Released",
				"release_dates": map[string]any{"results": []any{map[string]any{"iso_3166_1": "US", "release_dates": []any{
					map[string]any{"type": 3, "release_date": "1999-03-31T00:00:00.000Z"},
					map[string]any{"type": 5, "release_date": "1999-09-21T00:00:00.000Z"}}}}}},
			1001: {"id": 1001, "title": "Coming Attraction", "release_date": Date(7), "overview": "Soon.", "poster_path": "/soon.jpg", "status": "Post Production",
				"release_dates": map[string]any{"results": []any{map[string]any{"iso_3166_1": "US", "release_dates": []any{
					map[string]any{"type": 3, "release_date": Date(7) + "T00:00:00.000Z"},
					map[string]any{"type": 4, "release_date": Date(30) + "T00:00:00.000Z"}}}}}},
		},
		Shows: map[int64]map[string]any{
			95396: {"id": 95396, "name": "Severance", "first_air_date": "2022-02-18", "overview": "Work and life, split.", "poster_path": "/sev.jpg",
				"status": "Returning Series", "episode_run_time": []int{55}, "networks": []any{map[string]any{"name": "Apple TV+"}},
				"seasons": []any{map[string]any{"season_number": 0, "episode_count": 1}, map[string]any{"season_number": 1, "episode_count": 2},
					map[string]any{"season_number": 2, "episode_count": 2}},
				"external_ids": map[string]any{"tvdb_id": 371980, "imdb_id": "tt11280740"}},
		},
		Raw: map[string]any{},
		Episodes: map[string][]map[string]any{
			"95396/0": {{"season_number": 0, "episode_number": 1, "name": "Special", "air_date": "2022-01-01"}},
			"95396/1": {{"season_number": 1, "episode_number": 1, "name": "Good News About Hell", "air_date": "2022-02-18"},
				{"season_number": 1, "episode_number": 2, "name": "Half Loop", "air_date": "2022-02-18"}},
			"95396/2": {{"season_number": 2, "episode_number": 1, "name": "Hello, Ms. Cobel", "air_date": Date(-3)},
				{"season_number": 2, "episode_number": 2, "name": "Goodbye, Mrs. Selvig", "air_date": Date(4)}},
		},
	}
	ts := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(ts.Close)
	f.URL = ts.URL
	return f
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("api_key") != Key {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	path := strings.Trim(r.URL.Path, "/")
	f.mu.Lock()
	if f.hits == nil {
		f.hits = map[string]int{}
	}
	f.hits[path]++
	raw, ok := f.Raw[path]
	f.mu.Unlock()
	if ok {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(raw)
		return
	}
	parts := strings.Split(path, "/")
	write := func(v any) { w.Header().Set("Content-Type", "application/json"); json.NewEncoder(w).Encode(v) }
	id := func(i int) int64 {
		if i >= len(parts) {
			return 0
		}
		n, _ := strconv.ParseInt(parts[i], 10, 64)
		return n
	}
	q := strings.ToLower(r.URL.Query().Get("query"))
	switch {
	case parts[0] == "configuration":
		write(map[string]any{})
	case parts[0] == "search" && parts[1] == "movie":
		res := []any{}
		for _, m := range f.Movies {
			if strings.Contains(strings.ToLower(m["title"].(string)), q) {
				res = append(res, m)
			}
		}
		write(map[string]any{"results": res})
	case parts[0] == "search" && parts[1] == "tv":
		res := []any{}
		for _, s := range f.Shows {
			if strings.Contains(strings.ToLower(s["name"].(string)), q) {
				res = append(res, s)
			}
		}
		write(map[string]any{"results": res})
	case parts[0] == "movie" && len(parts) == 2:
		if m, ok := f.Movies[id(1)]; ok {
			write(m)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	case parts[0] == "tv" && len(parts) == 2:
		if s, ok := f.Shows[id(1)]; ok {
			write(s)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	case parts[0] == "tv" && len(parts) == 4 && parts[2] == "season":
		if eps, ok := f.Episodes[parts[1]+"/"+parts[3]]; ok {
			write(map[string]any{"episodes": eps})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	case parts[0] == "find":
		res := map[string]any{"movie_results": []any{}, "tv_results": []any{}}
		switch r.URL.Query().Get("external_source") {
		case "tvdb_id":
			for sid, s := range f.Shows {
				if ext, _ := s["external_ids"].(map[string]any); ext != nil && strconv.Itoa(ext["tvdb_id"].(int)) == parts[1] {
					res["tv_results"] = []any{map[string]any{"id": sid}}
				}
			}
		case "imdb_id":
			for mid, m := range f.Movies {
				if m["imdb_id"] == parts[1] {
					res["movie_results"] = []any{map[string]any{"id": mid}}
				}
			}
		}
		write(res)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}
