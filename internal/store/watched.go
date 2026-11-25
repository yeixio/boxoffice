package store

import (
	"context"
	"encoding/json"
	"time"
)

// Watched is what Matinee says about a movie's or a show's plays.
type Watched struct {
	TitleID int64 `json:"titleId"`
	// LastPlayed is when anyone last played it.
	LastPlayed *time.Time `json:"lastPlayed,omitempty"`
	// Viewers is how many people have played any of it, and FinishedBy how
	// many have finished it (a show: every episode it has).
	Viewers    int `json:"viewers"`
	FinishedBy int `json:"finishedBy"`
	// FinishedAt is when the last of them finished.
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	// FurthestSeason and FurthestEpisode are the latest episode of a show
	// anyone has started, specials left out; 0 and 0 when nobody has, or
	// Matinee is too old to say.
	FurthestSeason  int `json:"furthestSeason,omitempty"`
	FurthestEpisode int `json:"furthestEpisode,omitempty"`
	// AllFinishedSeason and AllFinishedEpisode are the latest episode of
	// a show that everyone who has played any of it has finished,
	// specials left out; 0 and 0 when there's none, or Matinee is too old
	// to say.
	AllFinishedSeason  int `json:"allFinishedSeason,omitempty"`
	AllFinishedEpisode int `json:"allFinishedEpisode,omitempty"`
}

// WatchReport is when Matinee last said what was watched, for movies or
// for shows, and how many people it has.
type WatchReport struct {
	At     time.Time `json:"at"`
	People int       `json:"people"`
	Titles int       `json:"titles"`
}

func watchReportKey(kind string) string { return "watch_report_" + kind }

func unixOrZero(t *time.Time) int64 {
	if t == nil || t.IsZero() {
		return 0
	}
	return t.Unix()
}

func timeOrNil(sec int64) *time.Time {
	if sec <= 0 {
		return nil
	}
	t := time.Unix(sec, 0).UTC()
	return &t
}

// ReplaceWatched takes a whole report for a kind (movie or series): the
// titles in it are kept, and ones from earlier reports that aren't in it
// are forgotten, as they've no plays any more.
func (s *Store) ReplaceWatched(ctx context.Context, kind string, people int, rows []Watched) error {
	stamp := time.Now().UnixNano()
	for _, w := range rows {
		_, err := s.exec(ctx, `INSERT INTO watched (kind, title_id, last_played, viewers, finished_by, finished_at, furthest_season, furthest_episode, all_finished_season, all_finished_episode, report) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (kind, title_id) DO UPDATE SET last_played = excluded.last_played, viewers = excluded.viewers,
				finished_by = excluded.finished_by, finished_at = excluded.finished_at,
				furthest_season = excluded.furthest_season, furthest_episode = excluded.furthest_episode,
				all_finished_season = excluded.all_finished_season, all_finished_episode = excluded.all_finished_episode, report = excluded.report`,
			kind, w.TitleID, unixOrZero(w.LastPlayed), w.Viewers, w.FinishedBy, unixOrZero(w.FinishedAt), w.FurthestSeason, w.FurthestEpisode, w.AllFinishedSeason, w.AllFinishedEpisode, stamp)
		if err != nil {
			return err
		}
	}
	if _, err := s.exec(ctx, "DELETE FROM watched WHERE kind = ? AND report <> ?", kind, stamp); err != nil {
		return err
	}
	b, _ := json.Marshal(WatchReport{At: time.Now().UTC().Truncate(time.Second), People: people, Titles: len(rows)})
	return s.SetSetting(ctx, watchReportKey(kind), string(b))
}

// WatchedTitles is what was watched of a kind, by title id.
func (s *Store) WatchedTitles(ctx context.Context, kind string) (map[int64]Watched, error) {
	rows, err := s.query(ctx, "SELECT title_id, last_played, viewers, finished_by, finished_at, furthest_season, furthest_episode, all_finished_season, all_finished_episode FROM watched WHERE kind = ?", kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]Watched{}
	for rows.Next() {
		var w Watched
		var last, fin int64
		if err := rows.Scan(&w.TitleID, &last, &w.Viewers, &w.FinishedBy, &fin, &w.FurthestSeason, &w.FurthestEpisode, &w.AllFinishedSeason, &w.AllFinishedEpisode); err != nil {
			return nil, err
		}
		w.LastPlayed, w.FinishedAt = timeOrNil(last), timeOrNil(fin)
		out[w.TitleID] = w
	}
	return out, rows.Err()
}

// LastWatchReport is the last report for a kind; ok is false when Matinee
// has never sent one.
func (s *Store) LastWatchReport(ctx context.Context, kind string) (WatchReport, bool, error) {
	v, err := s.Setting(ctx, watchReportKey(kind))
	if err != nil || v == "" {
		return WatchReport{}, false, err
	}
	var r WatchReport
	if err := json.Unmarshal([]byte(v), &r); err != nil {
		return WatchReport{}, false, nil
	}
	return r, true, nil
}
