package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// What a clean-up rule goes by.
const (
	CleanupEveryone = "everyone" // everyone who started it has finished it
	CleanupAnyone   = "anyone"   // someone has finished it
	CleanupNobody   = "nobody"   // nobody has played it
)

// Why a clean-up rule is waiting.
const (
	CleanupNoReport = "report" // Matinee hasn't said lately what was watched
	CleanupNoPlays  = "plays"  // Matinee says nothing at all was played, which a "nobody" rule won't take on trust
)

// CleanupRule says which titles leave the library.
type CleanupRule struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"` // movie or series
	// Watched is everyone, anyone or nobody, and Days how long it has been
	// quiet: since its last play, or since it was added for nobody.
	Watched string `json:"watched"`
	Days    int    `json:"days"`
	// EndedOnly leaves shows that are still running alone.
	EndedOnly bool `json:"endedOnly"`
	// GraceDays is how long a title is listed as leaving soon before it
	// can go.
	GraceDays int `json:"graceDays"`
	// Auto deletes titles by itself once those days are up. Without it the
	// rule only lists them.
	Auto      bool       `json:"auto"`
	Enabled   bool       `json:"enabled"`
	CheckedAt *time.Time `json:"checkedAt,omitempty"`
	Waiting   string     `json:"waiting,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
}

// CleanupItem is a title listed as leaving soon.
type CleanupItem struct {
	ID      int64  `json:"id"`
	RuleID  int64  `json:"ruleId"`
	Kind    string `json:"kind"`
	TitleID int64  `json:"titleId"`
	// Files and Size are what it had on disk when it was listed.
	Files    int       `json:"files"`
	Size     int64     `json:"size"`
	ListedAt time.Time `json:"listedAt"`
	LeavesAt time.Time `json:"leavesAt"`
	Problem  string    `json:"problem,omitempty"`
}

const cleanupRuleCols = "id, name, kind, watched, days, ended_only, grace_days, auto, enabled, checked_at, waiting, created_at"

func scanCleanupRule(row interface{ Scan(...any) error }) (CleanupRule, error) {
	var r CleanupRule
	var ended, auto, enabled int
	var checked, created int64
	err := row.Scan(&r.ID, &r.Name, &r.Kind, &r.Watched, &r.Days, &ended, &r.GraceDays, &auto, &enabled, &checked, &r.Waiting, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	r.EndedOnly, r.Auto, r.Enabled = ended != 0, auto != 0, enabled != 0
	if checked > 0 {
		t := time.Unix(checked, 0).UTC()
		r.CheckedAt = &t
	}
	r.CreatedAt = time.Unix(created, 0).UTC()
	return r, err
}

// CleanupRules is every clean-up rule, oldest first.
func (s *Store) CleanupRules(ctx context.Context) ([]CleanupRule, error) {
	rows, err := s.query(ctx, "SELECT "+cleanupRuleCols+" FROM cleanup_rules ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CleanupRule{}
	for rows.Next() {
		r, err := scanCleanupRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CleanupRule is one clean-up rule.
func (s *Store) CleanupRule(ctx context.Context, id int64) (CleanupRule, error) {
	return scanCleanupRule(s.row(ctx, "SELECT "+cleanupRuleCols+" FROM cleanup_rules WHERE id = ?", id))
}

// AddCleanupRule saves a new rule.
func (s *Store) AddCleanupRule(ctx context.Context, r CleanupRule) (CleanupRule, error) {
	id, err := s.insert(ctx, `INSERT INTO cleanup_rules (name, kind, watched, days, ended_only, grace_days, auto, enabled, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.Name, r.Kind, r.Watched, r.Days, b2i(r.EndedOnly), r.GraceDays, b2i(r.Auto), b2i(r.Enabled), now())
	if err != nil {
		return r, err
	}
	return s.CleanupRule(ctx, id)
}

// UpdateCleanupRule saves a rule's settings. What it listed as leaving
// soon is taken off the list: the changed rule lists afresh, and each
// title's days start over.
func (s *Store) UpdateCleanupRule(ctx context.Context, r CleanupRule) error {
	if _, err := s.exec(ctx, "DELETE FROM cleanup_items WHERE rule_id = ?", r.ID); err != nil {
		return err
	}
	_, err := s.exec(ctx, `UPDATE cleanup_rules SET name = ?, kind = ?, watched = ?, days = ?, ended_only = ?, grace_days = ?, auto = ?, enabled = ?, waiting = '' WHERE id = ?`,
		r.Name, r.Kind, r.Watched, r.Days, b2i(r.EndedOnly), r.GraceDays, b2i(r.Auto), b2i(r.Enabled), r.ID)
	return err
}

// CleanupChecked notes that a rule was looked at, and why it's waiting if
// it is.
func (s *Store) CleanupChecked(ctx context.Context, id int64, waiting string) error {
	_, err := s.exec(ctx, "UPDATE cleanup_rules SET checked_at = ?, waiting = ? WHERE id = ?", now(), waiting, id)
	return err
}

// DeleteCleanupRule removes a rule and takes what it listed off the list.
func (s *Store) DeleteCleanupRule(ctx context.Context, id int64) error {
	if _, err := s.exec(ctx, "DELETE FROM cleanup_items WHERE rule_id = ?", id); err != nil {
		return err
	}
	_, err := s.exec(ctx, "DELETE FROM cleanup_rules WHERE id = ?", id)
	return err
}

const cleanupItemCols = "id, rule_id, kind, title_id, files, size, listed_at, leaves_at, problem"

func scanCleanupItem(row interface{ Scan(...any) error }) (CleanupItem, error) {
	var i CleanupItem
	var listed, leaves int64
	err := row.Scan(&i.ID, &i.RuleID, &i.Kind, &i.TitleID, &i.Files, &i.Size, &listed, &leaves, &i.Problem)
	if errors.Is(err, sql.ErrNoRows) {
		return i, ErrNotFound
	}
	i.ListedAt, i.LeavesAt = time.Unix(listed, 0).UTC(), time.Unix(leaves, 0).UTC()
	return i, err
}

// CleanupItems is every title leaving soon, soonest first.
func (s *Store) CleanupItems(ctx context.Context) ([]CleanupItem, error) {
	rows, err := s.query(ctx, "SELECT "+cleanupItemCols+" FROM cleanup_items ORDER BY leaves_at, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CleanupItem{}
	for rows.Next() {
		i, err := scanCleanupItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// CleanupItem is one title leaving soon.
func (s *Store) CleanupItem(ctx context.Context, id int64) (CleanupItem, error) {
	return scanCleanupItem(s.row(ctx, "SELECT "+cleanupItemCols+" FROM cleanup_items WHERE id = ?", id))
}

// ListCleanupItem lists a title as leaving soon. One that's listed already
// (by any rule) stays as it is; listed says whether this call listed it.
func (s *Store) ListCleanupItem(ctx context.Context, i CleanupItem) (listed bool, err error) {
	res, err := s.exec(ctx, `INSERT INTO cleanup_items (rule_id, kind, title_id, files, size, listed_at, leaves_at) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (kind, title_id) DO NOTHING`, i.RuleID, i.Kind, i.TitleID, i.Files, i.Size, now(), i.LeavesAt.Unix())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// RelistCleanupItem notes what a listed title has on disk now, when it
// can go, and why it couldn't if it couldn't.
func (s *Store) RelistCleanupItem(ctx context.Context, id int64, files int, size int64, leaves time.Time, problem string) error {
	_, err := s.exec(ctx, "UPDATE cleanup_items SET files = ?, size = ?, leaves_at = ?, problem = ? WHERE id = ?", files, size, leaves.Unix(), problem, id)
	return err
}

// DropCleanupItem takes a title off the leaving soon list.
func (s *Store) DropCleanupItem(ctx context.Context, id int64) error {
	_, err := s.exec(ctx, "DELETE FROM cleanup_items WHERE id = ?", id)
	return err
}

// KeepFromCleanup has no clean-up rule list a title again, and takes it
// off the leaving soon list.
func (s *Store) KeepFromCleanup(ctx context.Context, kind string, titleID int64) error {
	if _, err := s.exec(ctx, "INSERT INTO cleanup_kept (kind, title_id, kept_at) VALUES (?, ?, ?) ON CONFLICT (kind, title_id) DO NOTHING", kind, titleID, now()); err != nil {
		return err
	}
	_, err := s.exec(ctx, "DELETE FROM cleanup_items WHERE kind = ? AND title_id = ?", kind, titleID)
	return err
}

// UnkeepFromCleanup lets clean-up rules list a title again.
func (s *Store) UnkeepFromCleanup(ctx context.Context, kind string, titleID int64) error {
	_, err := s.exec(ctx, "DELETE FROM cleanup_kept WHERE kind = ? AND title_id = ?", kind, titleID)
	return err
}

// CleanupKept is the titles of a kind an admin said to keep, with when.
func (s *Store) CleanupKept(ctx context.Context, kind string) (map[int64]time.Time, error) {
	rows, err := s.query(ctx, "SELECT title_id, kept_at FROM cleanup_kept WHERE kind = ?", kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]time.Time{}
	for rows.Next() {
		var id, at int64
		if err := rows.Scan(&id, &at); err != nil {
			return nil, err
		}
		out[id] = time.Unix(at, 0).UTC()
	}
	return out, rows.Err()
}

// LockedTitles is the ids of the movies or shows (kind movie or series) on
// a Do not touch profile.
func (s *Store) LockedTitles(ctx context.Context, kind string) (map[int64]bool, error) {
	table := "movies"
	if kind == "series" {
		table = "series"
	}
	rows, err := s.query(ctx, "SELECT t.id FROM "+table+" t JOIN quality_profiles p ON p.id = t.profile_id WHERE p.locked <> 0")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
