// Package store keeps Box Office's state in SQLite or PostgreSQL.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"github.com/yeixio/boxoffice/internal/quality"
)

//go:embed schema.sql
var schema string

// ErrNotFound means there's no such row.
var ErrNotFound = errors.New("not found")

// ErrExists means the row is there already.
var ErrExists = errors.New("already exists")

// Store is the database.
type Store struct {
	db       *sql.DB
	postgres bool
}

// IsPostgres says whether dsn names a PostgreSQL database; anything else is
// a SQLite file path (":memory:" in tests).
func IsPostgres(dsn string) bool {
	return strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://")
}

// Open connects to the database and brings its schema up to date.
func Open(dsn string) (*Store, error) {
	s := &Store{postgres: IsPostgres(dsn)}
	var err error
	if s.postgres {
		s.db, err = sql.Open("pgx", dsn)
		if err == nil {
			s.db.SetMaxOpenConns(10)
		}
	} else {
		s.db, err = sql.Open("sqlite", "file:"+dsn+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)")
		if err == nil {
			// One writer at a time is all SQLite allows anyway, and one
			// connection keeps a :memory: database the same database.
			s.db.SetMaxOpenConns(1)
		}
	}
	if err != nil {
		return nil, err
	}
	if err := s.migrate(context.Background()); err != nil {
		s.db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error                   { return s.db.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// backfill fills in a column just added, for the rows already there.
var backfill = map[string]string{
	// Downloads that failed for articles gone from the news servers, from
	// before they were marked, by what they said.
	// Downloads fetched whole, from before that was recorded: all but
	// those that failed while still fetching, by what they said.
	"downloads.fetched": `UPDATE downloads SET fetched = 1 WHERE status = 'completed' OR (status = 'failed' AND missing = 0
		AND message NOT LIKE 'Close %' AND message NOT LIKE 'Write %' AND message NOT LIKE 'Open %'
		AND message NOT LIKE '%couldn''t make a file%' AND message NOT LIKE '%stopped answering%' AND message NOT LIKE '%NZB for this download is gone%')`,
	"downloads.missing": "UPDATE downloads SET missing = 1 WHERE status = 'failed' AND quarantined = '' AND (message LIKE '%gone from your news servers%' OR message LIKE '%missing from your news servers%' OR message LIKE '%blocks are damaged%')",
}

func (s *Store) migrate(ctx context.Context) error {
	ddl := schema
	if s.postgres {
		ddl = strings.ReplaceAll(ddl, "INTEGER PRIMARY KEY AUTOINCREMENT", "BIGSERIAL PRIMARY KEY")
		ddl = strings.ReplaceAll(ddl, "BLOB", "BYTEA")
	}
	// Comments go first, so a semicolon in one can't split a statement.
	for _, stmt := range strings.Split(stripComments(ddl), ";") {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	// Columns added since a table was first made.
	for _, c := range []struct{ table, column, def string }{
		{"downloads", "quarantined", "TEXT NOT NULL DEFAULT ''"},
		{"quality_profiles", "locked", "INTEGER NOT NULL DEFAULT 0"},
		{"movies", "auto_upgrade", "TEXT NOT NULL DEFAULT ''"},
		{"series", "auto_upgrade", "TEXT NOT NULL DEFAULT ''"},
		{"movies", "rating", "DOUBLE PRECISION NOT NULL DEFAULT 0"},
		{"series", "rating", "DOUBLE PRECISION NOT NULL DEFAULT 0"},
		{"indexers", "daily_queries", "INTEGER NOT NULL DEFAULT 0"},
		{"indexers", "daily_grabs", "INTEGER NOT NULL DEFAULT 0"},
		{"usenet_servers", "quota", "BIGINT NOT NULL DEFAULT 0"},
		{"usenet_servers", "quota_since", "BIGINT NOT NULL DEFAULT 0"},
		{"usenet_servers", "expires", "BIGINT NOT NULL DEFAULT 0"},
		{"users", "language", "TEXT NOT NULL DEFAULT ''"},
		{"members", "language", "TEXT NOT NULL DEFAULT ''"},
		{"downloads", "edition", "TEXT NOT NULL DEFAULT ''"},
		{"downloads", "mode", "TEXT NOT NULL DEFAULT ''"},
		{"authors", "locked", "INTEGER NOT NULL DEFAULT 0"},
		{"downloads", "missing", "INTEGER NOT NULL DEFAULT 0"},
		{"downloads", "fetched", "INTEGER NOT NULL DEFAULT 0"},
	} {
		if _, err := s.db.ExecContext(ctx, "SELECT "+c.column+" FROM "+c.table+" WHERE 1 = 0"); err != nil {
			if _, err := s.db.ExecContext(ctx, "ALTER TABLE "+c.table+" ADD COLUMN "+c.column+" "+c.def); err != nil {
				return fmt.Errorf("add %s.%s: %w", c.table, c.column, err)
			}
			if fill := backfill[c.table+"."+c.column]; fill != "" {
				if _, err := s.db.ExecContext(ctx, fill); err != nil {
					return fmt.Errorf("fill in %s.%s: %w", c.table, c.column, err)
				}
			}
		}
	}
	// Radarr's and Sonarr's copies of one download client become one.
	if err := s.MergeDuplicateDownloadClients(ctx); err != nil {
		return err
	}
	// A new server starts with the default quality profiles.
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM quality_profiles").Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		for _, p := range quality.Defaults() {
			if _, err := s.CreateProfile(ctx, p); err != nil {
				return err
			}
		}
	}
	// Every server has the Do not touch profile.
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM quality_profiles WHERE locked = 1").Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		p := quality.DoNotTouch()
		if _, err := s.CreateProfile(ctx, p); errors.Is(err, ErrExists) {
			// Someone has a profile of that name already.
			p.Name += " (Box Office)"
			_, err = s.CreateProfile(ctx, p)
			return err
		} else if err != nil {
			return err
		}
	}
	return nil
}

func stripComments(s string) string {
	var b strings.Builder
	for line := range strings.SplitSeq(s, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// q rewrites ? placeholders to $1, $2… for PostgreSQL.
func (s *Store) q(query string) string {
	if !s.postgres || !strings.Contains(query, "?") {
		return query
	}
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (s *Store) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return s.db.ExecContext(ctx, s.q(query), args...)
}

func (s *Store) query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return s.db.QueryContext(ctx, s.q(query), args...)
}

func (s *Store) row(ctx context.Context, query string, args ...any) *sql.Row {
	return s.db.QueryRowContext(ctx, s.q(query), args...)
}

// insert runs an INSERT and returns the new row's id.
func (s *Store) insert(ctx context.Context, query string, args ...any) (int64, error) {
	if s.postgres {
		var id int64
		err := s.row(ctx, query+" RETURNING id", args...).Scan(&id)
		return id, uniqueErr(err)
	}
	res, err := s.exec(ctx, query, args...)
	if err != nil {
		return 0, uniqueErr(err)
	}
	return res.LastInsertId()
}

func uniqueErr(err error) error {
	if err != nil && (strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "duplicate key")) {
		return ErrExists
	}
	return err
}

func now() int64 { return time.Now().Unix() }

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// unix turns a nullable Unix time into a *time.Time.
func unix(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.Unix(v.Int64, 0).UTC()
	return &t
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Unix()
}

// SortTitle is how a title sorts: lower case, without a leading article.
func SortTitle(title string) string {
	t := strings.ToLower(strings.TrimSpace(title))
	for _, a := range []string{"the ", "a ", "an "} {
		if rest, ok := strings.CutPrefix(t, a); ok && rest != "" {
			t = rest
			break
		}
	}
	return strings.TrimLeftFunc(t, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// ---- settings ----

// Setting reads a setting; a missing one is "".
func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.row(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetSetting writes a setting; "" deletes it.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	if value == "" {
		_, err := s.exec(ctx, "DELETE FROM settings WHERE key = ?", key)
		return err
	}
	_, err := s.exec(ctx, "INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value", key, value)
	return err
}

// ---- quality profiles ----

func scanProfile(r interface{ Scan(...any) error }) (quality.Profile, error) {
	var p quality.Profile
	var allowed string
	var upgrade, locked int
	if err := r.Scan(&p.ID, &p.Name, &allowed, &p.Cutoff, &upgrade, &locked); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return p, ErrNotFound
		}
		return p, err
	}
	p.UpgradeOK, p.Locked = upgrade == 1, locked == 1
	return p, json.Unmarshal([]byte(allowed), &p.Allowed)
}

const profileCols = "id, name, allowed, cutoff, upgrade, locked"

func (s *Store) Profiles(ctx context.Context) ([]quality.Profile, error) {
	rows, err := s.query(ctx, "SELECT "+profileCols+" FROM quality_profiles ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []quality.Profile{}
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) Profile(ctx context.Context, id int) (quality.Profile, error) {
	return scanProfile(s.row(ctx, "SELECT "+profileCols+" FROM quality_profiles WHERE id = ?", id))
}

func (s *Store) CreateProfile(ctx context.Context, p quality.Profile) (quality.Profile, error) {
	if err := p.Check(); err != nil {
		return p, err
	}
	allowed, _ := json.Marshal(p.Allowed)
	id, err := s.insert(ctx, "INSERT INTO quality_profiles (name, allowed, cutoff, upgrade, locked) VALUES (?, ?, ?, ?, ?)",
		strings.TrimSpace(p.Name), string(allowed), p.Cutoff, b2i(p.UpgradeOK), b2i(p.Locked))
	p.ID = int(id)
	return p, uniqueErr(err)
}

func (s *Store) UpdateProfile(ctx context.Context, p quality.Profile) error {
	if err := p.Check(); err != nil {
		return err
	}
	allowed, _ := json.Marshal(p.Allowed)
	res, err := s.exec(ctx, "UPDATE quality_profiles SET name = ?, allowed = ?, cutoff = ?, upgrade = ? WHERE id = ?",
		strings.TrimSpace(p.Name), string(allowed), p.Cutoff, b2i(p.UpgradeOK), p.ID)
	if err != nil {
		return uniqueErr(err)
	}
	return affected(res)
}

// ErrLocked means a title is on the Do not touch profile, so Box Office
// leaves its files as they are.
var ErrLocked = errors.New("it's set to Do not touch, so Box Office leaves its files alone; pick another quality profile for it first")

// TitleLocked says whether a movie, a show, or an episode's show is on the
// Do not touch profile, or a book's author is set to Do not touch. kind is
// movie, series, episode, author, or ebook or audiobook for a book.
func (s *Store) TitleLocked(ctx context.Context, kind string, id int64) (bool, error) {
	q := map[string]string{
		"movie":   "SELECT p.locked FROM movies t JOIN quality_profiles p ON p.id = t.profile_id WHERE t.id = ?",
		"series":  "SELECT p.locked FROM series t JOIN quality_profiles p ON p.id = t.profile_id WHERE t.id = ?",
		"episode": "SELECT p.locked FROM episodes e JOIN series t ON t.id = e.series_id JOIN quality_profiles p ON p.id = t.profile_id WHERE e.id = ?",
		"author":  "SELECT locked FROM authors WHERE id = ?",
		AlbumKind: "SELECT 0 FROM albums WHERE id = ?",
		Ebook:     "SELECT a.locked FROM books b JOIN authors a ON a.id = b.author_id WHERE b.id = ?",
		Audiobook: "SELECT a.locked FROM books b JOIN authors a ON a.id = b.author_id WHERE b.id = ?",
	}[kind]
	if q == "" {
		return false, fmt.Errorf("no such kind of title %q", kind)
	}
	var locked int
	err := s.row(ctx, q, id).Scan(&locked)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return locked == 1, err
}

// ErrAuthorLocked is ErrLocked for a book: its author is on Do not touch.
// errors.Is(ErrAuthorLocked, ErrLocked) holds.
var ErrAuthorLocked error = authorLocked{}

type authorLocked struct{}

func (authorLocked) Error() string {
	return "its author is set to Do not touch, so Box Office leaves their books alone; turn that off on the author's page first"
}
func (authorLocked) Is(target error) bool { return target == ErrLocked }

// CheckUnlocked is ErrLocked when the title is on Do not touch
// (ErrAuthorLocked for a book).
func (s *Store) CheckUnlocked(ctx context.Context, kind string, id int64) error {
	if locked, err := s.TitleLocked(ctx, kind, id); err != nil {
		return err
	} else if locked {
		if IsBookKind(kind) || kind == "author" {
			return ErrAuthorLocked
		}
		return ErrLocked
	}
	return nil
}

// ErrInUse means something still refers to the row.
var ErrInUse = errors.New("in use")

func (s *Store) DeleteProfile(ctx context.Context, id int) error {
	var n int
	if err := s.row(ctx, "SELECT (SELECT COUNT(*) FROM movies WHERE profile_id = ?) + (SELECT COUNT(*) FROM series WHERE profile_id = ?)", id, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrInUse
	}
	res, err := s.exec(ctx, "DELETE FROM quality_profiles WHERE id = ?", id)
	if err != nil {
		return err
	}
	return affected(res)
}

func affected(res sql.Result) error {
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- root folders ----

// RootFolder is a folder new titles of one kind go in.
type RootFolder struct {
	ID   int    `json:"id"`
	Kind string `json:"kind"` // movie or series
	Path string `json:"path"`
}

func (s *Store) RootFolders(ctx context.Context, kind string) ([]RootFolder, error) {
	rows, err := s.query(ctx, "SELECT id, kind, path FROM root_folders WHERE kind = ? OR ? = '' ORDER BY path", kind, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RootFolder{}
	for rows.Next() {
		var f RootFolder
		if err := rows.Scan(&f.ID, &f.Kind, &f.Path); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) AddRootFolder(ctx context.Context, kind, path string) (RootFolder, error) {
	id, err := s.insert(ctx, "INSERT INTO root_folders (kind, path) VALUES (?, ?)", kind, path)
	return RootFolder{ID: int(id), Kind: kind, Path: path}, err
}

func (s *Store) DeleteRootFolder(ctx context.Context, id int) error {
	res, err := s.exec(ctx, "DELETE FROM root_folders WHERE id = ?", id)
	if err != nil {
		return err
	}
	return affected(res)
}
