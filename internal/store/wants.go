package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Wants say what was asked for a movie or an episode beyond its quality
// profile (see title_wants in schema.sql).
const (
	// WantBetter looks for a better copy than the one there, even past
	// the profile's cutoff.
	WantBetter = "better"
	// WantReplace looks for another copy because the one there is bad
	// (wrong language, out of sync, broken): any copy the profile takes
	// will do, except the one there.
	WantReplace = "replace"
)

// Edition modes: how an edition asked for goes into the library.
const (
	// ModeReplace takes the place of the copy there (which goes to the
	// recycle folder, kept as long as Settings says).
	ModeReplace = "replace"
	// ModeAlongside goes in next to the copy there, named with its
	// edition, so both can be watched.
	ModeAlongside = "alongside"
)

// TitleWant is what was asked for one movie or episode.
type TitleWant struct {
	Kind    string    `json:"kind"` // movie or episode
	OwnerID int64     `json:"ownerId"`
	Edition string    `json:"edition,omitempty"`
	Want    string    `json:"want,omitempty"`
	Note    string    `json:"note,omitempty"`
	Updated time.Time `json:"updatedAt"`
}

// TitleWant is what was asked for a movie or an episode; the zero value
// when nothing was.
func (s *Store) TitleWant(ctx context.Context, kind string, id int64) (TitleWant, error) {
	w := TitleWant{Kind: kind, OwnerID: id}
	var at int64
	err := s.row(ctx, "SELECT edition, want, note, updated_at FROM title_wants WHERE kind = ? AND owner_id = ?", kind, id).
		Scan(&w.Edition, &w.Want, &w.Note, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return w, nil
	}
	if at > 0 {
		w.Updated = time.Unix(at, 0).UTC()
	}
	return w, err
}

// SetTitleWant records what was asked; an empty one is forgotten.
func (s *Store) SetTitleWant(ctx context.Context, w TitleWant) error {
	if w.Edition == "" && w.Want == "" {
		_, err := s.exec(ctx, "DELETE FROM title_wants WHERE kind = ? AND owner_id = ?", w.Kind, w.OwnerID)
		return err
	}
	_, err := s.exec(ctx, `INSERT INTO title_wants (kind, owner_id, edition, want, note, updated_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (kind, owner_id) DO UPDATE SET edition = excluded.edition, want = excluded.want, note = excluded.note, updated_at = excluded.updated_at`,
		w.Kind, w.OwnerID, w.Edition, w.Want, w.Note, now())
	return err
}

// ClearWant forgets a better or replacement copy asked for, once one is
// in; the edition asked for stays.
func (s *Store) ClearWant(ctx context.Context, kind string, id int64) error {
	w, err := s.TitleWant(ctx, kind, id)
	if err != nil || w.Want == "" {
		return err
	}
	w.Want, w.Note = "", ""
	return s.SetTitleWant(ctx, w)
}

// AddEditionFile records a movie's extra edition, kept next to its main
// copy (a Director's Cut alongside the theatrical one, say). It doesn't
// count as the movie's copy: searches and upgrades look at that one.
func (s *Store) AddEditionFile(ctx context.Context, movieID int64, path string, size int64, q int) (File, error) {
	if _, err := s.exec(ctx, "DELETE FROM media_files WHERE path = ?", path); err != nil {
		return File{}, err
	}
	f := File{Kind: "edition", OwnerID: movieID, Path: path, Size: size, Quality: q}
	id, err := s.insert(ctx, "INSERT INTO media_files (kind, owner_id, path, size, quality, added_at) VALUES (?, ?, ?, ?, ?, ?)",
		f.Kind, f.OwnerID, f.Path, f.Size, f.Quality, now())
	f.ID = id
	return f, err
}

// EditionFiles are a movie's extra editions.
func (s *Store) EditionFiles(ctx context.Context, movieID int64) ([]File, error) {
	rows, err := s.query(ctx, "SELECT id, kind, owner_id, path, size, quality, added_at, media, guessed FROM media_files WHERE kind = 'edition' AND owner_id = ? ORDER BY path", movieID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []File{}
	for rows.Next() {
		var f File
		var added int64
		var media sql.NullString
		var guessed sql.NullInt64
		if err := rows.Scan(&f.ID, &f.Kind, &f.OwnerID, &f.Path, &f.Size, &f.Quality, &added, &media, &guessed); err != nil {
			return nil, err
		}
		f.AddedAt = time.Unix(added, 0).UTC()
		f.checked(media, guessed)
		out = append(out, f)
	}
	return out, rows.Err()
}

// RecentDownloads are downloads that finished since t, newest first.
func (s *Store) RecentDownloads(ctx context.Context, since time.Time) ([]Download, error) {
	return s.downloads(ctx, "finished_at >= ? AND status IN ('completed', 'failed') ORDER BY finished_at DESC LIMIT 500", since.Unix())
}

// DownloadImportedTo is the newest download moved in to path, for the
// release a copy on disk came from.
func (s *Store) DownloadImportedTo(ctx context.Context, path string) (Download, error) {
	list, err := s.downloads(ctx, "imported_to = ? ORDER BY finished_at DESC LIMIT 1", path)
	if err != nil {
		return Download{}, err
	}
	if len(list) == 0 {
		return Download{}, ErrNotFound
	}
	return list[0], nil
}
