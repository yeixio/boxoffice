package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Job is a piece of background work, and its record once done.
type Job struct {
	ID         int64     `json:"id"`
	Kind       string    `json:"kind"`
	Subject    string    `json:"subject"`
	Payload    string    `json:"-"`
	Status     string    `json:"status"` // queued, running, done, skipped, failed
	Message    string    `json:"message,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	StartedAt  time.Time `json:"startedAt,omitzero"`
	FinishedAt time.Time `json:"finishedAt,omitzero"`
}

const jobCols = "id, kind, subject, payload, status, message, created_at, started_at, finished_at"

func scanJob(r interface{ Scan(...any) error }) (Job, error) {
	var j Job
	var c, st, f int64
	err := r.Scan(&j.ID, &j.Kind, &j.Subject, &j.Payload, &j.Status, &j.Message, &c, &st, &f)
	if errors.Is(err, sql.ErrNoRows) {
		return j, ErrNotFound
	}
	j.CreatedAt = time.Unix(c, 0).UTC()
	if st > 0 {
		j.StartedAt = time.Unix(st, 0).UTC()
	}
	if f > 0 {
		j.FinishedAt = time.Unix(f, 0).UTC()
	}
	return j, err
}

// QueueJob adds work to do. Work of the same kind with the same payload
// that's still waiting isn't queued twice.
func (s *Store) QueueJob(ctx context.Context, kind, subject, payload string) (Job, error) {
	j, err := scanJob(s.row(ctx, "SELECT "+jobCols+" FROM jobs WHERE status = 'queued' AND kind = ? AND payload = ?", kind, payload))
	if err == nil {
		return j, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return j, err
	}
	id, err := s.insert(ctx, "INSERT INTO jobs (kind, subject, payload, created_at) VALUES (?, ?, ?, ?)", kind, subject, payload, now())
	if err != nil {
		return j, err
	}
	return scanJob(s.row(ctx, "SELECT "+jobCols+" FROM jobs WHERE id = ?", id))
}

// NextJob takes the oldest queued job and marks it running.
func (s *Store) NextJob(ctx context.Context) (Job, error) {
	for {
		j, err := scanJob(s.row(ctx, "SELECT "+jobCols+" FROM jobs WHERE status = 'queued' ORDER BY id LIMIT 1"))
		if err != nil {
			return j, err
		}
		res, err := s.exec(ctx, "UPDATE jobs SET status = 'running', started_at = ? WHERE id = ? AND status = 'queued'", now(), j.ID)
		if err != nil {
			return j, err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			j.Status, j.StartedAt = "running", time.Now().UTC()
			return j, nil
		}
		// Someone else took it; look again.
	}
}

// FinishJob records how a job ended.
func (s *Store) FinishJob(ctx context.Context, id int64, status, message string) error {
	_, err := s.exec(ctx, "UPDATE jobs SET status = ?, message = ?, finished_at = ? WHERE id = ?", status, message, now(), id)
	return err
}

// RequeueRunning puts jobs a stopped server was in the middle of back in
// the queue, so they run again.
func (s *Store) RequeueRunning(ctx context.Context) error {
	_, err := s.exec(ctx, "UPDATE jobs SET status = 'queued', started_at = 0 WHERE status = 'running'")
	return err
}

// Jobs lists the latest jobs, newest first.
func (s *Store) Jobs(ctx context.Context, limit int) ([]Job, error) {
	rows, err := s.query(ctx, "SELECT "+jobCols+" FROM jobs ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// PruneJobs forgets finished jobs older than before.
func (s *Store) PruneJobs(ctx context.Context, before time.Time) error {
	_, err := s.exec(ctx, "DELETE FROM jobs WHERE status NOT IN ('queued', 'running') AND finished_at < ?", before.Unix())
	return err
}
