package api

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/yeixio/boxoffice/internal/health"
	"github.com/yeixio/boxoffice/internal/store"
)

// stats is a short summary for a dashboard, such as Homepage's custom API
// widget: what's in the library, what's downloading, and how many Health
// problems need fixing. Like the rest of /api/v1 it takes the API key in
// X-Api-Key.
type stats struct {
	store.LibraryStats
	Downloading int   `json:"downloading"` // fetching or unpacking now
	Queued      int   `json:"queued"`      // waiting or in progress
	Speed       int64 `json:"speed"`       // bytes a second
	// Problems is how many Health issues need fixing (errors and warnings);
	// Health is checked at most every few minutes for it.
	Problems int `json:"problems"`
}

// healthLife is how long a Health check's count of problems is reused, as
// a dashboard can ask every few seconds and a check reads the disks.
const healthLife = 5 * time.Minute

type healthCount struct {
	mu sync.Mutex
	at time.Time
	n  int
}

func (s *Server) getStats(w http.ResponseWriter, r *http.Request) error {
	lib, err := s.Store.LibraryStats(r.Context())
	if err != nil {
		return err
	}
	st := stats{LibraryStats: lib, Problems: s.problems(r.Context())}
	if s.Downloads != nil {
		l := s.Downloads.Live()
		st.Downloading, st.Speed = len(l.Progress), l.Speed
	}
	if q, err := s.Store.DownloadQueue(r.Context()); err == nil {
		st.Queued = len(q)
	}
	return writeJSON(w, http.StatusOK, st)
}

func (s *Server) problems(ctx context.Context) int {
	h := &s.healthCount
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.at.IsZero() && time.Since(h.at) < healthLife {
		return h.n
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	c := &health.Checker{Store: s.Store, Backups: s.Backups, TMDBKey: func(ctx context.Context) string { return s.Catalog.TMDB(ctx).Key }}
	n := 0
	for _, i := range c.Check(ctx) {
		if i.Level == "error" || i.Level == "warning" {
			n++
		}
	}
	h.n, h.at = n, time.Now()
	return n
}
