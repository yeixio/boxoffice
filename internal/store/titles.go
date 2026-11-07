package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/yeixio/boxoffice/internal/quality"
)

// Movie is a movie on the wanted list.
type Movie struct {
	ID              int64      `json:"id"`
	TMDBID          int64      `json:"tmdbId"`
	IMDBID          string     `json:"imdbId,omitempty"`
	Title           string     `json:"title"`
	SortTitle       string     `json:"sortTitle"`
	Year            int        `json:"year,omitempty"`
	Overview        string     `json:"overview,omitempty"`
	Poster          string     `json:"poster,omitempty"`
	Backdrop        string     `json:"backdrop,omitempty"`
	Runtime         int        `json:"runtime,omitempty"` // minutes
	Status          string     `json:"status,omitempty"`  // TMDB's: Released, In Production…
	InCinemas       *time.Time `json:"inCinemas,omitempty"`
	DigitalRelease  *time.Time `json:"digitalRelease,omitempty"`
	PhysicalRelease *time.Time `json:"physicalRelease,omitempty"`
	Rating          float64    `json:"rating,omitempty"` // out of 10
	Monitored       bool       `json:"monitored"`
	ProfileID       int        `json:"profileId"`
	RootFolder      string     `json:"rootFolder"`
	Path            string     `json:"path"`
	AddedAt         time.Time  `json:"addedAt"`
	RefreshedAt     time.Time  `json:"refreshedAt"`
	// AutoUpgrade is "" to follow Settings, or "on" or "off" for this
	// movie: whether Box Office looks for upgrades of it by itself.
	AutoUpgrade string `json:"autoUpgrade"`
	File        *File  `json:"file,omitempty"`
}

// File is a copy on disk of a movie or an episode.
type File struct {
	ID      int64     `json:"id"`
	Kind    string    `json:"kind"` // movie or episode
	OwnerID int64     `json:"ownerId"`
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	Quality int       `json:"quality"`
	AddedAt time.Time `json:"addedAt"`
	// Media is what the quality check found inside the file, once it has
	// looked; Guessed the quality it had before the check corrected it.
	Media   *quality.Media `json:"media,omitempty"`
	Guessed *int           `json:"guessed,omitempty"`
}

// checked fills in what the quality check found, from the media and
// guessed columns.
func (f *File) checked(media sql.NullString, guessed sql.NullInt64) {
	if media.Valid && media.String != "" {
		var m quality.Media
		if json.Unmarshal([]byte(media.String), &m) == nil {
			f.Media = &m
		}
	}
	if guessed.Valid && guessed.Int64 >= 0 {
		g := int(guessed.Int64)
		f.Guessed = &g
	}
}

const movieCols = `m.id, m.tmdb_id, m.imdb_id, m.title, m.sort_title, m.year, m.overview, m.poster, m.backdrop, m.runtime, m.status,
	m.in_cinemas, m.digital_release, m.physical_release, m.rating, m.monitored, m.profile_id, m.root_folder, m.path, m.added_at, m.refreshed_at, m.auto_upgrade,
	f.id, f.path, f.size, f.quality, f.added_at, f.media, f.guessed`

const movieFrom = ` FROM movies m LEFT JOIN media_files f ON f.kind = 'movie' AND f.owner_id = m.id`

func scanMovie(r interface{ Scan(...any) error }) (Movie, error) {
	var m Movie
	var cin, dig, phy sql.NullInt64
	var mon int
	var added, refreshed int64
	var fid, fsize, fadded sql.NullInt64
	var fpath sql.NullString
	var fq, fguessed sql.NullInt64
	var fmedia sql.NullString
	err := r.Scan(&m.ID, &m.TMDBID, &m.IMDBID, &m.Title, &m.SortTitle, &m.Year, &m.Overview, &m.Poster, &m.Backdrop, &m.Runtime, &m.Status,
		&cin, &dig, &phy, &m.Rating, &mon, &m.ProfileID, &m.RootFolder, &m.Path, &added, &refreshed, &m.AutoUpgrade,
		&fid, &fpath, &fsize, &fq, &fadded, &fmedia, &fguessed)
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	if err != nil {
		return m, err
	}
	m.InCinemas, m.DigitalRelease, m.PhysicalRelease = unix(cin), unix(dig), unix(phy)
	m.Monitored = mon == 1
	m.AddedAt, m.RefreshedAt = time.Unix(added, 0).UTC(), time.Unix(refreshed, 0).UTC()
	if fid.Valid {
		m.File = &File{ID: fid.Int64, Kind: "movie", OwnerID: m.ID, Path: fpath.String, Size: fsize.Int64, Quality: int(fq.Int64), AddedAt: time.Unix(fadded.Int64, 0).UTC()}
		m.File.checked(fmedia, fguessed)
	}
	return m, nil
}

func (s *Store) movies(ctx context.Context, where string, args ...any) ([]Movie, error) {
	rows, err := s.query(ctx, "SELECT "+movieCols+movieFrom+" "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Movie{}
	for rows.Next() {
		m, err := scanMovie(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Movies lists every movie, by title.
func (s *Store) Movies(ctx context.Context) ([]Movie, error) {
	return s.movies(ctx, "ORDER BY m.sort_title, m.year")
}

func (s *Store) Movie(ctx context.Context, id int64) (Movie, error) {
	return scanMovie(s.row(ctx, "SELECT "+movieCols+movieFrom+" WHERE m.id = ?", id))
}

func (s *Store) MovieByTMDB(ctx context.Context, tmdbID int64) (Movie, error) {
	return scanMovie(s.row(ctx, "SELECT "+movieCols+movieFrom+" WHERE m.tmdb_id = ?", tmdbID))
}

// MoviesReleasing lists monitored movies with a release of any kind in [from, to).
func (s *Store) MoviesReleasing(ctx context.Context, from, to time.Time) ([]Movie, error) {
	f, t := from.Unix(), to.Unix()
	return s.movies(ctx, `WHERE m.monitored = 1 AND ((m.in_cinemas >= ? AND m.in_cinemas < ?) OR (m.digital_release >= ? AND m.digital_release < ?)
		OR (m.physical_release >= ? AND m.physical_release < ?)) ORDER BY m.sort_title`, f, t, f, t, f, t)
}

// AddMovie puts a movie on the wanted list.
func (s *Store) AddMovie(ctx context.Context, m Movie) (Movie, error) {
	id, err := s.insert(ctx, `INSERT INTO movies (tmdb_id, imdb_id, title, sort_title, year, overview, poster, backdrop, runtime, status,
		in_cinemas, digital_release, physical_release, rating, monitored, profile_id, root_folder, path, added_at, refreshed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.TMDBID, m.IMDBID, m.Title, SortTitle(m.Title), m.Year, m.Overview, m.Poster, m.Backdrop, m.Runtime, m.Status,
		nullTime(m.InCinemas), nullTime(m.DigitalRelease), nullTime(m.PhysicalRelease), m.Rating, b2i(m.Monitored), m.ProfileID, m.RootFolder, m.Path, now(), now())
	if err != nil {
		return m, err
	}
	return s.Movie(ctx, id)
}

// UpdateMovie saves what an admin can change: monitoring, profile, folder.
func (s *Store) UpdateMovie(ctx context.Context, m Movie) error {
	res, err := s.exec(ctx, "UPDATE movies SET monitored = ?, profile_id = ?, root_folder = ?, path = ?, auto_upgrade = ? WHERE id = ?",
		b2i(m.Monitored), m.ProfileID, m.RootFolder, m.Path, m.AutoUpgrade, m.ID)
	if err != nil {
		return err
	}
	return affected(res)
}

// RefreshMovie saves new details from TMDB.
func (s *Store) RefreshMovie(ctx context.Context, m Movie) error {
	res, err := s.exec(ctx, `UPDATE movies SET imdb_id = ?, title = ?, sort_title = ?, year = ?, overview = ?, poster = ?, backdrop = ?, runtime = ?, status = ?,
		in_cinemas = ?, digital_release = ?, physical_release = ?, rating = ?, refreshed_at = ? WHERE id = ?`,
		m.IMDBID, m.Title, SortTitle(m.Title), m.Year, m.Overview, m.Poster, m.Backdrop, m.Runtime, m.Status,
		nullTime(m.InCinemas), nullTime(m.DigitalRelease), nullTime(m.PhysicalRelease), m.Rating, now(), m.ID)
	if err != nil {
		return err
	}
	return affected(res)
}

// FillRating sets a title's rating when it hasn't one yet, for titles
// moved in before Box Office kept ratings.
func (s *Store) FillRating(ctx context.Context, kind string, id int64, rating float64) error {
	table := "movies"
	if kind == "series" {
		table = "series"
	}
	_, err := s.exec(ctx, "UPDATE "+table+" SET rating = ? WHERE id = ? AND rating = 0", rating, id)
	return err
}

func (s *Store) DeleteMovie(ctx context.Context, id int64) error {
	if _, err := s.exec(ctx, "DELETE FROM media_files WHERE kind IN ('movie', 'edition') AND owner_id = ?", id); err != nil {
		return err
	}
	if _, err := s.exec(ctx, "DELETE FROM title_wants WHERE kind = 'movie' AND owner_id = ?", id); err != nil {
		return err
	}
	res, err := s.exec(ctx, "DELETE FROM movies WHERE id = ?", id)
	if err != nil {
		return err
	}
	return affected(res)
}

// ---- series ----

// Series is a TV show on the wanted list.
type Series struct {
	ID          int64     `json:"id"`
	TMDBID      int64     `json:"tmdbId"`
	TVDBID      int64     `json:"tvdbId,omitempty"`
	IMDBID      string    `json:"imdbId,omitempty"`
	Title       string    `json:"title"`
	SortTitle   string    `json:"sortTitle"`
	Year        int       `json:"year,omitempty"`
	Overview    string    `json:"overview,omitempty"`
	Poster      string    `json:"poster,omitempty"`
	Backdrop    string    `json:"backdrop,omitempty"`
	Status      string    `json:"status,omitempty"` // TMDB's: Returning Series, Ended…
	Network     string    `json:"network,omitempty"`
	Runtime     int       `json:"runtime,omitempty"`
	Rating      float64   `json:"rating,omitempty"` // out of 10
	Monitored   bool      `json:"monitored"`
	ProfileID   int       `json:"profileId"`
	RootFolder  string    `json:"rootFolder"`
	Path        string    `json:"path"`
	AddedAt     time.Time `json:"addedAt"`
	RefreshedAt time.Time `json:"refreshedAt"`
	AutoUpgrade string    `json:"autoUpgrade"` // as Movie's
	Seasons     []Season  `json:"seasons"`
	// Counts over monitored, aired episodes.
	EpisodeCount int `json:"episodeCount"`
	FileCount    int `json:"fileCount"`
	// Over every episode file, wanted or not.
	SizeOnDisk int64 `json:"sizeOnDisk"`
	// The latest episode to have aired, and the next one to air.
	LastAired  *time.Time `json:"lastAired,omitempty"`
	NextAiring *time.Time `json:"nextAiring,omitempty"`
}

// Season is one season of a show, and whether its episodes are wanted.
type Season struct {
	Number    int  `json:"number"`
	Monitored bool `json:"monitored"`
}

// Episode is one episode of a show.
type Episode struct {
	ID        int64      `json:"id"`
	SeriesID  int64      `json:"seriesId"`
	Season    int        `json:"season"`
	Number    int        `json:"number"`
	Title     string     `json:"title,omitempty"`
	Overview  string     `json:"overview,omitempty"`
	AirDate   *time.Time `json:"airDate,omitempty"`
	Monitored bool       `json:"monitored"`
	File      *File      `json:"file,omitempty"`
}

const seriesCols = `id, tmdb_id, tvdb_id, imdb_id, title, sort_title, year, overview, poster, backdrop, status, network, runtime, rating,
	monitored, profile_id, root_folder, path, added_at, refreshed_at, auto_upgrade`

func scanSeries(r interface{ Scan(...any) error }) (Series, error) {
	var t Series
	var mon int
	var added, refreshed int64
	err := r.Scan(&t.ID, &t.TMDBID, &t.TVDBID, &t.IMDBID, &t.Title, &t.SortTitle, &t.Year, &t.Overview, &t.Poster, &t.Backdrop, &t.Status, &t.Network, &t.Runtime, &t.Rating,
		&mon, &t.ProfileID, &t.RootFolder, &t.Path, &added, &refreshed, &t.AutoUpgrade)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	t.Monitored = mon == 1
	t.AddedAt, t.RefreshedAt = time.Unix(added, 0).UTC(), time.Unix(refreshed, 0).UTC()
	return t, err
}

func (s *Store) seriesList(ctx context.Context, where string, args ...any) ([]Series, error) {
	rows, err := s.query(ctx, "SELECT "+seriesCols+" FROM series "+where, args...)
	if err != nil {
		return nil, err
	}
	out := []Series{}
	for rows.Next() {
		t, err := scanSeries(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.fillSeries(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) fillSeries(ctx context.Context, t *Series) error {
	rows, err := s.query(ctx, "SELECT number, monitored FROM seasons WHERE series_id = ? ORDER BY number", t.ID)
	if err != nil {
		return err
	}
	t.Seasons = []Season{}
	for rows.Next() {
		var se Season
		var mon int
		if err := rows.Scan(&se.Number, &mon); err != nil {
			rows.Close()
			return err
		}
		se.Monitored = mon == 1
		t.Seasons = append(t.Seasons, se)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	at := now()
	if err := s.row(ctx, `SELECT COUNT(*), COUNT(f.id) FROM episodes e
		JOIN seasons se ON se.series_id = e.series_id AND se.number = e.season
		LEFT JOIN media_files f ON f.kind = 'episode' AND f.owner_id = e.id
		WHERE e.series_id = ? AND e.monitored = 1 AND se.monitored = 1 AND e.air_date IS NOT NULL AND e.air_date <= ?`, t.ID, at).
		Scan(&t.EpisodeCount, &t.FileCount); err != nil {
		return err
	}
	// Specials (season 0) don't count towards when a show last or next airs.
	var size, last, next sql.NullInt64
	if err := s.row(ctx, `SELECT
		(SELECT CAST(SUM(f.size) AS BIGINT) FROM media_files f JOIN episodes e ON f.kind = 'episode' AND f.owner_id = e.id WHERE e.series_id = ?),
		(SELECT MAX(air_date) FROM episodes WHERE series_id = ? AND season > 0 AND air_date <= ?),
		(SELECT MIN(air_date) FROM episodes WHERE series_id = ? AND season > 0 AND air_date > ?)`,
		t.ID, t.ID, at, t.ID, at).Scan(&size, &last, &next); err != nil {
		return err
	}
	t.SizeOnDisk, t.LastAired, t.NextAiring = size.Int64, unix(last), unix(next)
	return nil
}

func (s *Store) SeriesAll(ctx context.Context) ([]Series, error) {
	return s.seriesList(ctx, "ORDER BY sort_title, year")
}

func (s *Store) Series(ctx context.Context, id int64) (Series, error) {
	t, err := scanSeries(s.row(ctx, "SELECT "+seriesCols+" FROM series WHERE id = ?", id))
	if err != nil {
		return t, err
	}
	return t, s.fillSeries(ctx, &t)
}

func (s *Store) SeriesByTMDB(ctx context.Context, tmdbID int64) (Series, error) {
	t, err := scanSeries(s.row(ctx, "SELECT "+seriesCols+" FROM series WHERE tmdb_id = ?", tmdbID))
	if err != nil {
		return t, err
	}
	return t, s.fillSeries(ctx, &t)
}

func (s *Store) SeriesByTVDB(ctx context.Context, tvdbID int64) (Series, error) {
	if tvdbID == 0 {
		return Series{}, ErrNotFound
	}
	t, err := scanSeries(s.row(ctx, "SELECT "+seriesCols+" FROM series WHERE tvdb_id = ?", tvdbID))
	if err != nil {
		return t, err
	}
	return t, s.fillSeries(ctx, &t)
}

// AddSeries puts a show on the wanted list, with its seasons.
func (s *Store) AddSeries(ctx context.Context, t Series) (Series, error) {
	id, err := s.insert(ctx, `INSERT INTO series (tmdb_id, tvdb_id, imdb_id, title, sort_title, year, overview, poster, backdrop, status, network, runtime, rating,
		monitored, profile_id, root_folder, path, added_at, refreshed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.TMDBID, t.TVDBID, t.IMDBID, t.Title, SortTitle(t.Title), t.Year, t.Overview, t.Poster, t.Backdrop, t.Status, t.Network, t.Runtime, t.Rating,
		b2i(t.Monitored), t.ProfileID, t.RootFolder, t.Path, now(), now())
	if err != nil {
		return t, err
	}
	for _, se := range t.Seasons {
		if err := s.SetSeason(ctx, id, se); err != nil {
			return t, err
		}
	}
	return s.Series(ctx, id)
}

// SetSeason adds a season, or changes whether it's monitored.
func (s *Store) SetSeason(ctx context.Context, seriesID int64, se Season) error {
	_, err := s.exec(ctx, `INSERT INTO seasons (series_id, number, monitored) VALUES (?, ?, ?)
		ON CONFLICT (series_id, number) DO UPDATE SET monitored = excluded.monitored`, seriesID, se.Number, b2i(se.Monitored))
	return err
}

// EnsureSeason adds a season that isn't there yet, leaving one that is alone.
func (s *Store) EnsureSeason(ctx context.Context, seriesID int64, se Season) error {
	_, err := s.exec(ctx, `INSERT INTO seasons (series_id, number, monitored) VALUES (?, ?, ?) ON CONFLICT (series_id, number) DO NOTHING`,
		seriesID, se.Number, b2i(se.Monitored))
	return err
}

func (s *Store) UpdateSeries(ctx context.Context, t Series) error {
	res, err := s.exec(ctx, "UPDATE series SET monitored = ?, profile_id = ?, root_folder = ?, path = ?, auto_upgrade = ? WHERE id = ?",
		b2i(t.Monitored), t.ProfileID, t.RootFolder, t.Path, t.AutoUpgrade, t.ID)
	if err != nil {
		return err
	}
	return affected(res)
}

func (s *Store) RefreshSeries(ctx context.Context, t Series) error {
	res, err := s.exec(ctx, `UPDATE series SET tvdb_id = ?, imdb_id = ?, title = ?, sort_title = ?, year = ?, overview = ?, poster = ?, backdrop = ?,
		status = ?, network = ?, runtime = ?, rating = ?, refreshed_at = ? WHERE id = ?`,
		t.TVDBID, t.IMDBID, t.Title, SortTitle(t.Title), t.Year, t.Overview, t.Poster, t.Backdrop, t.Status, t.Network, t.Runtime, t.Rating, now(), t.ID)
	if err != nil {
		return err
	}
	return affected(res)
}

func (s *Store) DeleteSeries(ctx context.Context, id int64) error {
	if _, err := s.exec(ctx, "DELETE FROM media_files WHERE kind = 'episode' AND owner_id IN (SELECT id FROM episodes WHERE series_id = ?)", id); err != nil {
		return err
	}
	if _, err := s.exec(ctx, "DELETE FROM episodes WHERE series_id = ?", id); err != nil {
		return err
	}
	if _, err := s.exec(ctx, "DELETE FROM seasons WHERE series_id = ?", id); err != nil {
		return err
	}
	res, err := s.exec(ctx, "DELETE FROM series WHERE id = ?", id)
	if err != nil {
		return err
	}
	return affected(res)
}

// ---- episodes ----

const episodeCols = `e.id, e.series_id, e.season, e.number, e.title, e.overview, e.air_date, e.monitored,
	f.id, f.path, f.size, f.quality, f.added_at, f.media, f.guessed`

const episodeFrom = ` FROM episodes e LEFT JOIN media_files f ON f.kind = 'episode' AND f.owner_id = e.id`

func scanEpisode(r interface{ Scan(...any) error }) (Episode, error) {
	var e Episode
	var air sql.NullInt64
	var mon int
	var fid, fsize, fq, fadded, fguessed sql.NullInt64
	var fpath, fmedia sql.NullString
	err := r.Scan(&e.ID, &e.SeriesID, &e.Season, &e.Number, &e.Title, &e.Overview, &air, &mon, &fid, &fpath, &fsize, &fq, &fadded, &fmedia, &fguessed)
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	if err != nil {
		return e, err
	}
	e.AirDate, e.Monitored = unix(air), mon == 1
	if fid.Valid {
		e.File = &File{ID: fid.Int64, Kind: "episode", OwnerID: e.ID, Path: fpath.String, Size: fsize.Int64, Quality: int(fq.Int64), AddedAt: time.Unix(fadded.Int64, 0).UTC()}
		e.File.checked(fmedia, fguessed)
	}
	return e, nil
}

func (s *Store) episodes(ctx context.Context, where string, args ...any) ([]Episode, error) {
	rows, err := s.query(ctx, "SELECT "+episodeCols+episodeFrom+" "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Episode{}
	for rows.Next() {
		e, err := scanEpisode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Episodes lists a show's episodes in order.
func (s *Store) Episodes(ctx context.Context, seriesID int64) ([]Episode, error) {
	return s.episodes(ctx, "WHERE e.series_id = ? ORDER BY e.season, e.number", seriesID)
}

func (s *Store) Episode(ctx context.Context, id int64) (Episode, error) {
	return scanEpisode(s.row(ctx, "SELECT "+episodeCols+episodeFrom+" WHERE e.id = ?", id))
}

// EpisodesAiring lists monitored episodes, in monitored seasons of
// monitored shows, that air in [from, to).
func (s *Store) EpisodesAiring(ctx context.Context, from, to time.Time) ([]Episode, error) {
	return s.episodes(ctx, `JOIN series t ON t.id = e.series_id JOIN seasons se ON se.series_id = e.series_id AND se.number = e.season
		WHERE t.monitored = 1 AND se.monitored = 1 AND e.monitored = 1 AND e.air_date >= ? AND e.air_date < ? ORDER BY e.air_date, e.season, e.number`,
		from.Unix(), to.Unix())
}

// UpsertEpisode adds an episode or refreshes its details, keeping whether
// it's monitored.
func (s *Store) UpsertEpisode(ctx context.Context, e Episode) error {
	_, err := s.exec(ctx, `INSERT INTO episodes (series_id, season, number, title, overview, air_date, monitored) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (series_id, season, number) DO UPDATE SET title = excluded.title, overview = excluded.overview, air_date = excluded.air_date`,
		e.SeriesID, e.Season, e.Number, e.Title, e.Overview, nullTime(e.AirDate), b2i(e.Monitored))
	return err
}

func (s *Store) SetEpisodeMonitored(ctx context.Context, id int64, monitored bool) error {
	res, err := s.exec(ctx, "UPDATE episodes SET monitored = ? WHERE id = ?", b2i(monitored), id)
	if err != nil {
		return err
	}
	return affected(res)
}

// ---- files ----

// AddFile records a file for a movie or an episode, replacing the one it
// had: Box Office keeps one copy per title for now.
func (s *Store) AddFile(ctx context.Context, f File) (File, error) {
	if _, err := s.exec(ctx, "DELETE FROM media_files WHERE kind = ? AND owner_id = ?", f.Kind, f.OwnerID); err != nil {
		return f, err
	}
	id, err := s.insert(ctx, "INSERT INTO media_files (kind, owner_id, path, size, quality, added_at) VALUES (?, ?, ?, ?, ?, ?)",
		f.Kind, f.OwnerID, f.Path, f.Size, f.Quality, now())
	f.ID = id
	return f, err
}

func (s *Store) FileByID(ctx context.Context, id int64) (File, error) {
	var f File
	var added int64
	var media sql.NullString
	var guessed sql.NullInt64
	err := s.row(ctx, "SELECT id, kind, owner_id, path, size, quality, added_at, media, guessed FROM media_files WHERE id = ?", id).
		Scan(&f.ID, &f.Kind, &f.OwnerID, &f.Path, &f.Size, &f.Quality, &added, &media, &guessed)
	if errors.Is(err, sql.ErrNoRows) {
		return f, ErrNotFound
	}
	f.checked(media, guessed)
	f.AddedAt = time.Unix(added, 0).UTC()
	return f, err
}

func (s *Store) DeleteFile(ctx context.Context, id int64) error {
	res, err := s.exec(ctx, "DELETE FROM media_files WHERE id = ?", id)
	if err != nil {
		return err
	}
	return affected(res)
}

// SampleFilePaths is up to n media files' paths, picked at random, for
// checking that files are where Box Office thinks they are.
func (s *Store) SampleFilePaths(ctx context.Context, n int) ([]string, error) {
	rows, err := s.query(ctx, "SELECT path FROM media_files ORDER BY RANDOM() LIMIT ?", n)
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

// SetRefreshed records when a movie's or show's details were last
// refreshed, for one whose refresh is put off.
func (s *Store) SetRefreshed(ctx context.Context, kind string, id int64, at time.Time) error {
	table := "movies"
	if kind == "series" {
		table = "series"
	}
	_, err := s.exec(ctx, "UPDATE "+table+" SET refreshed_at = ? WHERE id = ?", at.Unix(), id)
	return err
}
