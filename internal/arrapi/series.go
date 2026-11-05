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
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/tmdb"
)

// seriesStatus is Sonarr's word for whether a show is still going.
func seriesStatus(s string) string {
	switch strings.ToLower(s) {
	case "ended", "canceled":
		return "ended"
	case "planned", "in production", "pilot":
		return "upcoming"
	}
	return "continuing"
}

func (h *Handler) seriesJSON(t store.Series, ids ids) map[string]any {
	seasons := make([]map[string]any, 0, len(t.Seasons))
	for _, se := range t.Seasons {
		seasons = append(seasons, map[string]any{"seasonNumber": se.Number, "monitored": se.Monitored})
	}
	pct := 0.0
	if t.EpisodeCount > 0 {
		pct = 100 * float64(t.FileCount) / float64(t.EpisodeCount)
	}
	return map[string]any{
		"id": t.ID, "tvdbId": t.TVDBID, "tmdbId": t.TMDBID, "imdbId": t.IMDBID, "title": t.Title, "sortTitle": t.SortTitle,
		"year": t.Year, "overview": t.Overview, "images": images(t.Poster, t.Backdrop), "status": seriesStatus(t.Status),
		"network": t.Network, "runtime": t.Runtime, "seasons": seasons, "monitored": t.Monitored,
		"qualityProfileId": ids.toApp(t.ProfileID), "languageProfileId": 1, "seasonFolder": true, "seriesType": "standard",
		"path": t.Path, "rootFolderPath": t.RootFolder, "titleSlug": slug(t.Title, t.TMDBID), "added": t.AddedAt.Format(time.RFC3339),
		"tags": []int{}, "genres": []string{},
		"statistics": map[string]any{"seasonCount": len(t.Seasons), "episodeCount": t.EpisodeCount, "episodeFileCount": t.FileCount,
			"totalEpisodeCount": t.EpisodeCount, "percentOfEpisodes": pct, "sizeOnDisk": t.SizeOnDisk},
	}
}

func episodeJSON(e store.Episode) map[string]any {
	out := map[string]any{
		"id": e.ID, "seriesId": e.SeriesID, "seasonNumber": e.Season, "episodeNumber": e.Number, "title": e.Title,
		"overview": e.Overview, "monitored": e.Monitored, "hasFile": e.File != nil, "episodeFileId": 0,
		"airDateUtc": stamp(e.AirDate), "airDate": nil,
	}
	if e.AirDate != nil {
		out["airDate"] = e.AirDate.Format("2006-01-02")
	}
	if e.File != nil {
		out["episodeFileId"] = e.File.ID
	}
	return out
}

func (h *Handler) lookupSeriesJSON(ctx context.Context, s tmdb.Show, ids ids) map[string]any {
	if have, err := h.Catalog.Store.SeriesByTMDB(ctx, s.TMDBID); err == nil {
		return h.seriesJSON(have, ids)
	}
	t := store.Series{TMDBID: s.TMDBID, TVDBID: s.TVDBID, IMDBID: s.IMDBID, Title: s.Title, SortTitle: store.SortTitle(s.Title), Year: s.Year,
		Overview: s.Overview, Poster: s.Poster, Backdrop: s.Backdrop, Status: s.Status, Network: s.Network, Runtime: s.Runtime}
	for _, se := range s.Seasons {
		t.Seasons = append(t.Seasons, store.Season{Number: se.Number, Monitored: se.Number > 0})
	}
	out := h.seriesJSON(t, ids)
	delete(out, "id")
	delete(out, "path")
	delete(out, "rootFolderPath")
	out["folder"] = catalog.FolderName(s.Title, s.Year)
	out["monitored"] = false
	out["qualityProfileId"] = 0
	return out
}

func (h *Handler) seriesList(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	ids, err := h.profileIDs(ctx)
	if err != nil {
		return err
	}
	var list []store.Series
	switch q := r.URL.Query(); {
	case q.Get("tvdbId") != "":
		id, _ := strconv.ParseInt(q.Get("tvdbId"), 10, 64)
		list = []store.Series{}
		if t, err := h.Catalog.Store.SeriesByTVDB(ctx, id); err == nil {
			list = append(list, t)
		}
	case q.Get("tmdbId") != "":
		id, _ := strconv.ParseInt(q.Get("tmdbId"), 10, 64)
		list = []store.Series{}
		if t, err := h.Catalog.Store.SeriesByTMDB(ctx, id); err == nil {
			list = append(list, t)
		}
	default:
		if list, err = h.Catalog.Store.SeriesAll(ctx); err != nil {
			return err
		}
	}
	out := make([]map[string]any, 0, len(list))
	for _, t := range list {
		out = append(out, h.seriesJSON(t, ids))
	}
	return write(w, http.StatusOK, out)
}

func (h *Handler) series(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	t, err := h.Catalog.Store.Series(r.Context(), id)
	if err != nil {
		return err
	}
	ids, err := h.profileIDs(r.Context())
	if err != nil {
		return err
	}
	return write(w, http.StatusOK, h.seriesJSON(t, ids))
}

func (h *Handler) lookupSeries(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	term := strings.TrimSpace(r.URL.Query().Get("term"))
	if term == "" {
		return write(w, http.StatusOK, []any{})
	}
	found, err := h.Catalog.TMDB(ctx).SearchShows(ctx, term)
	if err != nil {
		return err
	}
	ids, err := h.profileIDs(ctx)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(found))
	for _, s := range found {
		out = append(out, h.lookupSeriesJSON(ctx, s, ids))
	}
	return write(w, http.StatusOK, out)
}

func (h *Handler) addSeries(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	var in titleInput
	if err := read(r, &in); err != nil {
		return err
	}
	c := h.Catalog.TMDB(ctx)
	var info tmdb.Show
	var err error
	switch {
	case in.TMDBID > 0:
		info, err = c.Show(ctx, in.TMDBID)
	case in.TVDBID > 0:
		info, err = c.ShowByTVDB(ctx, in.TVDBID)
	default:
		return invalid("TvdbId", "'Tvdb Id' must be greater than '0'.")
	}
	if errors.Is(err, tmdb.ErrNotFound) {
		return invalid("TvdbId", "TMDB doesn't know this show")
	}
	if err != nil {
		return err
	}
	if _, err := h.Catalog.Store.SeriesByTMDB(ctx, info.TMDBID); err == nil {
		return invalid("TvdbId", "This series has already been added")
	}
	ids, err := h.profileIDs(ctx)
	if err != nil {
		return err
	}
	o := catalog.AddOptions{RootFolder: in.RootFolderPath, Path: in.Path, Monitored: in.monitored(),
		Search: in.AddOptions.SearchForMissingEpisodes}
	if in.QualityProfileID > 0 {
		o.ProfileID = ids.fromApp(in.QualityProfileID)
	}
	var seasons map[int]bool
	o.Monitor, seasons = addMonitor(in)
	t, err := h.Catalog.AddSeriesInfo(ctx, info, o, seasons)
	if errors.Is(err, catalog.ErrExists) {
		return invalid("TvdbId", "This series has already been added")
	}
	if err != nil {
		return err
	}
	return write(w, http.StatusCreated, h.seriesJSON(t, ids))
}

func (h *Handler) editSeries(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id, err := pathID(r)
	if err != nil {
		return err
	}
	t, err := h.Catalog.Store.Series(ctx, id)
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
		t.ProfileID = pid
	}
	if in.Monitored != nil {
		t.Monitored = *in.Monitored
	}
	if in.Path != "" {
		t.Path = filepath.Clean(in.Path)
	}
	if in.RootFolderPath != "" {
		t.RootFolder = filepath.Clean(in.RootFolderPath)
	}
	if err := h.Catalog.Store.UpdateSeries(ctx, t); err != nil {
		return err
	}
	for _, se := range in.Seasons {
		if err := h.Catalog.Store.SetSeason(ctx, id, store.Season{Number: se.SeasonNumber, Monitored: se.Monitored}); err != nil {
			return err
		}
	}
	if t, err = h.Catalog.Store.Series(ctx, id); err != nil {
		return err
	}
	return write(w, http.StatusAccepted, h.seriesJSON(t, ids))
}

// deleteSeries takes a show off the wanted list, leaving its files alone.
func (h *Handler) deleteSeries(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	if err := h.Catalog.Store.DeleteSeries(r.Context(), id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusOK)
	return nil
}

func (h *Handler) episodes(w http.ResponseWriter, r *http.Request) error {
	id, _ := strconv.ParseInt(r.URL.Query().Get("seriesId"), 10, 64)
	if id <= 0 {
		return invalid("SeriesId", "seriesId is required")
	}
	eps, err := h.Catalog.Store.Episodes(r.Context(), id)
	if err != nil {
		return err
	}
	season, filter := 0, false
	if s := r.URL.Query().Get("seasonNumber"); s != "" {
		season, _ = strconv.Atoi(s)
		filter = true
	}
	out := make([]map[string]any, 0, len(eps))
	for _, e := range eps {
		if filter && e.Season != season {
			continue
		}
		out = append(out, episodeJSON(e))
	}
	return write(w, http.StatusOK, out)
}

func (h *Handler) episode(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	e, err := h.Catalog.Store.Episode(r.Context(), id)
	if err != nil {
		return err
	}
	return write(w, http.StatusOK, episodeJSON(e))
}

// addMonitor turns a Sonarr client's addOptions.monitor and season list
// into Box Office's rule. Sonarr's rule wins over the season list, as in
// Sonarr. "all", or no rule with every regular season switched on (how
// Matinee asks for a show), means the client made no real choice, so the
// show gets the default from Settings: on a long show, only the last few
// seasons. A list that picks some seasons is kept as it is.
func addMonitor(in titleInput) (string, map[int]bool) {
	var seasons map[int]bool
	everything := true
	for _, se := range in.Seasons {
		if seasons == nil {
			seasons = map[int]bool{}
		}
		seasons[se.SeasonNumber] = se.Monitored
		if se.SeasonNumber > 0 && !se.Monitored {
			everything = false
		}
	}
	switch m := strings.ToLower(in.AddOptions.Monitor); m {
	case "all":
		return "", nil
	case "", "unknown":
		if everything {
			return "", nil
		}
		return "", seasons
	case "latestseason", "lastseason", catalog.MonitorLatest:
		return catalog.MonitorLatest, nil
	case catalog.MonitorFuture, catalog.MonitorNone, catalog.MonitorRecent:
		return m, nil
	default: // missing, existing, firstSeason, pilot and the like
		if seasons == nil {
			return catalog.MonitorAll, nil
		}
		return catalog.MonitorAll, seasons
	}
}
