package store

import "context"

// Listed is a title's folder, for telling what in a root folder is on the
// list already.
type Listed struct {
	Kind  string // movie, series or music
	ID    int64
	Title string
	Path  string
}

// ListedFolders is every movie's, show's and artist's folder.
func (s *Store) ListedFolders(ctx context.Context) ([]Listed, error) {
	rows, err := s.query(ctx, `SELECT 'movie', id, title, path FROM movies
		UNION ALL SELECT 'series', id, title, path FROM series
		UNION ALL SELECT 'music', id, name, path FROM artists`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Listed
	for rows.Next() {
		var l Listed
		if err := rows.Scan(&l.Kind, &l.ID, &l.Title, &l.Path); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// FilePaths is every file Box Office keeps track of.
func (s *Store) FilePaths(ctx context.Context) ([]string, error) {
	rows, err := s.query(ctx, "SELECT path FROM media_files")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
