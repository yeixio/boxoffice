package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// List is a list followed from elsewhere: what's on it is added by itself.
type List struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Source  string `json:"source"` // tmdb, trakt or plex
	Address string `json:"address"`
	Enabled bool   `json:"enabled"`
	// Ask has new titles wait for a yes instead of being added.
	Ask bool `json:"ask"`
	// Search looks for a title once it's added.
	Search       bool   `json:"search"`
	Movies       bool   `json:"movies"`
	Shows        bool   `json:"shows"`
	MovieProfile int    `json:"movieProfile"`
	MovieRoot    string `json:"movieRoot"`
	ShowProfile  int    `json:"showProfile"`
	ShowRoot     string `json:"showRoot"`
	// CheckedAt is when it was last read; LastError is why that failed.
	CheckedAt *time.Time `json:"checkedAt,omitempty"`
	LastError string     `json:"lastError,omitempty"`
	AddedAt   time.Time  `json:"addedAt"`
}

// What became of a title on a followed list.
const (
	ListWaiting = "waiting" // waits for a yes
	ListAdded   = "added"   // added for the list
	ListHad     = "had"     // was here already
	ListRemoved = "removed" // added for a list, then removed here: not added again
	ListSkipped = "skipped" // said no to
	ListFailed  = "failed"  // couldn't be added; tried again next time
)

// ListItem is a title a followed list has had.
type ListItem struct {
	ListID int64     `json:"listId"`
	Kind   string    `json:"kind"` // movie or series
	TMDBID int64     `json:"tmdbId"`
	Title  string    `json:"title"`
	Year   int       `json:"year,omitempty"`
	State  string    `json:"state"`
	Note   string    `json:"note,omitempty"`
	SeenAt time.Time `json:"seenAt"`
}

const listCols = "id, name, source, address, enabled, ask, search, movies, shows, movie_profile, movie_root, show_profile, show_root, checked_at, last_error, added_at"

func scanList(row interface{ Scan(...any) error }) (List, error) {
	var l List
	var enabled, ask, search, movies, shows int
	var checked, added int64
	err := row.Scan(&l.ID, &l.Name, &l.Source, &l.Address, &enabled, &ask, &search, &movies, &shows,
		&l.MovieProfile, &l.MovieRoot, &l.ShowProfile, &l.ShowRoot, &checked, &l.LastError, &added)
	if errors.Is(err, sql.ErrNoRows) {
		return l, ErrNotFound
	}
	l.Enabled, l.Ask, l.Search, l.Movies, l.Shows = enabled != 0, ask != 0, search != 0, movies != 0, shows != 0
	if checked > 0 {
		t := time.Unix(checked, 0).UTC()
		l.CheckedAt = &t
	}
	l.AddedAt = time.Unix(added, 0).UTC()
	return l, err
}

// Lists is every followed list, oldest first.
func (s *Store) Lists(ctx context.Context) ([]List, error) {
	rows, err := s.query(ctx, "SELECT "+listCols+" FROM lists ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []List{}
	for rows.Next() {
		l, err := scanList(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// List is one followed list.
func (s *Store) List(ctx context.Context, id int64) (List, error) {
	return scanList(s.row(ctx, "SELECT "+listCols+" FROM lists WHERE id = ?", id))
}

// AddList follows a list.
func (s *Store) AddList(ctx context.Context, l List) (List, error) {
	id, err := s.insert(ctx, `INSERT INTO lists (name, source, address, enabled, ask, search, movies, shows, movie_profile, movie_root, show_profile, show_root, added_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		l.Name, l.Source, l.Address, b2i(l.Enabled), b2i(l.Ask), b2i(l.Search), b2i(l.Movies), b2i(l.Shows),
		l.MovieProfile, l.MovieRoot, l.ShowProfile, l.ShowRoot, now())
	if err != nil {
		return l, err
	}
	return s.List(ctx, id)
}

// UpdateList saves a list's settings; where it reads from stays.
func (s *Store) UpdateList(ctx context.Context, l List) error {
	_, err := s.exec(ctx, `UPDATE lists SET name = ?, enabled = ?, ask = ?, search = ?, movies = ?, shows = ?,
		movie_profile = ?, movie_root = ?, show_profile = ?, show_root = ? WHERE id = ?`,
		l.Name, b2i(l.Enabled), b2i(l.Ask), b2i(l.Search), b2i(l.Movies), b2i(l.Shows),
		l.MovieProfile, l.MovieRoot, l.ShowProfile, l.ShowRoot, l.ID)
	return err
}

// ListChecked notes that a list was read, and why that failed if it did.
func (s *Store) ListChecked(ctx context.Context, id int64, problem string) error {
	_, err := s.exec(ctx, "UPDATE lists SET checked_at = ?, last_error = ? WHERE id = ?", now(), problem, id)
	return err
}

// DeleteList stops following a list and forgets what it had.
func (s *Store) DeleteList(ctx context.Context, id int64) error {
	if _, err := s.exec(ctx, "DELETE FROM list_items WHERE list_id = ?", id); err != nil {
		return err
	}
	_, err := s.exec(ctx, "DELETE FROM lists WHERE id = ?", id)
	return err
}

// ListItems is what a list has had, newest first.
func (s *Store) ListItems(ctx context.Context, listID int64) ([]ListItem, error) {
	rows, err := s.query(ctx, "SELECT list_id, kind, tmdb_id, title, year, state, note, seen_at FROM list_items WHERE list_id = ? ORDER BY seen_at DESC, title", listID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ListItem{}
	for rows.Next() {
		var it ListItem
		var seen int64
		if err := rows.Scan(&it.ListID, &it.Kind, &it.TMDBID, &it.Title, &it.Year, &it.State, &it.Note, &seen); err != nil {
			return nil, err
		}
		it.SeenAt = time.Unix(seen, 0).UTC()
		out = append(out, it)
	}
	return out, rows.Err()
}

// SetListItem notes what became of a title on a list. When it was first
// seen stays.
func (s *Store) SetListItem(ctx context.Context, it ListItem) error {
	_, err := s.exec(ctx, `INSERT INTO list_items (list_id, kind, tmdb_id, title, year, state, note, seen_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (list_id, kind, tmdb_id) DO UPDATE SET title = excluded.title, year = excluded.year, state = excluded.state, note = excluded.note`,
		it.ListID, it.Kind, it.TMDBID, it.Title, it.Year, it.State, it.Note, now())
	return err
}

// ListAddedBefore says whether any list added a title (or found it removed
// after one did), so a title removed here isn't added again by another.
func (s *Store) ListAddedBefore(ctx context.Context, kind string, tmdbID int64) (bool, error) {
	var n int
	err := s.row(ctx, "SELECT COUNT(*) FROM list_items WHERE kind = ? AND tmdb_id = ? AND state IN (?, ?)", kind, tmdbID, ListAdded, ListRemoved).Scan(&n)
	return n > 0, err
}

// ListCounts is how many titles of each state each list has.
func (s *Store) ListCounts(ctx context.Context) (map[int64]map[string]int, error) {
	rows, err := s.query(ctx, "SELECT list_id, state, COUNT(*) FROM list_items GROUP BY list_id, state")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]map[string]int{}
	for rows.Next() {
		var id int64
		var state string
		var n int
		if err := rows.Scan(&id, &state, &n); err != nil {
			return nil, err
		}
		if out[id] == nil {
			out[id] = map[string]int{}
		}
		out[id][state] = n
	}
	return out, rows.Err()
}
