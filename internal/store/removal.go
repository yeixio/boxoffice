package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
)

// OtherTitleIn names a movie or show, other than the one given, whose
// folder is folder or inside it, or that has a file inside it: deleting
// folder would take that title's files too. "" means there's none.
func (s *Store) OtherTitleIn(ctx context.Context, folder, kind string, id int64) (string, error) {
	folder = filepath.Clean(folder)
	prefix := folder + string(filepath.Separator)
	n := len(prefix)
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
	// Files kept somewhere other than their title's folder.
	var path string
	var err error
	if kind == "movie" {
		err = s.row(ctx, "SELECT path FROM media_files WHERE substr(path, 1, ?) = ? AND NOT (kind = 'movie' AND owner_id = ?) LIMIT 1", n, prefix, id).Scan(&path)
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

// ImportsFor is the finished downloads that were moved into the library
// for a title, newest first; with episode ids, only those that brought
// one of the episodes.
func (s *Store) ImportsFor(ctx context.Context, kind string, titleID int64, episodeIDs ...int64) ([]Download, error) {
	list, err := s.downloads(ctx, "kind = ? AND title_id = ? AND status = 'completed' AND imported_to <> '' ORDER BY finished_at DESC, id DESC", kind, titleID)
	if err != nil || len(episodeIDs) == 0 {
		return list, err
	}
	out := []Download{}
	for _, d := range list {
		for _, e := range d.EpisodeIDs {
			if slices.Contains(episodeIDs, e) {
				out = append(out, d)
				break
			}
		}
	}
	return out, nil
}

// SetSeasonEpisodesMonitored watches, or stops watching, every episode of a
// season.
func (s *Store) SetSeasonEpisodesMonitored(ctx context.Context, seriesID int64, season int, monitored bool) error {
	_, err := s.exec(ctx, "UPDATE episodes SET monitored = ? WHERE series_id = ? AND season = ?", b2i(monitored), seriesID, season)
	return err
}

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

// RecordJob notes, on Activity, something done right away rather than
// queued, such as files deleted from the library.
func (s *Store) RecordJob(ctx context.Context, kind, subject, status, message string) error {
	t := now()
	_, err := s.insert(ctx, "INSERT INTO jobs (kind, subject, payload, status, message, created_at, started_at, finished_at) VALUES (?, ?, '{}', ?, ?, ?, ?, ?)",
		kind, subject, status, message, t, t, t)
	return err
}

// LibraryRoots is every library folder: the root folders, and any other a
// title sits in (one moved in from another app can keep its old one).
func (s *Store) LibraryRoots(ctx context.Context) ([]string, error) {
	rows, err := s.query(ctx, "SELECT path FROM root_folders UNION SELECT root_folder FROM movies UNION SELECT root_folder FROM series")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		if p != "" && !slices.Contains(out, filepath.Clean(p)) {
			out = append(out, filepath.Clean(p))
		}
	}
	slices.Sort(out)
	return out, rows.Err()
}
