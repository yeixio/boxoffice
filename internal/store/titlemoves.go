package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// Moving titles to another root folder (a new drive, say): one row per
// title, worked through in order in the background by package relocate.
// The rows say how far each got, so a move a restart cut off carries on
// from where it was, and never loses the files.

// Title move states.
const (
	MoveWaiting   = "waiting"   // not started
	MoveCopying   = "copying"   // being copied to the new folder; the original is untouched
	MoveCopied    = "copied"    // copied and checked; the title still points at the original
	MoveSwitched  = "switched"  // the title points at the copy; the original is being removed
	MoveDone      = "done"      // moved
	MoveFailed    = "failed"    // stopped; the original is where it was
	MoveSkipped   = "skipped"   // couldn't be moved, for the reason in its note
	MoveCancelled = "cancelled" // cancelled before it was moved
)

// MoveFinished says a move in state is over.
func MoveFinished(state string) bool {
	switch state {
	case MoveDone, MoveFailed, MoveSkipped, MoveCancelled:
		return true
	}
	return false
}

// TitleMove is one title being moved to another root folder.
type TitleMove struct {
	ID      int64  `json:"id"`
	Kind    string `json:"kind"` // movie or series
	TitleID int64  `json:"titleId"`
	Title   string `json:"title"`
	// FromRoot and FromPath are the root folder and folder it was in when
	// it was picked; ToRoot is where it goes, and ToPath the folder there.
	FromRoot string `json:"fromRoot"`
	FromPath string `json:"fromPath"`
	ToRoot   string `json:"toRoot"`
	ToPath   string `json:"toPath"`
	Files    int    `json:"files"`
	Size     int64  `json:"size"`
	State    string `json:"state"`
	// Manifest is the files copied, with their sizes and checksums, as
	// JSON, once it's copied: only those are removed from the original.
	Manifest   string     `json:"-"`
	Note       string     `json:"note,omitempty"`
	Tries      int        `json:"-"`
	CreatedAt  time.Time  `json:"createdAt"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

const titleMoveCols = "id, kind, title_id, title, from_root, from_path, to_root, to_path, files, size, state, manifest, note, tries, created_at, started_at, finished_at"

func scanTitleMove(sc interface{ Scan(...any) error }) (TitleMove, error) {
	var m TitleMove
	var created, started, finished int64
	err := sc.Scan(&m.ID, &m.Kind, &m.TitleID, &m.Title, &m.FromRoot, &m.FromPath, &m.ToRoot, &m.ToPath, &m.Files, &m.Size, &m.State,
		&m.Manifest, &m.Note, &m.Tries, &created, &started, &finished)
	m.CreatedAt = time.Unix(created, 0).UTC()
	m.StartedAt = unix(sql.NullInt64{Int64: started, Valid: started > 0})
	m.FinishedAt = unix(sql.NullInt64{Int64: finished, Valid: finished > 0})
	return m, err
}

func (s *Store) titleMoves(ctx context.Context, where string, args ...any) ([]TitleMove, error) {
	rows, err := s.query(ctx, "SELECT "+titleMoveCols+" FROM title_moves "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TitleMove{}
	for rows.Next() {
		m, err := scanTitleMove(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AddTitleMoves puts titles in line to be moved.
func (s *Store) AddTitleMoves(ctx context.Context, list []TitleMove) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t := now()
	for _, m := range list {
		if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO title_moves (kind, title_id, title, from_root, from_path, to_root, files, size, state, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`), m.Kind, m.TitleID, m.Title, m.FromRoot, m.FromPath, m.ToRoot, m.Files, m.Size, MoveWaiting, t); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// TitleMoves lists the moves not cleared away, in the order they're made:
// the ones under way, then waiting, then the finished ones.
func (s *Store) TitleMoves(ctx context.Context) ([]TitleMove, error) {
	return s.titleMoves(ctx, "ORDER BY id")
}

// TitleMoveByID is one move.
func (s *Store) TitleMoveByID(ctx context.Context, id int64) (TitleMove, error) {
	m, err := scanTitleMove(s.row(ctx, "SELECT "+titleMoveCols+" FROM title_moves WHERE id = ?", id))
	if isNoRows(err) {
		return m, ErrNotFound
	}
	return m, err
}

// UnfinishedTitleMoves is the moves still to do, those a restart cut off
// first, then the waiting ones in order.
func (s *Store) UnfinishedTitleMoves(ctx context.Context) ([]TitleMove, error) {
	return s.titleMoves(ctx, "WHERE state IN (?, ?, ?, ?) ORDER BY CASE WHEN state = ? THEN 1 ELSE 0 END, id",
		MoveCopying, MoveCopied, MoveSwitched, MoveWaiting, MoveWaiting)
}

// TitleMoving says whether a title is waiting to be moved, or being moved.
func (s *Store) TitleMoving(ctx context.Context, kind string, id int64) (bool, error) {
	var n int
	err := s.row(ctx, "SELECT COUNT(*) FROM title_moves WHERE kind = ? AND title_id = ? AND state IN (?, ?, ?, ?)",
		kind, id, MoveCopying, MoveCopied, MoveSwitched, MoveWaiting).Scan(&n)
	return n > 0, err
}

// SaveTitleMove saves how far a move got.
func (s *Store) SaveTitleMove(ctx context.Context, m TitleMove) error {
	res, err := s.exec(ctx, `UPDATE title_moves SET to_path = ?, files = ?, size = ?, state = ?, manifest = ?, note = ?, tries = ?,
		started_at = ?, finished_at = ? WHERE id = ?`,
		m.ToPath, m.Files, m.Size, m.State, m.Manifest, m.Note, m.Tries, unixOrZero(m.StartedAt), unixOrZero(m.FinishedAt), m.ID)
	if err != nil {
		return err
	}
	return affected(res)
}

// CancelTitleMoves cancels the moves that haven't started.
func (s *Store) CancelTitleMoves(ctx context.Context) (int, error) {
	res, err := s.exec(ctx, "UPDATE title_moves SET state = ?, note = '', finished_at = ? WHERE state = ?", MoveCancelled, now(), MoveWaiting)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// RetryTitleMoves puts the failed moves back in line.
func (s *Store) RetryTitleMoves(ctx context.Context) (int, error) {
	res, err := s.exec(ctx, "UPDATE title_moves SET state = ?, note = '', tries = 0, finished_at = 0 WHERE state = ?", MoveWaiting, MoveFailed)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// ClearTitleMoves forgets the finished moves.
func (s *Store) ClearTitleMoves(ctx context.Context) error {
	_, err := s.exec(ctx, "DELETE FROM title_moves WHERE state IN (?, ?, ?, ?)", MoveDone, MoveFailed, MoveSkipped, MoveCancelled)
	return err
}

// prefixLen is how long prefix is in SQL's substr, which counts
// characters, not bytes: "Amélie (2001)/" is 14, not 15.
func prefixLen(prefix string) int { return utf8.RuneCountInString(prefix) }

// SwitchTitleFolder points a moved title at its new folder, in one go:
// its root folder and folder, the records of the files inside, and the
// subtitles Box Office wrote there; and marks the move switched. It does
// nothing, and says ErrNotFound, if the title is gone or its folder isn't
// from any more: something changed it meanwhile.
func (s *Store) SwitchTitleFolder(ctx context.Context, moveID int64, kind string, id int64, from, toRoot, to string) error {
	table := map[string]string{"movie": "movies", "series": "series"}[kind]
	if table == "" {
		return ErrNotFound
	}
	from, to = filepath.Clean(from), filepath.Clean(to)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, s.q("UPDATE "+table+" SET root_folder = ?, path = ? WHERE id = ? AND path = ?"), toRoot, to, id, from)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	prefix := from + string(filepath.Separator)
	for _, t := range []struct{ table, key string }{{"media_files", "id"}, {"subtitles", "file_id"}} {
		extra := ""
		if t.table == "subtitles" {
			extra = ", language"
		}
		rows, err := tx.QueryContext(ctx, s.q("SELECT "+t.key+extra+", path FROM "+t.table+" WHERE substr(path, 1, ?) = ?"), prefixLen(prefix), prefix)
		if err != nil {
			return err
		}
		type move struct {
			key        int64
			lang, path string
		}
		var moves []move
		for rows.Next() {
			var m move
			var err error
			if extra != "" {
				err = rows.Scan(&m.key, &m.lang, &m.path)
			} else {
				err = rows.Scan(&m.key, &m.path)
			}
			if err != nil {
				rows.Close()
				return err
			}
			m.path = to + string(filepath.Separator) + strings.TrimPrefix(m.path, prefix)
			moves = append(moves, m)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, m := range moves {
			q, args := "UPDATE media_files SET path = ? WHERE id = ?", []any{m.path, m.key}
			if extra != "" {
				q, args = "UPDATE subtitles SET path = ? WHERE file_id = ? AND language = ?", []any{m.path, m.key, m.lang}
			}
			if _, err := tx.ExecContext(ctx, s.q(q), args...); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, s.q("UPDATE title_moves SET state = ? WHERE id = ?"), MoveSwitched, moveID); err != nil {
		return err
	}
	return tx.Commit()
}

// FixWaitingIn says whether a Problem files fix waiting to be decided on
// moved or wrote files in folder: putting it back needs them where they
// are.
func (s *Store) FixWaitingIn(ctx context.Context, folder string) (bool, error) {
	folder = filepath.Clean(folder)
	prefix := folder + string(filepath.Separator)
	var n int
	err := s.row(ctx, `SELECT COUNT(*) FROM problem_fix_files ff JOIN problem_fixes f ON f.id = ff.fix_id
		WHERE f.state = ? AND (ff.dest = ? OR substr(ff.dest, 1, ?) = ? OR ff.src = ? OR substr(ff.src, 1, ?) = ?)`,
		FixWaiting, folder, prefixLen(prefix), prefix, folder, prefixLen(prefix), prefix).Scan(&n)
	return n > 0, err
}

// OthersFilesIn names another title whose folder or recorded files are
// in folder, or says "". A movie's own files are its copy, its second
// copy and its editions; a show's, its episodes'.
func (s *Store) OthersFilesIn(ctx context.Context, folder, kind string, id int64) (string, error) {
	folder = filepath.Clean(folder)
	prefix := folder + string(filepath.Separator)
	n := prefixLen(prefix)
	for _, q := range []struct{ table, kind string }{{"movies", "movie"}, {"series", "series"}} {
		var title string
		except := int64(-1)
		if q.kind == kind {
			except = id
		}
		err := s.row(ctx, "SELECT title FROM "+q.table+" WHERE id <> ? AND (path = ? OR substr(path, 1, ?) = ?) LIMIT 1",
			except, folder, n, prefix).Scan(&title)
		if err == nil {
			return title, nil
		}
		if !isNoRows(err) {
			return "", err
		}
	}
	var path string
	var err error
	if kind == "movie" {
		err = s.row(ctx, "SELECT path FROM media_files WHERE substr(path, 1, ?) = ? AND NOT (kind IN ('movie', 'second', 'edition') AND owner_id = ?) LIMIT 1",
			n, prefix, id).Scan(&path)
	} else {
		err = s.row(ctx, `SELECT path FROM media_files WHERE substr(path, 1, ?) = ?
			AND NOT (kind = 'episode' AND owner_id IN (SELECT id FROM episodes WHERE series_id = ?)) LIMIT 1`, n, prefix, id).Scan(&path)
	}
	if err == nil {
		return "another title's file " + filepath.Base(path), nil
	}
	if !isNoRows(err) {
		return "", err
	}
	return "", nil
}
