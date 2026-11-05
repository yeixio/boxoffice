package store

import (
	"context"
	"time"
)

// Search is when a movie or an episode was last searched for, and how many
// searches in a row found nothing Box Office could take.
type Search struct {
	At     time.Time `json:"at"`
	Misses int       `json:"misses"`
}

// MarkSearched records that a movie or an episode was just searched for,
// and whether the search found a release it could take.
func (s *Store) MarkSearched(ctx context.Context, kind string, id int64, found bool) error {
	misses := 1
	if found {
		misses = 0
	}
	_, err := s.exec(ctx, `INSERT INTO searched (kind, id, at, misses) VALUES (?, ?, ?, ?)
		ON CONFLICT (kind, id) DO UPDATE SET at = excluded.at, misses = CASE WHEN excluded.misses = 0 THEN 0 ELSE searched.misses + 1 END`,
		kind, id, now(), misses)
	return err
}

// Searched is the last search for each movie or episode of a kind.
func (s *Store) Searched(ctx context.Context, kind string) (map[int64]Search, error) {
	rows, err := s.query(ctx, "SELECT id, at, misses FROM searched WHERE kind = ?", kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]Search{}
	for rows.Next() {
		var id, at int64
		var misses int
		if err := rows.Scan(&id, &at, &misses); err != nil {
			return nil, err
		}
		out[id] = Search{At: time.Unix(at, 0).UTC(), Misses: misses}
	}
	return out, rows.Err()
}
