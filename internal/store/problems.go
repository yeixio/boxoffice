package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// LibraryTitle is a movie or show on the list, with every file Box Office
// has recorded for it, for the Problem files check (see the problems
// package).
type LibraryTitle struct {
	Kind   string // movie or series
	ID     int64
	Title  string
	Year   int
	Root   string
	Path   string
	Locked bool // on a Do not touch profile
	Files  []TitleFile
	// Episodes are a show's, by season and number, with or without a file.
	Episodes []Episode
}

// TitleFile is one recorded file of a title.
type TitleFile struct {
	File
	// Season and Number are the episode's, for a show's files.
	Season, Number int
	EpisodeTitle   string
}

// LibraryTitles lists every movie and show on the list with its files.
func (s *Store) LibraryTitles(ctx context.Context) ([]LibraryTitle, error) {
	var out []LibraryTitle
	byKey := map[string]int{}
	key := func(kind string, id int64) string { return kind + ":" + strconv.FormatInt(id, 10) }
	for _, q := range []struct{ kind, table string }{{"movie", "movies"}, {"series", "series"}} {
		rows, err := s.query(ctx, `SELECT t.id, t.title, t.year, t.root_folder, t.path, p.locked
			FROM `+q.table+` t JOIN quality_profiles p ON p.id = t.profile_id ORDER BY t.sort_title`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			t := LibraryTitle{Kind: q.kind}
			var locked int
			if err := rows.Scan(&t.ID, &t.Title, &t.Year, &t.Root, &t.Path, &locked); err != nil {
				rows.Close()
				return nil, err
			}
			t.Locked = locked == 1
			byKey[key(q.kind, t.ID)] = len(out)
			out = append(out, t)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	// Episodes, with their files when they have one.
	rows, err := s.query(ctx, `SELECT e.id, e.series_id, e.season, e.number, e.title, e.monitored, f.id, f.path, f.size, f.quality, f.added_at
		FROM episodes e LEFT JOIN media_files f ON f.kind = 'episode' AND f.owner_id = e.id ORDER BY e.series_id, e.season, e.number`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var e Episode
		var mon int
		var fid, fsize, fq, fadded sql.NullInt64
		var fpath sql.NullString
		if err := rows.Scan(&e.ID, &e.SeriesID, &e.Season, &e.Number, &e.Title, &mon, &fid, &fpath, &fsize, &fq, &fadded); err != nil {
			rows.Close()
			return nil, err
		}
		e.Monitored = mon == 1
		i, ok := byKey[key("series", e.SeriesID)]
		if !ok {
			continue
		}
		if fid.Valid {
			e.File = &File{ID: fid.Int64, Kind: "episode", OwnerID: e.ID, Path: fpath.String, Size: fsize.Int64, Quality: int(fq.Int64), AddedAt: time.Unix(fadded.Int64, 0).UTC()}
			out[i].Files = append(out[i].Files, TitleFile{File: *e.File, Season: e.Season, Number: e.Number, EpisodeTitle: e.Title})
		}
		out[i].Episodes = append(out[i].Episodes, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Movies' copies and their other editions.
	rows, err = s.query(ctx, "SELECT id, kind, owner_id, path, size, quality, added_at FROM media_files WHERE kind IN ('movie', 'edition') ORDER BY path")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var f File
		var added int64
		if err := rows.Scan(&f.ID, &f.Kind, &f.OwnerID, &f.Path, &f.Size, &f.Quality, &added); err != nil {
			return nil, err
		}
		f.AddedAt = time.Unix(added, 0).UTC()
		if i, ok := byKey[key("movie", f.OwnerID)]; ok {
			out[i].Files = append(out[i].Files, TitleFile{File: f})
		}
	}
	return out, rows.Err()
}

// FileByPath is the recorded file at path.
func (s *Store) FileByPath(ctx context.Context, path string) (File, error) {
	var f File
	var added int64
	err := s.row(ctx, "SELECT id, kind, owner_id, path, size, quality, added_at FROM media_files WHERE path = ?", path).
		Scan(&f.ID, &f.Kind, &f.OwnerID, &f.Path, &f.Size, &f.Quality, &added)
	if isNoRows(err) {
		return f, ErrNotFound
	}
	f.AddedAt = time.Unix(added, 0).UTC()
	return f, err
}

// MoveFileRecord says a recorded file is now at another path, with its
// new size.
func (s *Store) MoveFileRecord(ctx context.Context, from, to string, size int64) error {
	_, err := s.exec(ctx, "UPDATE media_files SET path = ?, size = ? WHERE path = ?", to, size, from)
	return err
}

// RestoreFile records a file again exactly as it was, for putting back
// what a fix changed: a movie's or an episode's copy replaces the one it
// has now, an edition is added next to it.
func (s *Store) RestoreFile(ctx context.Context, f File) error {
	if f.Kind == "edition" {
		_, err := s.AddEditionFile(ctx, f.OwnerID, f.Path, f.Size, f.Quality)
		return err
	}
	if _, err := s.exec(ctx, "DELETE FROM media_files WHERE path = ?", f.Path); err != nil {
		return err
	}
	_, err := s.AddFile(ctx, f)
	return err
}

// ForgetFileAt drops the record of the file at path, if there is one.
func (s *Store) ForgetFileAt(ctx context.Context, path string) error {
	_, err := s.exec(ctx, "DELETE FROM media_files WHERE path = ?", path)
	return err
}

// MoveTitleFolder says a movie's or show's folder is now at another path,
// and moves the records of the files inside it along.
func (s *Store) MoveTitleFolder(ctx context.Context, kind string, id int64, from, to string) error {
	table := map[string]string{"movie": "movies", "series": "series"}[kind]
	if table == "" {
		return ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, s.q("UPDATE "+table+" SET path = ? WHERE id = ?"), to, id); err != nil {
		return err
	}
	from, to = filepath.Clean(from), filepath.Clean(to)
	prefix := from + string(filepath.Separator)
	rows, err := tx.QueryContext(ctx, s.q("SELECT id, path FROM media_files WHERE substr(path, 1, ?) = ?"), len(prefix), prefix)
	if err != nil {
		return err
	}
	type move struct {
		id   int64
		path string
	}
	var moves []move
	for rows.Next() {
		var m move
		if err := rows.Scan(&m.id, &m.path); err != nil {
			rows.Close()
			return err
		}
		moves = append(moves, move{m.id, to + string(filepath.Separator) + strings.TrimPrefix(m.path, prefix)})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, m := range moves {
		if _, err := tx.ExecContext(ctx, s.q("UPDATE media_files SET path = ? WHERE id = ?"), m.path, m.id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ---- fixes ----

// Fix states.
const (
	FixWaiting = "waiting" // originals set aside, waiting to be deleted for good or put back
	FixDone    = "done"    // nothing to wait for: originals deleted, or nothing was set aside
	FixUndone  = "undone"  // put back
)

// Roles of a fix's files.
const (
	FixAside = "aside" // an original, set aside in .recycle
	FixMoved = "moved" // renamed or moved
	FixMade  = "made"  // a new file the fix wrote
)

// ProblemFix is what one fix on the Problem files page did.
type ProblemFix struct {
	ID        int64            `json:"id"`
	Action    string           `json:"action"`
	Title     string           `json:"title"`
	Root      string           `json:"root"`
	State     string           `json:"state"`
	CreatedAt time.Time        `json:"createdAt"`
	DecidedAt *time.Time       `json:"decidedAt,omitempty"`
	Files     []ProblemFixFile `json:"files"`
}

// ProblemFixFile is a file a fix moved, set aside or wrote.
type ProblemFixFile struct {
	Role string `json:"role"`
	Src  string `json:"src"`
	Dest string `json:"dest"`
	Size int64  `json:"size"`
	// Record is the file's record before the fix, if it had one.
	Record *File `json:"-"`
}

// AddProblemFix records a fix that's been carried out.
func (s *Store) AddProblemFix(ctx context.Context, f ProblemFix) (ProblemFix, error) {
	if f.State == "" {
		f.State = FixWaiting
	}
	id, err := s.insert(ctx, "INSERT INTO problem_fixes (action, title, root, state, created_at) VALUES (?, ?, ?, ?, ?)",
		f.Action, f.Title, f.Root, f.State, now())
	if err != nil {
		return f, err
	}
	f.ID = id
	for _, ff := range f.Files {
		kind, owner, q := "", int64(0), 0
		if ff.Record != nil {
			kind, owner, q = ff.Record.Kind, ff.Record.OwnerID, ff.Record.Quality
		}
		if _, err := s.exec(ctx, "INSERT INTO problem_fix_files (fix_id, role, src, dest, size, file_kind, file_owner, quality) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
			id, ff.Role, ff.Src, ff.Dest, ff.Size, kind, owner, q); err != nil {
			return f, err
		}
	}
	return s.ProblemFix(ctx, id)
}

// ProblemFix is one fix with its files.
func (s *Store) ProblemFix(ctx context.Context, id int64) (ProblemFix, error) {
	list, err := s.problemFixes(ctx, "WHERE id = ?", id)
	if err != nil {
		return ProblemFix{}, err
	}
	if len(list) == 0 {
		return ProblemFix{}, ErrNotFound
	}
	return list[0], nil
}

// ProblemFixes lists the fixes still waiting for a decision, then the last
// few others, newest first.
func (s *Store) ProblemFixes(ctx context.Context, recent int) ([]ProblemFix, error) {
	waiting, err := s.problemFixes(ctx, "WHERE state = ? ORDER BY created_at DESC, id DESC", FixWaiting)
	if err != nil {
		return nil, err
	}
	rest, err := s.problemFixes(ctx, "WHERE state <> ? ORDER BY created_at DESC, id DESC LIMIT ?", FixWaiting, recent)
	if err != nil {
		return nil, err
	}
	return append(waiting, rest...), nil
}

func (s *Store) problemFixes(ctx context.Context, where string, args ...any) ([]ProblemFix, error) {
	rows, err := s.query(ctx, "SELECT id, action, title, root, state, created_at, decided_at FROM problem_fixes "+where, args...)
	if err != nil {
		return nil, err
	}
	out := []ProblemFix{}
	for rows.Next() {
		var f ProblemFix
		var created, decided int64
		if err := rows.Scan(&f.ID, &f.Action, &f.Title, &f.Root, &f.State, &created, &decided); err != nil {
			rows.Close()
			return nil, err
		}
		f.CreatedAt = time.Unix(created, 0).UTC()
		if decided != 0 {
			d := time.Unix(decided, 0).UTC()
			f.DecidedAt = &d
		}
		f.Files = []ProblemFixFile{}
		out = append(out, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		frows, err := s.query(ctx, "SELECT role, src, dest, size, file_kind, file_owner, quality FROM problem_fix_files WHERE fix_id = ? ORDER BY role, src", out[i].ID)
		if err != nil {
			return nil, err
		}
		for frows.Next() {
			var ff ProblemFixFile
			var kind string
			var owner int64
			var q int
			if err := frows.Scan(&ff.Role, &ff.Src, &ff.Dest, &ff.Size, &kind, &owner, &q); err != nil {
				frows.Close()
				return nil, err
			}
			if kind != "" {
				ff.Record = &File{Kind: kind, OwnerID: owner, Path: ff.Src, Size: ff.Size, Quality: q}
			}
			out[i].Files = append(out[i].Files, ff)
		}
		frows.Close()
		if err := frows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// SetProblemFixState marks a fix done or put back.
func (s *Store) SetProblemFixState(ctx context.Context, id int64, state string) error {
	res, err := s.exec(ctx, "UPDATE problem_fixes SET state = ?, decided_at = ? WHERE id = ?", state, now(), id)
	if err != nil {
		return err
	}
	return affected(res)
}
