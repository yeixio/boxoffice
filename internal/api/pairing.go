package api

import (
	"context"

	"github.com/yeixio/boxoffice/internal/arrapi"
	"github.com/yeixio/boxoffice/internal/search"
)

// pairing is what the arr API offers Matinee beyond Radarr and Sonarr
// (see arrapi.Pairing): the downloader's progress, and searches for other
// editions and better copies. None without a downloader and a search.
func (s *Server) pairing() *arrapi.Pairing {
	if s.Downloads == nil || s.Search == nil || s.Catalog == nil {
		return nil
	}
	return &arrapi.Pairing{
		Live: func() arrapi.LiveState {
			l := s.Downloads.Live()
			out := arrapi.LiveState{Speed: l.Speed, Steps: map[int64]arrapi.LiveStep{}}
			for id, p := range l.Progress {
				out.Steps[id] = arrapi.LiveStep{DoneBytes: p.DoneBytes, Size: p.Size, StepDone: p.StepDone, StepTotal: p.StepTotal, Stage: p.Stage}
			}
			return out
		},
		Waiting:   s.Downloads.Waiting,
		CanImport: s.Downloads.CanImport,
		Editions: func(ctx context.Context, movieID int64, owned []string) (any, []string, error) {
			return s.Search.MovieEditions(ctx, movieID, owned)
		},
		Ask: func(ctx context.Context, kind string, id int64, subject, edition, mode, want string) error {
			j := search.AskedJob{Kind: kind, ID: id, Ask: search.Ask{Edition: edition, Mode: mode, Want: want}}
			_, err := s.Catalog.Jobs.Queue(ctx, search.JobSearchAsked, subject, j.Payload())
			return err
		},
	}
}
