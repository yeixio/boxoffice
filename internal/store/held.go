package store

import (
	"context"
	"time"
)

// HeldRelease is a release held back until it's old enough to grab by
// itself (the minimum release age).
type HeldRelease struct {
	Key    string    `json:"key"`
	Title  string    `json:"title"`
	Data   string    `json:"-"` // the release, as the search package wrote it
	DueAt  time.Time `json:"dueAt"`
	HeldAt time.Time `json:"heldAt"`
}

// HoldRelease notes a release to look at again at due. Held again, it
// keeps the sooner of the two times.
func (s *Store) HoldRelease(ctx context.Context, key, title, data string, due time.Time) error {
	_, err := s.exec(ctx, `INSERT INTO held_releases (key, title, data, due_at, held_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET data = excluded.data,
			due_at = CASE WHEN excluded.due_at < held_releases.due_at THEN excluded.due_at ELSE held_releases.due_at END`,
		key, title, data, due.Unix(), now())
	return err
}

// HeldReleases is every release held back, soonest first.
func (s *Store) HeldReleases(ctx context.Context) ([]HeldRelease, error) {
	rows, err := s.query(ctx, "SELECT key, title, data, due_at, held_at FROM held_releases ORDER BY due_at, key")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HeldRelease
	for rows.Next() {
		var h HeldRelease
		var due, held int64
		if err := rows.Scan(&h.Key, &h.Title, &h.Data, &due, &held); err != nil {
			return nil, err
		}
		h.DueAt, h.HeldAt = time.Unix(due, 0).UTC(), time.Unix(held, 0).UTC()
		out = append(out, h)
	}
	return out, rows.Err()
}

// DropHeld forgets held releases.
func (s *Store) DropHeld(ctx context.Context, keys ...string) error {
	for _, k := range keys {
		if _, err := s.exec(ctx, "DELETE FROM held_releases WHERE key = ?", k); err != nil {
			return err
		}
	}
	return nil
}
