package api

import (
	"net/http"

	"github.com/yeixio/boxoffice/internal/store"
)

// bulkEdit is a change to many titles at once: the quality profile (such
// as Do not touch) and whether they're watched for.
type bulkEdit struct {
	IDs       []int64 `json:"ids"`
	ProfileID *int    `json:"profileId"`
	Monitored *bool   `json:"monitored"`
}

// maxBulk caps one bulk change.
const maxBulk = 10000

func (s *Server) readBulk(r *http.Request) (bulkEdit, error) {
	var in bulkEdit
	if err := readJSON(r, &in); err != nil {
		return in, err
	}
	if len(in.IDs) == 0 {
		return in, errorf(http.StatusBadRequest, "pick at least one title")
	}
	if len(in.IDs) > maxBulk {
		return in, errorf(http.StatusBadRequest, "that's too many at once")
	}
	if in.ProfileID == nil && in.Monitored == nil {
		return in, errorf(http.StatusBadRequest, "say what to change")
	}
	if in.ProfileID != nil {
		if _, err := s.Store.Profile(r.Context(), *in.ProfileID); err != nil {
			return in, errorf(http.StatusBadRequest, "there's no such quality profile")
		}
	}
	return in, nil
}

// bulkMovies changes many movies; ones that are gone are skipped.
func (s *Server) bulkMovies(w http.ResponseWriter, r *http.Request) error {
	in, err := s.readBulk(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	changed := 0
	for _, id := range in.IDs {
		m, err := s.Store.Movie(ctx, id)
		if err == store.ErrNotFound {
			continue
		} else if err != nil {
			return err
		}
		if in.ProfileID != nil {
			m.ProfileID = *in.ProfileID
		}
		if in.Monitored != nil {
			m.Monitored = *in.Monitored
		}
		if err := s.Store.UpdateMovie(ctx, m); err != nil {
			return err
		}
		changed++
	}
	return writeJSON(w, http.StatusOK, map[string]int{"changed": changed})
}

// bulkSeries changes many shows; ones that are gone are skipped.
func (s *Server) bulkSeries(w http.ResponseWriter, r *http.Request) error {
	in, err := s.readBulk(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	changed := 0
	for _, id := range in.IDs {
		t, err := s.Store.Series(ctx, id)
		if err == store.ErrNotFound {
			continue
		} else if err != nil {
			return err
		}
		if in.ProfileID != nil {
			t.ProfileID = *in.ProfileID
		}
		if in.Monitored != nil {
			t.Monitored = *in.Monitored
		}
		if err := s.Store.UpdateSeries(ctx, t); err != nil {
			return err
		}
		changed++
	}
	return writeJSON(w, http.StatusOK, map[string]int{"changed": changed})
}
