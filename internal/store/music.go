package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Artist is a music artist on the list.
type Artist struct {
	ID             int64     `json:"id"`
	MBID           string    `json:"mbid"`
	Name           string    `json:"name"`
	SortName       string    `json:"sortName"`
	Type           string    `json:"type,omitempty"`
	Country        string    `json:"country,omitempty"`
	Disambiguation string    `json:"disambiguation,omitempty"`
	Monitored      bool      `json:"monitored"`
	AlbumTypes     []string  `json:"albumTypes"`
	RootFolder     string    `json:"rootFolder"`
	Path           string    `json:"path"`
	AddedAt        time.Time `json:"addedAt"`
	RefreshedAt    time.Time `json:"refreshedAt"`
	// Counted from its albums when listed.
	Albums       int `json:"albums"`
	WantedAlbums int `json:"wantedAlbums"`
	// HaveAlbums is how many of its albums are in the library.
	HaveAlbums int `json:"haveAlbums"`
}

// AlbumKind is the kind of an album's downloads and files.
const AlbumKind = "album"

// Album is one of an artist's albums, EPs or singles.
type Album struct {
	ID             int64    `json:"id"`
	ArtistID       int64    `json:"artistId"`
	MBID           string   `json:"mbid"`
	Title          string   `json:"title"`
	Type           string   `json:"type"`
	SecondaryTypes []string `json:"secondaryTypes"`
	Released       string   `json:"released,omitempty"`
	Monitored      bool     `json:"monitored"`
	// File is the album in the library: its folder, and the size of its
	// tracks.
	File *File `json:"file,omitempty"`
	// ArtistName is filled in where albums of several artists are listed.
	ArtistName string `json:"artistName,omitempty"`
}

const artistCols = `a.id, a.mbid, a.name, a.sort_name, a.type, a.country, a.disambiguation, a.monitored, a.album_types,
	a.root_folder, a.path, a.added_at, a.refreshed_at,
	(SELECT COUNT(*) FROM albums b WHERE b.artist_id = a.id),
	(SELECT COUNT(*) FROM albums b WHERE b.artist_id = a.id AND b.monitored = 1),
	(SELECT COUNT(*) FROM albums b WHERE b.artist_id = a.id
		AND EXISTS (SELECT 1 FROM media_files f WHERE f.kind = 'album' AND f.owner_id = b.id))`

func scanArtist(r interface{ Scan(...any) error }) (Artist, error) {
	var a Artist
	var mon int
	var types string
	var added, refreshed int64
	err := r.Scan(&a.ID, &a.MBID, &a.Name, &a.SortName, &a.Type, &a.Country, &a.Disambiguation, &mon, &types,
		&a.RootFolder, &a.Path, &added, &refreshed, &a.Albums, &a.WantedAlbums, &a.HaveAlbums)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	if err != nil {
		return a, err
	}
	a.Monitored = mon == 1
	if json.Unmarshal([]byte(types), &a.AlbumTypes) != nil || a.AlbumTypes == nil {
		a.AlbumTypes = []string{}
	}
	a.AddedAt = time.Unix(added, 0).UTC()
	if refreshed > 0 {
		a.RefreshedAt = time.Unix(refreshed, 0).UTC()
	}
	return a, nil
}

// Artists lists the artists on the list, by sort name.
func (s *Store) Artists(ctx context.Context) ([]Artist, error) {
	rows, err := s.query(ctx, "SELECT "+artistCols+" FROM artists a ORDER BY a.sort_name, a.id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Artist{}
	for rows.Next() {
		a, err := scanArtist(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Artist finds one artist.
func (s *Store) Artist(ctx context.Context, id int64) (Artist, error) {
	return scanArtist(s.row(ctx, "SELECT "+artistCols+" FROM artists a WHERE a.id = ?", id))
}

// ArtistByMBID finds an artist by MusicBrainz id.
func (s *Store) ArtistByMBID(ctx context.Context, mbid string) (Artist, error) {
	return scanArtist(s.row(ctx, "SELECT "+artistCols+" FROM artists a WHERE a.mbid = ?", strings.ToLower(mbid)))
}

func jsonList(v []string) string {
	if v == nil {
		v = []string{}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// AddArtist puts an artist on the list; ErrExists if it's there already.
func (s *Store) AddArtist(ctx context.Context, a Artist) (Artist, error) {
	if a.SortName == "" {
		a.SortName = a.Name
	}
	id, err := s.insert(ctx, `INSERT INTO artists (mbid, name, sort_name, type, country, disambiguation, monitored, album_types, root_folder, path, added_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		strings.ToLower(a.MBID), a.Name, strings.ToLower(a.SortName), a.Type, a.Country, a.Disambiguation, b2i(a.Monitored), jsonList(a.AlbumTypes), a.RootFolder, a.Path, now())
	if err != nil {
		return Artist{}, err
	}
	return s.Artist(ctx, id)
}

// UpdateArtist saves what MusicBrainz says about an artist, and the
// choices made for it.
func (s *Store) UpdateArtist(ctx context.Context, a Artist) error {
	_, err := s.exec(ctx, `UPDATE artists SET name = ?, sort_name = ?, type = ?, country = ?, disambiguation = ?, monitored = ?, album_types = ?,
		root_folder = ?, path = ? WHERE id = ?`,
		a.Name, strings.ToLower(a.SortName), a.Type, a.Country, a.Disambiguation, b2i(a.Monitored), jsonList(a.AlbumTypes), a.RootFolder, a.Path, a.ID)
	return err
}

// ArtistRefreshed notes that an artist's albums were just brought up to
// date.
func (s *Store) ArtistRefreshed(ctx context.Context, id int64) error {
	_, err := s.exec(ctx, "UPDATE artists SET refreshed_at = ? WHERE id = ?", now(), id)
	return err
}

// DeleteArtist takes an artist and its albums off the list. It doesn't
// touch any files: Box Office just forgets them.
func (s *Store) DeleteArtist(ctx context.Context, id int64) error {
	if _, err := s.exec(ctx, "DELETE FROM media_files WHERE kind = 'album' AND owner_id IN (SELECT id FROM albums WHERE artist_id = ?)", id); err != nil {
		return err
	}
	if _, err := s.exec(ctx, "DELETE FROM albums WHERE artist_id = ?", id); err != nil {
		return err
	}
	res, err := s.exec(ctx, "DELETE FROM artists WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

const albumCols = "id, artist_id, mbid, title, type, secondary_types, released, monitored"

func scanAlbum(r interface{ Scan(...any) error }) (Album, error) {
	var b Album
	var mon int
	var sec string
	err := r.Scan(&b.ID, &b.ArtistID, &b.MBID, &b.Title, &b.Type, &sec, &b.Released, &mon)
	if errors.Is(err, sql.ErrNoRows) {
		return b, ErrNotFound
	}
	if err != nil {
		return b, err
	}
	b.Monitored = mon == 1
	if json.Unmarshal([]byte(sec), &b.SecondaryTypes) != nil || b.SecondaryTypes == nil {
		b.SecondaryTypes = []string{}
	}
	return b, nil
}

// Albums lists an artist's albums, newest first.
func (s *Store) Albums(ctx context.Context, artistID int64) ([]Album, error) {
	return s.albumList(ctx, "SELECT "+albumCols+" FROM albums WHERE artist_id = ? ORDER BY released DESC, title", artistID)
}

func (s *Store) albumList(ctx context.Context, q string, args ...any) ([]Album, error) {
	rows, err := s.query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	out := []Album{}
	for rows.Next() {
		b, err := scanAlbum(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, b)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return out, s.albumFiles(ctx, out)
}

// albumFiles fills in the albums' copies in the library.
func (s *Store) albumFiles(ctx context.Context, list []Album) error {
	if len(list) == 0 {
		return nil
	}
	at := map[int64]int{}
	for i, b := range list {
		at[b.ID] = i
	}
	rows, err := s.query(ctx, "SELECT id, kind, owner_id, path, size, quality, added_at FROM media_files WHERE kind = 'album'")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var f File
		var added int64
		if err := rows.Scan(&f.ID, &f.Kind, &f.OwnerID, &f.Path, &f.Size, &f.Quality, &added); err != nil {
			return err
		}
		f.AddedAt = time.Unix(added, 0).UTC()
		if i, ok := at[f.OwnerID]; ok {
			list[i].File = &f
		}
	}
	return rows.Err()
}

// Album finds one album.
func (s *Store) Album(ctx context.Context, id int64) (Album, error) {
	b, err := scanAlbum(s.row(ctx, "SELECT "+albumCols+" FROM albums WHERE id = ?", id))
	if err != nil {
		return b, err
	}
	var f File
	var added int64
	err = s.row(ctx, "SELECT id, kind, owner_id, path, size, quality, added_at FROM media_files WHERE kind = 'album' AND owner_id = ?", id).
		Scan(&f.ID, &f.Kind, &f.OwnerID, &f.Path, &f.Size, &f.Quality, &added)
	if errors.Is(err, sql.ErrNoRows) {
		return b, nil
	}
	if err != nil {
		return b, err
	}
	f.AddedAt = time.Unix(added, 0).UTC()
	b.File = &f
	return b, nil
}

// WantedAlbums lists the albums wanted that have no copy in the library
// yet and are out by today (a "YYYY-MM-DD" date; an album out in a year or
// month only counts once that's begun). Albums with no date are left out.
func (s *Store) WantedAlbums(ctx context.Context, today string) ([]Album, error) {
	list, err := s.albumList(ctx, `SELECT `+albumCols+` FROM albums b WHERE b.monitored = 1 AND b.released <> '' AND b.released <= ?
		AND NOT EXISTS (SELECT 1 FROM media_files f WHERE f.kind = 'album' AND f.owner_id = b.id)
		ORDER BY b.id`, today)
	if err != nil {
		return nil, err
	}
	return list, nil
}

// UpsertAlbum adds an album, or brings its details up to date. An album
// already on the list keeps whether it's watched for; a new one takes
// b.Monitored. It reports whether the album is new.
func (s *Store) UpsertAlbum(ctx context.Context, b Album) (bool, error) {
	res, err := s.exec(ctx, "UPDATE albums SET title = ?, type = ?, secondary_types = ?, released = ? WHERE mbid = ? AND artist_id = ?",
		b.Title, b.Type, jsonList(b.SecondaryTypes), b.Released, strings.ToLower(b.MBID), b.ArtistID)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return false, nil
	}
	_, err = s.insert(ctx, "INSERT INTO albums (artist_id, mbid, title, type, secondary_types, released, monitored) VALUES (?, ?, ?, ?, ?, ?, ?)",
		b.ArtistID, strings.ToLower(b.MBID), b.Title, b.Type, jsonList(b.SecondaryTypes), b.Released, b2i(b.Monitored))
	if errors.Is(err, ErrExists) {
		// Credited to another artist on the list too (a collaboration);
		// it stays with the first.
		return false, nil
	}
	return err == nil, err
}

// SetAlbumMonitored says whether an album is watched for.
func (s *Store) SetAlbumMonitored(ctx context.Context, id int64, on bool) error {
	res, err := s.exec(ctx, "UPDATE albums SET monitored = ? WHERE id = ?", b2i(on), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
