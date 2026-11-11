package arrapi

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/quality"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/tmdb"
)

func images(poster, backdrop string) []map[string]string {
	out := []map[string]string{}
	if poster != "" {
		out = append(out, map[string]string{"coverType": "poster", "url": poster, "remoteUrl": poster})
	}
	if backdrop != "" {
		out = append(out, map[string]string{"coverType": "fanart", "url": backdrop, "remoteUrl": backdrop})
	}
	return out
}

func slug(title string, id int64) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-") + "-" + strconv.FormatInt(id, 10)
}

func stamp(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

func qualityJSON(id int) map[string]any {
	q, _ := quality.ByID(id)
	return map[string]any{"quality": map[string]any{"id": q.ID, "name": q.Name, "source": q.Source, "resolution": q.Resolution}}
}

// movieStatus is Radarr's word for where a movie is in its life.
func movieStatus(m store.Movie) string {
	now := time.Now()
	switch {
	case m.DigitalRelease != nil && m.DigitalRelease.Before(now), m.PhysicalRelease != nil && m.PhysicalRelease.Before(now):
		return "released"
	case m.InCinemas != nil && m.InCinemas.Before(now):
		return "inCinemas"
	}
	return "announced"
}

func (h *Handler) movieJSON(m store.Movie, ids ids) map[string]any {
	out := map[string]any{
		"id": m.ID, "tmdbId": m.TMDBID, "imdbId": m.IMDBID, "title": m.Title, "originalTitle": m.Title, "sortTitle": m.SortTitle,
		"year": m.Year, "overview": m.Overview, "images": images(m.Poster, m.Backdrop), "runtime": m.Runtime,
		"status": movieStatus(m), "inCinemas": stamp(m.InCinemas), "digitalRelease": stamp(m.DigitalRelease), "physicalRelease": stamp(m.PhysicalRelease),
		"monitored": m.Monitored, "qualityProfileId": ids.toApp(m.ProfileID), "path": m.Path, "rootFolderPath": m.RootFolder,
		"folderName": filepath.Base(m.Path), "minimumAvailability": "released", "titleSlug": slug(m.Title, m.TMDBID),
		"added": m.AddedAt.Format(time.RFC3339), "hasFile": m.File != nil, "isAvailable": movieStatus(m) == "released",
		"sizeOnDisk": 0, "genres": []string{}, "tags": []int{},
	}
	if m.File != nil {
		out["sizeOnDisk"] = m.File.Size
		out["movieFile"] = map[string]any{
			"id": m.File.ID, "movieId": m.ID, "path": m.File.Path, "relativePath": rel(m.Path, m.File.Path),
			"size": m.File.Size, "quality": qualityJSON(m.File.Quality), "dateAdded": m.File.AddedAt.Format(time.RFC3339),
		}
	}
	return out
}

func rel(dir, path string) string {
	if r, err := filepath.Rel(dir, path); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return filepath.Base(path)
}

// lookupJSON describes a movie found online the way Radarr's lookup does:
// with its id and settings when it's already added.
func (h *Handler) lookupMovieJSON(ctx context.Context, m tmdb.Movie, ids ids) map[string]any {
	if have, err := h.Catalog.Store.MovieByTMDB(ctx, m.TMDBID); err == nil {
		return h.movieJSON(have, ids)
	}
	sm := store.Movie{TMDBID: m.TMDBID, IMDBID: m.IMDBID, Title: m.Title, SortTitle: store.SortTitle(m.Title), Year: m.Year, Overview: m.Overview,
		Poster: m.Poster, Backdrop: m.Backdrop, Runtime: m.Runtime, InCinemas: m.InCinemas, DigitalRelease: m.DigitalRelease, PhysicalRelease: m.PhysicalRelease}
	out := h.movieJSON(sm, ids)
	delete(out, "id")
	delete(out, "path")
	delete(out, "rootFolderPath")
	out["folderName"] = catalog.FolderName(m.Title, m.Year)
	out["monitored"] = false
	out["qualityProfileId"] = 0
	return out
}

func (h *Handler) movies(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	ids, err := h.profileIDs(ctx)
	if err != nil {
		return err
	}
	var list []store.Movie
	if t := r.URL.Query().Get("tmdbId"); t != "" {
		id, _ := strconv.ParseInt(t, 10, 64)
		list = []store.Movie{}
		if m, err := h.Catalog.Store.MovieByTMDB(ctx, id); err == nil {
			list = append(list, m)
		}
	} else if list, err = h.Catalog.Store.Movies(ctx); err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(list))
	for _, m := range list {
		out = append(out, h.movieJSON(m, ids))
	}
	return write(w, http.StatusOK, out)
}

func (h *Handler) movie(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	m, err := h.Catalog.Store.Movie(r.Context(), id)
	if err != nil {
		return err
	}
	ids, err := h.profileIDs(r.Context())
	if err != nil {
		return err
	}
	return write(w, http.StatusOK, h.movieJSON(m, ids))
}

func (h *Handler) lookupMovies(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	term := strings.TrimSpace(r.URL.Query().Get("term"))
	if term == "" {
		return write(w, http.StatusOK, []any{})
	}
	var found []tmdb.Movie
	var err error
	if imdb, ok := strings.CutPrefix(strings.ToLower(term), "imdb:"); ok {
		var m tmdb.Movie
		if m, err = h.Catalog.TMDB(ctx).MovieByIMDB(ctx, strings.TrimSpace(imdb)); err == nil {
			found = []tmdb.Movie{m}
		} else if errors.Is(err, tmdb.ErrNotFound) {
			err = nil
		}
	} else {
		found, err = h.Catalog.TMDB(ctx).SearchMovies(ctx, term)
	}
	if err != nil {
		return err
	}
	ids, err := h.profileIDs(ctx)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(found))
	for _, m := range found {
		out = append(out, h.lookupMovieJSON(ctx, m, ids))
	}
	return write(w, http.StatusOK, out)
}

func (h *Handler) lookupMovieTMDB(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id, _ := strconv.ParseInt(r.URL.Query().Get("tmdbId"), 10, 64)
	if id <= 0 {
		return errNotFound
	}
	m, err := h.Catalog.TMDB(ctx).Movie(ctx, id)
	if errors.Is(err, tmdb.ErrNotFound) {
		return errNotFound
	}
	if err != nil {
		return err
	}
	ids, err := h.profileIDs(ctx)
	if err != nil {
		return err
	}
	return write(w, http.StatusOK, h.lookupMovieJSON(ctx, m, ids))
}

func (h *Handler) lookupMovieIMDB(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	m, err := h.Catalog.TMDB(ctx).MovieByIMDB(ctx, r.URL.Query().Get("imdbId"))
	if errors.Is(err, tmdb.ErrNotFound) {
		return errNotFound
	}
	if err != nil {
		return err
	}
	ids, err := h.profileIDs(ctx)
	if err != nil {
		return err
	}
	return write(w, http.StatusOK, h.lookupMovieJSON(ctx, m, ids))
}

// titleInput is what a client sends to add or edit a movie or show.
type titleInput struct {
	TMDBID           int64  `json:"tmdbId"`
	TVDBID           int64  `json:"tvdbId"`
	Title            string `json:"title"`
	QualityProfileID int    `json:"qualityProfileId"`
	RootFolderPath   string `json:"rootFolderPath"`
	Path             string `json:"path"`
	Monitored        *bool  `json:"monitored"`
	Seasons          []struct {
		SeasonNumber int  `json:"seasonNumber"`
		Monitored    bool `json:"monitored"`
	} `json:"seasons"`
	AddOptions struct {
		SearchForMovie           bool   `json:"searchForMovie"`
		SearchForMissingEpisodes bool   `json:"searchForMissingEpisodes"`
		Monitor                  string `json:"monitor"`
	} `json:"addOptions"`
}

func (t titleInput) monitored() bool { return t.Monitored == nil || *t.Monitored }

func (h *Handler) addMovie(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	var in titleInput
	if err := read(r, &in); err != nil {
		return err
	}
	if in.TMDBID <= 0 {
		return invalid("TmdbId", "'Tmdb Id' must be greater than '0'.")
	}
	ids, err := h.profileIDs(ctx)
	if err != nil {
		return err
	}
	o := catalog.AddOptions{RootFolder: in.RootFolderPath, Path: in.Path, Monitored: in.monitored(), Search: in.AddOptions.SearchForMovie}
	if in.QualityProfileID > 0 {
		o.ProfileID = ids.fromApp(in.QualityProfileID)
	}
	m, err := h.Catalog.AddMovie(ctx, in.TMDBID, o)
	if errors.Is(err, catalog.ErrExists) {
		return invalid("TmdbId", "This movie has already been added")
	}
	if err != nil {
		return err
	}
	return write(w, http.StatusCreated, h.movieJSON(m, ids))
}

func (h *Handler) editMovie(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id, err := pathID(r)
	if err != nil {
		return err
	}
	m, err := h.Catalog.Store.Movie(ctx, id)
	if err != nil {
		return err
	}
	var in titleInput
	if err := read(r, &in); err != nil {
		return err
	}
	ids, err := h.profileIDs(ctx)
	if err != nil {
		return err
	}
	if in.QualityProfileID > 0 {
		pid := ids.fromApp(in.QualityProfileID)
		if _, err := h.Catalog.Store.Profile(ctx, pid); err != nil {
			return invalid("QualityProfileId", "there's no such quality profile")
		}
		m.ProfileID = pid
	}
	if in.Monitored != nil {
		m.Monitored = *in.Monitored
	}
	if in.Path != "" {
		m.Path = filepath.Clean(in.Path)
	}
	if in.RootFolderPath != "" {
		m.RootFolder = filepath.Clean(in.RootFolderPath)
	}
	if err := h.Catalog.Store.UpdateMovie(ctx, m); err != nil {
		return err
	}
	if m, err = h.Catalog.Store.Movie(ctx, id); err != nil {
		return err
	}
	return write(w, http.StatusAccepted, h.movieJSON(m, ids))
}

// deleteMovie takes a movie off the wanted list, cancelling its downloads.
// Its files stay where they are, whatever deleteFiles says: Box Office
// never deletes a library.
func (h *Handler) deleteMovie(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	t, err := h.Catalog.Store.Movie(r.Context(), id)
	if err != nil {
		return err
	}
	if err := h.Catalog.Store.DeleteMovie(r.Context(), id); err != nil {
		return err
	}
	if h.Pairing != nil && h.Pairing.Removed != nil {
		h.Pairing.Removed(r.Context(), "movie", id, t.Title)
	}
	w.WriteHeader(http.StatusOK)
	return nil
}

// calendar is Radarr's or Sonarr's calendar, depending on the app.
func (h *Handler) calendar(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	q := r.URL.Query()
	from, to := parseTime(q.Get("start"), time.Now().AddDate(0, 0, -1)), parseTime(q.Get("end"), time.Now().AddDate(0, 0, 7))
	if h.App == Radarr {
		movies, err := h.Catalog.Store.MoviesReleasing(ctx, from, to)
		if err != nil {
			return err
		}
		ids, err := h.profileIDs(ctx)
		if err != nil {
			return err
		}
		out := make([]map[string]any, 0, len(movies))
		for _, m := range movies {
			out = append(out, h.movieJSON(m, ids))
		}
		return write(w, http.StatusOK, out)
	}
	eps, err := h.Catalog.Store.EpisodesAiring(ctx, from, to)
	if err != nil {
		return err
	}
	withSeries := q.Get("includeSeries") == "true"
	ids, err := h.profileIDs(ctx)
	if err != nil {
		return err
	}
	shows := map[int64]map[string]any{}
	out := make([]map[string]any, 0, len(eps))
	for _, e := range eps {
		j := episodeJSON(e)
		if withSeries {
			s, ok := shows[e.SeriesID]
			if !ok {
				t, err := h.Catalog.Store.Series(ctx, e.SeriesID)
				if err != nil {
					return err
				}
				s = h.seriesJSON(t, ids)
				shows[e.SeriesID] = s
			}
			j["series"] = s
		}
		out = append(out, j)
	}
	return write(w, http.StatusOK, out)
}

func parseTime(s string, def time.Time) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return def
}
