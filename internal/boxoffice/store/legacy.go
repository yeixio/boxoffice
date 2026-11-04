package store

import (
	"context"
	"time"
)

// SetLegacyID remembers that another app called local row localID legacyID.
func (s *Store) SetLegacyID(ctx context.Context, app, kind string, legacyID, localID int64) error {
	_, err := s.exec(ctx, `INSERT INTO legacy_ids (app, kind, legacy_id, local_id) VALUES (?, ?, ?, ?)
		ON CONFLICT (app, kind, legacy_id) DO UPDATE SET local_id = excluded.local_id`, app, kind, legacyID, localID)
	return err
}

// LegacyIDs maps another app's ids for one kind of row to Box Office's.
func (s *Store) LegacyIDs(ctx context.Context, app, kind string) (map[int64]int64, error) {
	rows, err := s.query(ctx, "SELECT legacy_id, local_id FROM legacy_ids WHERE app = ? AND kind = ?", app, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var l, id int64
		if err := rows.Scan(&l, &id); err != nil {
			return nil, err
		}
		out[l] = id
	}
	return out, rows.Err()
}

// Migration is one import from another app.
type Migration struct {
	ID         int64     `json:"id"`
	App        string    `json:"app"`
	URL        string    `json:"url"`
	Status     string    `json:"status"` // running, done, failed
	Report     string    `json:"report"` // JSON
	CreatedAt  time.Time `json:"createdAt"`
	FinishedAt time.Time `json:"finishedAt,omitzero"`
}

func (s *Store) StartMigration(ctx context.Context, app, url string) (int64, error) {
	return s.insert(ctx, "INSERT INTO migrations (app, url, status, created_at) VALUES (?, ?, 'running', ?)", app, url, now())
}

func (s *Store) FinishMigration(ctx context.Context, id int64, status, report string) error {
	_, err := s.exec(ctx, "UPDATE migrations SET status = ?, report = ?, finished_at = ? WHERE id = ?", status, report, now(), id)
	return err
}

func (s *Store) Migrations(ctx context.Context) ([]Migration, error) {
	rows, err := s.query(ctx, "SELECT id, app, url, status, report, created_at, finished_at FROM migrations ORDER BY id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Migration{}
	for rows.Next() {
		var m Migration
		var c, f int64
		if err := rows.Scan(&m.ID, &m.App, &m.URL, &m.Status, &m.Report, &c, &f); err != nil {
			return nil, err
		}
		m.CreatedAt = time.Unix(c, 0).UTC()
		if f > 0 {
			m.FinishedAt = time.Unix(f, 0).UTC()
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

