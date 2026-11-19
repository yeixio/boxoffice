package arrapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
)

// watched is PUT /api/v3/boxoffice/watched: Matinee says what has been
// watched, so Box Office's clean-up rules can go by it. /radarr takes the
// movies and /sonarr the shows. Each report is the whole picture for its
// kind and replaces the last one: a title left out has no plays. Titles
// Box Office hasn't got are passed over.
//
//	{"people": 4, "titles": [{"tmdbId": 603, "lastPlayed": "2026-11-01T20:00:00Z",
//	  "viewers": 2, "finishedBy": 1, "finishedAt": "2026-10-30T21:10:00Z"}]}
//
// A show is named by tvdbId or tmdbId, and is finished by someone who has
// played every episode Matinee has of it.
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
		rows = append(rows, store.Watched{TitleID: id, LastPlayed: clamp(t.LastPlayed), Viewers: t.Viewers, FinishedBy: t.FinishedBy, FinishedAt: clamp(t.FinishedAt)})
	}
	if err := st.ReplaceWatched(ctx, kind, in.People, rows); err != nil {
		return err
	}
	return write(w, http.StatusOK, map[string]int{"kept": len(rows)})
}
