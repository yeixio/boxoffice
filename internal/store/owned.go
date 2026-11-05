package store

import (
	"context"
	"time"
)

// Owned is where a movie or show on the list stands, in brief: enough to
// mark its poster in lists from TMDB.
type Owned struct {
	Kind      string // movie or series
	ID        int64
	TMDBID    int64
	Title     string
	Monitored bool
	AddedAt   time.Time
	// Files is how many files it has on disk; Wanted and Have count a
	// show's watched, aired episodes and the ones of them on disk. For a
	// movie both are 1, and Have is 1 once it's on disk.
	Files, Wanted, Have int
}

// OwnedTitles lists every movie and show on the list, for marking which
// titles in TMDB's lists are already here.
func (s *Store) OwnedTitles(ctx context.Context) ([]Owned, error) {
	out := []Owned{}
	rows, err := s.query(ctx, `SELECT m.id, m.tmdb_id, m.title, m.monitored, m.added_at, COUNT(f.id)
		FROM movies m LEFT JOIN media_files f ON f.kind = 'movie' AND f.owner_id = m.id
		GROUP BY m.id, m.tmdb_id, m.title, m.monitored, m.added_at`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		o := Owned{Kind: "movie", Wanted: 1}
		var mon int
		var added int64
		if err := rows.Scan(&o.ID, &o.TMDBID, &o.Title, &mon, &added, &o.Files); err != nil {
			rows.Close()
			return nil, err
		}
		o.Monitored, o.AddedAt, o.Have = mon == 1, time.Unix(added, 0).UTC(), min(o.Files, 1)
		out = append(out, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	at := now()
	rows, err = s.query(ctx, `SELECT s.id, s.tmdb_id, s.title, s.monitored, s.added_at,
		(SELECT COUNT(*) FROM episodes e JOIN media_files f ON f.kind = 'episode' AND f.owner_id = e.id WHERE e.series_id = s.id),
		(SELECT COUNT(*) FROM episodes e JOIN seasons se ON se.series_id = e.series_id AND se.number = e.season
			WHERE e.series_id = s.id AND e.monitored = 1 AND se.monitored = 1 AND e.air_date IS NOT NULL AND e.air_date <= ?),
		(SELECT COUNT(f.id) FROM episodes e JOIN seasons se ON se.series_id = e.series_id AND se.number = e.season
			JOIN media_files f ON f.kind = 'episode' AND f.owner_id = e.id
			WHERE e.series_id = s.id AND e.monitored = 1 AND se.monitored = 1 AND e.air_date IS NOT NULL AND e.air_date <= ?)
		FROM series s`, at, at)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		o := Owned{Kind: "series"}
		var mon int
		var added int64
		if err := rows.Scan(&o.ID, &o.TMDBID, &o.Title, &mon, &added, &o.Files, &o.Wanted, &o.Have); err != nil {
			return nil, err
		}
		o.Monitored, o.AddedAt = mon == 1, time.Unix(added, 0).UTC()
		out = append(out, o)
	}
	return out, rows.Err()
}
