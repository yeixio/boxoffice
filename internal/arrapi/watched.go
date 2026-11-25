package arrapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
)

// watched is PUT /api/v3/boxoffice/watched: Matinee says what has been
// watched, so Box Office's clean-up rules and space saver can go by it. /radarr takes the
// movies and /sonarr the shows. Each report is the whole picture for its
// kind and replaces the last one: a title left out has no plays. Titles
// Box Office hasn't got are passed over.
//
//	{"people": 4, "titles": [{"tmdbId": 603, "lastPlayed": "2026-11-01T20:00:00Z",
//	  "viewers": 2, "finishedBy": 1, "finishedAt": "2026-10-30T21:10:00Z"}]}
//
// A show is named by tvdbId or tmdbId, and is finished by someone who has
// played every episode Matinee has of it. A show can also say the latest
// episode anyone has started, specials left out, as furthestSeason and
// furthestEpisode: space saver fetches the next ones past it. And the
// latest episode everyone who has played any of it has finished, as
// allFinishedSeason and allFinishedEpisode: episodes up to it can be
// removed, where that's turned on.
func (h *Handler) watched(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		People int `json:"people"`
		Titles []struct {
			TMDBID     int64      `json:"tmdbId"`
			TVDBID     int64      `json:"tvdbId"`
			LastPlayed *time.Time `json:"lastPlayed"`
			Viewers    int        `json:"viewers"`
			FinishedBy int        `json:"finishedBy"`
			FinishedAt *time.Time `json:"finishedAt"`
			// Shows only.
			FurthestSeason  int `json:"furthestSeason"`
			FurthestEpisode int `json:"furthestEpisode"`
			// The latest episode everyone watching has finished.
			AllFinishedSeason  int `json:"allFinishedSeason"`
			AllFinishedEpisode int `json:"allFinishedEpisode"`
		} `json:"titles"`
	}
	if err := read(r, &in); err != nil {
		return err
	}
	if in.People < 0 {
		return invalid("people", "people is how many people Matinee has")
	}
	ctx := r.Context()
	st := h.Catalog.Store
	kind := "movie"
	if h.App == Sonarr {
		kind = "series"
	}
	// A time in the future (a wrong clock) would make a title look newly
	// played for ever; it counts as now.
	now := time.Now()
	clamp := func(t *time.Time) *time.Time {
		if t != nil && t.After(now) {
			return &now
		}
		return t
	}
	rows := make([]store.Watched, 0, len(in.Titles))
	seen := map[int64]bool{}
	for _, t := range in.Titles {
		if t.Viewers < 0 || t.FinishedBy < 0 || t.FinishedBy > t.Viewers {
			return invalid("titles", "viewers and finishedBy count people, and nobody finishes without playing")
		}
		var id int64
		var err error
		if kind == "movie" {
			var m store.Movie
			if m, err = st.MovieByTMDB(ctx, t.TMDBID); err == nil {
				id = m.ID
			}
		} else {
			var s store.Series
			if t.TVDBID != 0 {
				s, err = st.SeriesByTVDB(ctx, t.TVDBID)
			} else {
				err = store.ErrNotFound
			}
			if errors.Is(err, store.ErrNotFound) && t.TMDBID != 0 {
				s, err = st.SeriesByTMDB(ctx, t.TMDBID)
			}
			if err == nil {
				id = s.ID
			}
		}
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		row := store.Watched{TitleID: id, LastPlayed: clamp(t.LastPlayed), Viewers: t.Viewers, FinishedBy: t.FinishedBy, FinishedAt: clamp(t.FinishedAt)}
		if kind == "series" && t.FurthestSeason > 0 && t.FurthestEpisode > 0 {
			row.FurthestSeason, row.FurthestEpisode = t.FurthestSeason, t.FurthestEpisode
		}
		// Nobody has finished an episode nobody has started.
		if kind == "series" && t.AllFinishedSeason > 0 && t.AllFinishedEpisode > 0 &&
			(t.AllFinishedSeason < row.FurthestSeason || t.AllFinishedSeason == row.FurthestSeason && t.AllFinishedEpisode <= row.FurthestEpisode) {
			row.AllFinishedSeason, row.AllFinishedEpisode = t.AllFinishedSeason, t.AllFinishedEpisode
		}
		rows = append(rows, row)
	}
	if err := st.ReplaceWatched(ctx, kind, in.People, rows); err != nil {
		return err
	}
	// Someone may have got further into a show: fetch its next episodes.
	// The report is kept either way.
	if kind == "series" {
		if err := h.Catalog.SpaceSaver(ctx); err != nil {
			h.Log.Warn("couldn't run space saver after a watch report", "err", err)
		}
		if h.Watched != nil {
			h.Watched(ctx)
		}
	}
	return write(w, http.StatusOK, map[string]int{"kept": len(rows)})
}

// leaving is GET /api/v3/boxoffice/leaving: the movies (on /radarr) or
// shows (on /sonarr) that clean-up rules have listed as leaving soon, with
// the day each can go, soonest first, so Matinee can show a Leaving soon
// row. Playing one takes it off the list the next time the rules run.
func (h *Handler) leaving(w http.ResponseWriter, r *http.Request) error {
	type title struct {
		TMDBID   int64     `json:"tmdbId"`
		TVDBID   int64     `json:"tvdbId,omitempty"`
		Title    string    `json:"title"`
		Year     int       `json:"year,omitempty"`
		LeavesAt time.Time `json:"leavesAt"`
	}
	ctx := r.Context()
	st := h.Catalog.Store
	kind := "movie"
	if h.App == Sonarr {
		kind = "series"
	}
	items, err := st.CleanupItems(ctx)
	if err != nil {
		return err
	}
	out := []title{}
	for _, i := range items {
		if i.Kind != kind {
			continue
		}
		t := title{LeavesAt: i.LeavesAt}
		if kind == "movie" {
			m, err := st.Movie(ctx, i.TitleID)
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			t.TMDBID, t.Title, t.Year = m.TMDBID, m.Title, m.Year
		} else {
			s, err := st.Series(ctx, i.TitleID)
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			t.TMDBID, t.TVDBID, t.Title, t.Year = s.TMDBID, s.TVDBID, s.Title, s.Year
		}
		out = append(out, t)
	}
	return write(w, http.StatusOK, map[string]any{"titles": out})
}
