package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/yeixio/boxoffice/internal/quality"
)

// The quality check looks inside movie and episode files to see what
// they really are. It reads files only; what it finds goes in
// media_files.

// checkKinds are the kinds of file the quality check looks inside: movies,
// a movie's other editions, and episodes.
const checkKinds = "('movie', 'edition', 'episode')"

// ToCheck is a file the quality check hasn't looked inside yet (or that
// changed size since), with its title's profile.
type ToCheck struct {
	File
	ProfileID int
}

// FilesToCheck lists up to n files the quality check hasn't looked inside,
// or whose size changed since it did.
func (s *Store) FilesToCheck(ctx context.Context, n int) ([]ToCheck, error) {
	rows, err := s.query(ctx, `SELECT f.id, f.kind, f.owner_id, f.path, f.size, f.quality,
		COALESCE(m.profile_id, t.profile_id, 0)
		FROM media_files f
		LEFT JOIN movies m ON f.kind IN ('movie', 'edition') AND m.id = f.owner_id
		LEFT JOIN episodes e ON f.kind = 'episode' AND e.id = f.owner_id
		LEFT JOIN series t ON t.id = e.series_id
		WHERE f.kind IN `+checkKinds+` AND (f.probed_at = 0 OR f.probed_size <> f.size)
		ORDER BY f.id LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ToCheck
	for rows.Next() {
		var c ToCheck
		if err := rows.Scan(&c.ID, &c.Kind, &c.OwnerID, &c.Path, &c.Size, &c.Quality, &c.ProfileID); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SaveCheck records what the quality check found inside a file, and the
// quality it found it is. A changed quality keeps the one before, so
// people can see what was corrected; checking again later keeps the
// first one.
func (s *Store) SaveCheck(ctx context.Context, f File, m quality.Media, q int) error {
	b, _ := json.Marshal(m)
	guessed := -1
	if q != f.Quality {
		guessed = f.Quality
	}
	_, err := s.exec(ctx, `UPDATE media_files SET media = ?, probed_at = ?, probed_size = ?, quality = ?,
		guessed = CASE WHEN guessed >= 0 THEN (CASE WHEN guessed = ? THEN -1 ELSE guessed END) ELSE ? END
		WHERE id = ? AND path = ?`,
		string(b), now(), f.Size, q, q, guessed, f.ID, f.Path)
	return err
}

// CheckMoviesAgain has the quality check look inside every movie copy
// and edition again.
func (s *Store) CheckMoviesAgain(ctx context.Context) error {
	_, err := s.exec(ctx, "UPDATE media_files SET probed_at = 0 WHERE kind IN ('movie', 'edition')")
	return err
}

// CheckAgain has the quality check look inside every file again.
func (s *Store) CheckAgain(ctx context.Context) error {
	_, err := s.exec(ctx, "UPDATE media_files SET probed_at = 0 WHERE kind IN "+checkKinds)
	return err
}

// CheckCounts is how far the quality check has got.
type CheckCounts struct {
	Files      int `json:"files"`      // movie and episode files
	Checked    int `json:"checked"`    // looked inside, as they are now
	Unreadable int `json:"unreadable"` // couldn't be looked inside
	Unknown    int `json:"unknown"`    // still of unknown quality
}

func (s *Store) CheckCounts(ctx context.Context) (CheckCounts, error) {
	var c CheckCounts
	err := s.row(ctx, `SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN probed_at > 0 AND probed_size = size THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN probed_at > 0 AND probed_size = size AND media LIKE '%"error"%' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN quality = 0 THEN 1 ELSE 0 END), 0)
		FROM media_files WHERE kind IN `+checkKinds).Scan(&c.Files, &c.Checked, &c.Unreadable, &c.Unknown)
	return c, err
}

// Correction is a file whose quality the check changed.
type Correction struct {
	FileID  int64         `json:"fileId"`
	Kind    string        `json:"kind"` // movie or series, the page it's on
	TitleID int64         `json:"titleId"`
	Title   string        `json:"title"` // "Movie (2010)" or "Show S01E02"
	Path    string        `json:"path"`
	Was     int           `json:"was"`
	Now     int           `json:"now"`
	Media   quality.Media `json:"media"`
	At      time.Time     `json:"at"`
}

// Corrections lists the files whose quality the check changed, newest
// first.
func (s *Store) Corrections(ctx context.Context) ([]Correction, error) {
	rows, err := s.query(ctx, `SELECT f.id, f.kind, f.path, f.guessed, f.quality, f.media, f.probed_at,
		COALESCE(m.id, t.id, 0), COALESCE(m.title, t.title, ''), COALESCE(m.year, 0), COALESCE(e.season, 0), COALESCE(e.number, 0)
		FROM media_files f
		LEFT JOIN movies m ON f.kind IN ('movie', 'edition') AND m.id = f.owner_id
		LEFT JOIN episodes e ON f.kind = 'episode' AND e.id = f.owner_id
		LEFT JOIN series t ON t.id = e.series_id
		WHERE f.kind IN `+checkKinds+` AND f.guessed >= 0
		ORDER BY f.probed_at DESC, f.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Correction{}
	for rows.Next() {
		var c Correction
		var media sql.NullString
		var at int64
		var year, season, number int
		if err := rows.Scan(&c.FileID, &c.Kind, &c.Path, &c.Was, &c.Now, &media, &at, &c.TitleID, &c.Title, &year, &season, &number); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(media.String), &c.Media)
		c.At = time.Unix(at, 0).UTC()
		if c.Kind == "episode" {
			c.Kind = "series"
			c.Title += fmt.Sprintf(" S%02dE%02d", season, number)
		} else {
			c.Kind = "movie"
			if year > 0 {
				c.Title += fmt.Sprintf(" (%d)", year)
			}
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
