package store

import (
	"context"
	"time"
)

// CountRegrab notes a replacement Box Office grabbed by itself today.
func (s *Store) CountRegrab(ctx context.Context) error {
	_, err := s.exec(ctx, `INSERT INTO regrabs (day, n) VALUES (?, 1)
		ON CONFLICT (day) DO UPDATE SET n = regrabs.n + 1`, DataDay(time.Now()))
	return err
}

// RegrabsOn is how many replacements Box Office grabbed by itself on a
// day (as DataDay writes it).
func (s *Store) RegrabsOn(ctx context.Context, day string) (int, error) {
	var n int
	err := s.row(ctx, "SELECT CAST(COALESCE(SUM(n), 0) AS INTEGER) FROM regrabs WHERE day = ?", day).Scan(&n)
	return n, err
}

// RegrabWait is a replacement waiting for a day with room for it.
type RegrabWait struct {
	Key     string    `json:"key"`
	Subject string    `json:"subject"`
	Why     string    `json:"why"` // upgrade, fix or replace
	Release string    `json:"release"`
	Data    string    `json:"-"` // what to search for, as the search package wrote it
	Since   time.Time `json:"since"`
}

// WaitRegrab notes a replacement to look for again once there's room.
// Noted again, it keeps its place in the line.
func (s *Store) WaitRegrab(ctx context.Context, w RegrabWait) error {
	_, err := s.exec(ctx, `INSERT INTO regrab_waiting (key, subject, why, release, data, waiting_since) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET subject = excluded.subject, why = excluded.why, release = excluded.release, data = excluded.data`,
		w.Key, w.Subject, w.Why, w.Release, w.Data, now())
	return err
}

// RegrabsWaiting is every replacement waiting, oldest first.
func (s *Store) RegrabsWaiting(ctx context.Context) ([]RegrabWait, error) {
	rows, err := s.query(ctx, "SELECT key, subject, why, release, data, waiting_since FROM regrab_waiting ORDER BY waiting_since, key")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RegrabWait{}
	for rows.Next() {
		var w RegrabWait
		var since int64
		if err := rows.Scan(&w.Key, &w.Subject, &w.Why, &w.Release, &w.Data, &since); err != nil {
			return nil, err
		}
		w.Since = time.Unix(since, 0).UTC()
		out = append(out, w)
	}
	return out, rows.Err()
}

// DropRegrab forgets waiting replacements.
func (s *Store) DropRegrab(ctx context.Context, keys ...string) error {
	for _, k := range keys {
		if _, err := s.exec(ctx, "DELETE FROM regrab_waiting WHERE key = ?", k); err != nil {
			return err
		}
	}
	return nil
}
