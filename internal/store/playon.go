package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// PlayOnUsage is the data usage row PlayOn downloads count under, beside
// the news servers', so the monthly data cap counts them too.
const PlayOnUsage int64 = -1

// What became of a PlayOn recording.
const (
	PlayOnNew         = "new"         // listed, not downloaded yet
	PlayOnDownloading = "downloading" // being downloaded
	PlayOnFiled       = "filed"       // in the library
	PlayOnCheck       = "check"       // downloaded, waiting on Import files to be checked
	PlayOnFailed      = "failed"      // tried too many times; Retry tries again
	PlayOnSkipped     = "skipped"     // said not to download
	PlayOnExpired     = "expired"     // PlayOn deleted it before it was downloaded
)

// PlayOnRecording is a recording PlayOn made for the linked account.
type PlayOnRecording struct {
	ID       int64     `json:"id"`
	Name     string    `json:"name"`
	Series   string    `json:"series,omitempty"`
	Season   int       `json:"season,omitempty"`
	Episode  int       `json:"episode,omitempty"`
	Year     int       `json:"year,omitempty"`
	Provider string    `json:"provider,omitempty"`
	Quality  int       `json:"quality,omitempty"`
	Expires  time.Time `json:"expires,omitzero"`
	State    string    `json:"state"`
	Note     string    `json:"note,omitempty"`
	FiledTo  string    `json:"filedTo,omitempty"`
	Size     int64     `json:"size,omitempty"`
	Tries    int       `json:"tries,omitempty"`
	SeenAt   time.Time `json:"seenAt"`
	DoneAt   time.Time `json:"doneAt,omitzero"`
}

const playOnCols = "id, name, series, season, episode, year, provider, quality, expires_at, state, note, filed_to, size, tries, seen_at, done_at"

func scanPlayOn(row interface{ Scan(...any) error }) (PlayOnRecording, error) {
	var r PlayOnRecording
	var expires, seen, done int64
	err := row.Scan(&r.ID, &r.Name, &r.Series, &r.Season, &r.Episode, &r.Year, &r.Provider, &r.Quality, &expires, &r.State, &r.Note, &r.FiledTo, &r.Size, &r.Tries, &seen, &done)
	r.Expires, r.SeenAt, r.DoneAt = fromSecs(expires), fromSecs(seen), fromSecs(done)
	return r, err
}

// SeePlayOnRecording adds a recording PlayOn lists, or brings what PlayOn
// says about one already here up to date; what Box Office did with it is
// kept.
func (s *Store) SeePlayOnRecording(ctx context.Context, r PlayOnRecording) error {
	_, err := s.exec(ctx, `INSERT INTO playon_recordings (id, name, series, season, episode, year, provider, quality, expires_at, state, seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET name = excluded.name, series = excluded.series, season = excluded.season, episode = excluded.episode,
		year = excluded.year, provider = excluded.provider, quality = excluded.quality, expires_at = excluded.expires_at`,
		r.ID, r.Name, r.Series, r.Season, r.Episode, r.Year, r.Provider, r.Quality, secs(r.Expires), PlayOnNew, now())
	return err
}

// PlayOnRecording is one recording.
func (s *Store) PlayOnRecording(ctx context.Context, id int64) (PlayOnRecording, error) {
	r, err := scanPlayOn(s.row(ctx, "SELECT "+playOnCols+" FROM playon_recordings WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// PlayOnRecordings is every recording, newest first.
func (s *Store) PlayOnRecordings(ctx context.Context) ([]PlayOnRecording, error) {
	rows, err := s.query(ctx, "SELECT "+playOnCols+" FROM playon_recordings ORDER BY seen_at DESC, id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PlayOnRecording{}
	for rows.Next() {
		r, err := scanPlayOn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetPlayOnState says what became of a recording. tried counts one more
// try at downloading it; filedTo and size are kept when empty.
func (s *Store) SetPlayOnState(ctx context.Context, id int64, state, note, filedTo string, size int64, tried bool) error {
	done := int64(0)
	switch state {
	case PlayOnFiled, PlayOnCheck, PlayOnFailed, PlayOnSkipped, PlayOnExpired:
		done = now()
	}
	add := 0
	if tried {
		add = 1
	}
	q, args := "UPDATE playon_recordings SET state = ?, note = ?, tries = tries + ?, done_at = ?", []any{state, note, add, done}
	if filedTo != "" {
		q, args = q+", filed_to = ?", append(args, filedTo)
	}
	if size != 0 {
		q, args = q+", size = ?", append(args, size)
	}
	res, err := s.exec(ctx, q+" WHERE id = ?", append(args, id)...)
	if err != nil {
		return err
	}
	return affected(res)
}

// RetryPlayOn sets a recording that failed or was skipped to be downloaded
// again at the next check, with its tries counted from nothing.
func (s *Store) RetryPlayOn(ctx context.Context, id int64) error {
	res, err := s.exec(ctx, "UPDATE playon_recordings SET state = ?, note = '', tries = 0, done_at = 0 WHERE id = ? AND state IN (?, ?)",
		PlayOnNew, id, PlayOnFailed, PlayOnSkipped)
	if err != nil {
		return err
	}
	return affected(res)
}

// ResetPlayOnDownloading puts recordings a restart cut off back to be
// downloaded.
func (s *Store) ResetPlayOnDownloading(ctx context.Context) error {
	_, err := s.exec(ctx, "UPDATE playon_recordings SET state = ? WHERE state = ?", PlayOnNew, PlayOnDownloading)
	return err
}

// PrunePlayOn forgets recordings PlayOn deleted more than a month ago:
// PlayOn won't list them again.
func (s *Store) PrunePlayOn(ctx context.Context) error {
	_, err := s.exec(ctx, "DELETE FROM playon_recordings WHERE expires_at > 0 AND expires_at < ?", time.Now().AddDate(0, -1, 0).Unix())
	return err
}

// ForgetPlayOn forgets every recording, as when the PlayOn account is
// unlinked.
func (s *Store) ForgetPlayOn(ctx context.Context) error {
	_, err := s.exec(ctx, "DELETE FROM playon_recordings")
	return err
}
