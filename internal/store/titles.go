package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"sort"
	"strings"
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
	// SecondProfileID is the quality profile of a second copy to keep of
	// it (a 1080p one next to the 4K one, say), or 0 for none. SecondRoot
	// is the library folder that copy goes in, in a folder named like the
	// movie's; "" keeps it next to the first copy, in the movie's folder.
	// Second is that copy, once it's there.
	SecondProfileID int    `json:"secondProfileId,omitempty"`
	SecondRoot      string `json:"secondRoot,omitempty"`
	Second          *File  `json:"second,omitempty"`
	// Editions are other cuts of it kept next to its copy (see
	// AddEditionFile), when asked for with LabelEditions.
	Editions []File `json:"editions,omitempty"`
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
	// Edition is the cut a movie's copy is (Director's Cut, Extended…),
	// from its name or, with EditionByLength, from how long it runs;
	// filled in by Movie.LabelEditions.
	Edition         string `json:"edition,omitempty"`
	EditionFrom     string `json:"editionFrom,omitempty"`
	EditionByLength bool   `json:"editionByLength,omitempty"`
	// Subtitles are its subtitles in each language Settings → Subtitles
	// wants, when they're turned on; filled in by the API.
	Subtitles []Subtitle `json:"subtitles,omitempty"`
}

// LabelEditions fills in the edition of the movie's copy and of each of
// its other editions, and where it was told from (EditionFrom): its file
// name, else the name of the release it was downloaded from (release
// gives it, by path; "" when not known), else the title the file gives
// itself, else how long it runs against the others and the movie's
// runtime (see quality.LengthEdition), the only one that's a guess.
func (m *Movie) LabelEditions(release func(path string) string) {
	files := make([]*File, 0, 1+len(m.Editions))
	if m.File != nil {
		files = append(files, m.File)
	}
	for i := range m.Editions {
		files = append(files, &m.Editions[i])
	}
	length := func(f *File) float64 {
		if f.Media == nil || f.Media.Error != "" {
			return 0
		}
		return f.Media.Duration
	}
	for _, f := range files {
		f.Edition, f.EditionFrom, f.EditionByLength = "", "", false
		var inner string
		if f.Media != nil {
			inner = f.Media.Title
		}
		var rel string
		if release != nil {
			rel = release(f.Path)
		}
		for _, src := range []struct{ from, name string }{{EditionFromName, filepath.Base(f.Path)}, {EditionFromRelease, rel}, {EditionFromFile, inner}} {
			if e := quality.Edition(src.name); src.name != "" && e != "" {
				f.Edition, f.EditionFrom = e, src.from
				break
			}
		}
		if f.Edition != "" {
			continue
		}
		var others []float64
		for _, o := range files {
			if o != f {
				others = append(others, length(o))
			}
		}
		if e := quality.LengthEdition(length(f), others, m.Runtime); e != "" {
			f.Edition, f.EditionFrom, f.EditionByLength = e, EditionFromLength, true
		}
	}
}

// Where a copy's edition was told from.
const (
	EditionFromName    = "name"    // its file name
	EditionFromRelease = "release" // the release it was downloaded from
	EditionFromFile    = "file"    // the title inside the file
	EditionFromLength  = "length"  // how long it runs: a guess
)

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
	f.id, f.path, f.size, f.quality, f.added_at, f.media, f.guessed,
	m.second_profile_id, m.second_root, f2.id, f2.path, f2.size, f2.quality, f2.added_at, f2.media, f2.guessed`

const movieFrom = ` FROM movies m LEFT JOIN media_files f ON f.kind = 'movie' AND f.owner_id = m.id
	LEFT JOIN media_files f2 ON f2.kind = 'second' AND f2.owner_id = m.id`

// SecondKind is the kind of a movie's second copy among recorded files and
// the mode of a download for one.
const SecondKind = "second"

// SecondFolder is the folder a movie's second copy is kept in: the
// movie's own, or one named like it in the library folder chosen for
// second copies.
func (m Movie) SecondFolder() (root, folder string) {
	if m.SecondRoot == "" || filepath.Clean(m.SecondRoot) == filepath.Clean(m.RootFolder) {
		return m.RootFolder, m.Path
	}
	return m.SecondRoot, filepath.Join(m.SecondRoot, filepath.Base(m.Path))
}

func scanMovie(r interface{ Scan(...any) error }) (Movie, error) {
	var m Movie
	var cin, dig, phy sql.NullInt64
	var mon int
	var added, refreshed int64
	var fid, fsize, fadded sql.NullInt64
	var fpath sql.NullString
	var fq, fguessed sql.NullInt64
	var fmedia sql.NullString
	var sid, ssize, sadded, sq, sguessed sql.NullInt64
	var spath, smedia sql.NullString
	err := r.Scan(&m.ID, &m.TMDBID, &m.IMDBID, &m.Title, &m.SortTitle, &m.Year, &m.Overview, &m.Poster, &m.Backdrop, &m.Runtime, &m.Status,
		&cin, &dig, &phy, &m.Rating, &mon, &m.ProfileID, &m.RootFolder, &m.Path, &added, &refreshed, &m.AutoUpgrade,
		&fid, &fpath, &fsize, &fq, &fadded, &fmedia, &fguessed,
		&m.SecondProfileID, &m.SecondRoot, &sid, &spath, &ssize, &sq, &sadded, &smedia, &sguessed)
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
	if sid.Valid {
		m.Second = &File{ID: sid.Int64, Kind: SecondKind, OwnerID: m.ID, Path: spath.String, Size: ssize.Int64, Quality: int(sq.Int64), AddedAt: time.Unix(sadded.Int64, 0).UTC()}
		m.Second.checked(smedia, sguessed)
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

// UpdateMovie saves what an admin can change: monitoring, profile, folder,
// and the second copy to keep.
func (s *Store) UpdateMovie(ctx context.Context, m Movie) error {
	res, err := s.exec(ctx, "UPDATE movies SET monitored = ?, profile_id = ?, root_folder = ?, path = ?, auto_upgrade = ?, second_profile_id = ?, second_root = ? WHERE id = ?",
		b2i(m.Monitored), m.ProfileID, m.RootFolder, m.Path, m.AutoUpgrade, m.SecondProfileID, m.SecondRoot, m.ID)
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
	if _, err := s.exec(ctx, "DELETE FROM media_files WHERE kind IN ('movie', 'edition', 'second') AND owner_id = ?", id); err != nil {
		return err
	}
	if _, err := s.exec(ctx, "DELETE FROM title_wants WHERE kind = 'movie' AND owner_id = ?", id); err != nil {
		return err
	}
	if _, err := s.exec(ctx, "DELETE FROM cleanup_items WHERE kind = 'movie' AND title_id = ?", id); err != nil {
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
	// KeepAhead is the show's space saver: only this many episodes past
	// the latest one anyone has watched are fetched. 0 is off.
	KeepAhead int `json:"keepAhead"`
	// SecondProfileID is the quality profile of a second copy to keep of
	// each episode (a 1080p one next to the 4K one, say), or 0 for none.
	// SecondRoot is the library folder those go in, in a folder named
	// like the show's; "" keeps them next to the first copies.
	SecondProfileID int    `json:"secondProfileId,omitempty"`
	SecondRoot      string `json:"secondRoot,omitempty"`
	// Numbering is how releases of the show number its episodes: "" for
	// season and episode (S02E05), or NumberingAbsolute for straight
	// through from the first episode (anime: "Show - 137").
	Numbering string `json:"numbering"`
	// Audio is the sound wanted of a show that's released both dubbed
	// and subtitled: "" for either, AudioDubbed or AudioOriginal.
	Audio   string   `json:"audio"`
	Seasons []Season `json:"seasons"`
	// Counts over monitored, aired episodes, leaving out the ones space
	// saver is holding back that aren't on disk.
	EpisodeCount int `json:"episodeCount"`
	FileCount    int `json:"fileCount"`
	// Over every episode file, wanted or not.
	SizeOnDisk int64 `json:"sizeOnDisk"`
	// The latest episode to have aired, and the next one to air.
	LastAired  *time.Time `json:"lastAired,omitempty"`
	NextAiring *time.Time `json:"nextAiring,omitempty"`
}

const (
	NumberingAbsolute = "absolute"
	AudioDubbed       = "dub"
	AudioOriginal     = "sub"
	// numberingOrder starts a Numbering that names one of TMDB's other
	// episode orders (its episode groups), such as the DVD order.
	numberingOrder = "order:"
)

// OrderNumbering is the Numbering for one of TMDB's episode orders.
func OrderNumbering(id string) string { return numberingOrder + id }

// Order is the TMDB episode order a show's releases are numbered by,
// when its Numbering names one.
func (t Series) Order() (id string, ok bool) {
	id, ok = strings.CutPrefix(t.Numbering, numberingOrder)
	return id, ok && id != ""
}

// Place is where an episode sits in the episode order chosen for its
// show's releases.
type Place struct {
	Season int `json:"season"`
	Number int `json:"number"`
}

// PlacesTaken is the seasons and numbers a show's chosen episode order
// gives its episodes; empty when it follows none.
func PlacesTaken(eps []Episode) map[Place]bool {
	taken := map[Place]bool{}
	for _, e := range eps {
		if e.Place != nil {
			taken[*e.Place] = true
		}
	}
	return taken
}

// InOrder is a show's episodes numbered the way its releases are: an
// episode with a place in the chosen order takes that season and
// number. One without keeps its own unless a placed episode has taken
// them, and then it can't be told from a release's name (season -1, and
// a number below 0 of its own).
// Without a chosen order, eps come back as they are.
func InOrder(eps []Episode) []Episode { return InOrderOf(eps, PlacesTaken(eps)) }

// InOrderOf is InOrder for some of a show's episodes, given the places
// taken among all of them.
func InOrderOf(eps []Episode, taken map[Place]bool) []Episode {
	if len(taken) == 0 {
		return eps
	}
	out := slices.Clone(eps)
	for i, e := range out {
		switch {
		case e.Place != nil:
			out[i].Season, out[i].Number = e.Place.Season, e.Place.Number
		case taken[Place{e.Season, e.Number}]:
			out[i].Season, out[i].Number = -1, -i-1
		}
	}
	return out
}

// SetEpisodePlaces saves where a show's episodes sit in the order chosen
// for its releases, by episode id; its other episodes have no place.
func (s *Store) SetEpisodePlaces(ctx context.Context, seriesID int64, places map[int64]Place) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, s.q("UPDATE episodes SET place_season = NULL, place_number = NULL WHERE series_id = ? AND place_season IS NOT NULL"), seriesID); err != nil {
		return err
	}
	for id, p := range places {
		if _, err := tx.ExecContext(ctx, s.q("UPDATE episodes SET place_season = ?, place_number = ? WHERE id = ? AND series_id = ?"), p.Season, p.Number, id, seriesID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AbsoluteNumbers numbers a show's episodes straight through, the way
// anime releases do: the first episode of season 1 is 1, and each season
// carries on from the one before. Specials have no number. It gives each
// number's episode.
func AbsoluteNumbers(eps []Episode) map[int]Episode {
	in := make([]Episode, 0, len(eps))
	for _, e := range eps {
		if e.Season > 0 {
			in = append(in, e)
		}
	}
	sort.SliceStable(in, func(i, j int) bool {
		if in[i].Season != in[j].Season {
			return in[i].Season < in[j].Season
		}
		return in[i].Number < in[j].Number
	})
	out := make(map[int]Episode, len(in))
	for i, e := range in {
		out[i+1] = e
	}
	return out
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
	// Held is set by the show's space saver: the episode is too far past
	// what has been watched to fetch yet.
	Held bool  `json:"held,omitempty"`
	File *File `json:"file,omitempty"`
	// Second is its second copy, for a show that keeps one.
	Second *File `json:"second,omitempty"`
	// Place is where it sits in the episode order chosen for the show's
	// releases, when one is and it lists the episode.
	Place *Place `json:"place,omitempty"`
}

const seriesCols = `id, tmdb_id, tvdb_id, imdb_id, title, sort_title, year, overview, poster, backdrop, status, network, runtime, rating,
	monitored, profile_id, root_folder, path, added_at, refreshed_at, auto_upgrade, keep_ahead, second_profile_id, second_root, numbering, audio`

func scanSeries(r interface{ Scan(...any) error }) (Series, error) {
	var t Series
	var mon int
	var added, refreshed int64
	err := r.Scan(&t.ID, &t.TMDBID, &t.TVDBID, &t.IMDBID, &t.Title, &t.SortTitle, &t.Year, &t.Overview, &t.Poster, &t.Backdrop, &t.Status, &t.Network, &t.Runtime, &t.Rating,
		&mon, &t.ProfileID, &t.RootFolder, &t.Path, &added, &refreshed, &t.AutoUpgrade, &t.KeepAhead, &t.SecondProfileID, &t.SecondRoot, &t.Numbering, &t.Audio)
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
		WHERE e.series_id = ? AND e.monitored = 1 AND (e.held = 0 OR f.id IS NOT NULL) AND se.monitored = 1 AND e.air_date IS NOT NULL AND e.air_date <= ?`, t.ID, at).
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
		monitored, profile_id, root_folder, path, added_at, refreshed_at, keep_ahead) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.TMDBID, t.TVDBID, t.IMDBID, t.Title, SortTitle(t.Title), t.Year, t.Overview, t.Poster, t.Backdrop, t.Status, t.Network, t.Runtime, t.Rating,
		b2i(t.Monitored), t.ProfileID, t.RootFolder, t.Path, now(), now(), t.KeepAhead)
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
	res, err := s.exec(ctx, "UPDATE series SET monitored = ?, profile_id = ?, root_folder = ?, path = ?, auto_upgrade = ?, keep_ahead = ?, second_profile_id = ?, second_root = ?, numbering = ?, audio = ? WHERE id = ?",
		b2i(t.Monitored), t.ProfileID, t.RootFolder, t.Path, t.AutoUpgrade, t.KeepAhead, t.SecondProfileID, t.SecondRoot, t.Numbering, t.Audio, t.ID)
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
	if _, err := s.exec(ctx, "DELETE FROM media_files WHERE kind IN ('episode', 'episode2') AND owner_id IN (SELECT id FROM episodes WHERE series_id = ?)", id); err != nil {
		return err
	}
	if _, err := s.exec(ctx, "DELETE FROM episodes WHERE series_id = ?", id); err != nil {
		return err
	}
	if _, err := s.exec(ctx, "DELETE FROM cleanup_items WHERE kind = 'series' AND title_id = ?", id); err != nil {
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

const episodeCols = `e.id, e.series_id, e.season, e.number, e.title, e.overview, e.air_date, e.monitored, e.held, e.place_season, e.place_number,
	f.id, f.path, f.size, f.quality, f.added_at, f.media, f.guessed,
	f2.id, f2.path, f2.size, f2.quality, f2.added_at, f2.media, f2.guessed`

const episodeFrom = ` FROM episodes e LEFT JOIN media_files f ON f.kind = 'episode' AND f.owner_id = e.id
	LEFT JOIN media_files f2 ON f2.kind = 'episode2' AND f2.owner_id = e.id`

// SecondEpisodeKind is the kind of an episode's second copy among recorded
// files (see Series.SecondProfileID); it's owned by the episode.
const SecondEpisodeKind = "episode2"

// IsSecondKind says whether a recorded file's kind is a second copy: a
// movie's or an episode's.
func IsSecondKind(kind string) bool { return kind == SecondKind || kind == SecondEpisodeKind }

// SecondFolder is the folder a show's second copies are kept in: the
// show's own, or one named like it in the library folder chosen for them.
func (t Series) SecondFolder() (root, folder string) {
	if t.SecondRoot == "" || filepath.Clean(t.SecondRoot) == filepath.Clean(t.RootFolder) {
		return t.RootFolder, t.Path
	}
	return t.SecondRoot, filepath.Join(t.SecondRoot, filepath.Base(t.Path))
}

func scanEpisode(r interface{ Scan(...any) error }) (Episode, error) {
	var e Episode
	var air sql.NullInt64
	var mon, held int
	var fid, fsize, fq, fadded, fguessed sql.NullInt64
	var fpath, fmedia sql.NullString
	var sid, ssize, sq, sadded, sguessed sql.NullInt64
	var spath, smedia sql.NullString
	var ps, pn sql.NullInt64
	err := r.Scan(&e.ID, &e.SeriesID, &e.Season, &e.Number, &e.Title, &e.Overview, &air, &mon, &held, &ps, &pn, &fid, &fpath, &fsize, &fq, &fadded, &fmedia, &fguessed,
		&sid, &spath, &ssize, &sq, &sadded, &smedia, &sguessed)
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	if err != nil {
		return e, err
	}
	e.AirDate, e.Monitored, e.Held = unix(air), mon == 1, held == 1
	if ps.Valid && pn.Valid {
		e.Place = &Place{int(ps.Int64), int(pn.Int64)}
	}
	if fid.Valid {
		e.File = &File{ID: fid.Int64, Kind: "episode", OwnerID: e.ID, Path: fpath.String, Size: fsize.Int64, Quality: int(fq.Int64), AddedAt: time.Unix(fadded.Int64, 0).UTC()}
		e.File.checked(fmedia, fguessed)
	}
	if sid.Valid {
		e.Second = &File{ID: sid.Int64, Kind: SecondEpisodeKind, OwnerID: e.ID, Path: spath.String, Size: ssize.Int64, Quality: int(sq.Int64), AddedAt: time.Unix(sadded.Int64, 0).UTC()}
		e.Second.checked(smedia, sguessed)
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

// SetEpisodeHeld says whether a show's space saver is holding an episode
// back.
func (s *Store) SetEpisodeHeld(ctx context.Context, id int64, held bool) error {
	_, err := s.exec(ctx, "UPDATE episodes SET held = ? WHERE id = ?", b2i(held), id)
	return err
}

// ---- files ----

// AddFile records a file for a movie or an episode, replacing the one it
// had. A movie's second copy (kind SecondKind, owned by the movie) is
// recorded the same way, next to its first.
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

// DropNumberedSeasons clears a show's seasons numbered 1 to 1899 that hold
// no files: the ones TMDB's 1, 2, 3 numbering added next to the seasons
// TheTVDB, the indexers and the files number by year. Episodes with a file
// stay, and so does their season.
func (s *Store) DropNumberedSeasons(ctx context.Context, seriesID int64) (int, error) {
	const bare = `SELECT e.id FROM episodes e WHERE e.series_id = ? AND e.season > 0 AND e.season < 1900
		AND NOT EXISTS (SELECT 1 FROM media_files f WHERE f.kind = 'episode' AND f.owner_id = e.id)`
	if _, err := s.exec(ctx, "DELETE FROM title_wants WHERE kind = 'episode' AND owner_id IN ("+bare+")", seriesID); err != nil {
		return 0, err
	}
	if _, err := s.exec(ctx, "DELETE FROM searched WHERE kind = 'episode' AND id IN ("+bare+")", seriesID); err != nil {
		return 0, err
	}
	if _, err := s.exec(ctx, "DELETE FROM episodes WHERE id IN (SELECT id FROM ("+bare+") gone)", seriesID); err != nil {
		return 0, err
	}
	res, err := s.exec(ctx, `DELETE FROM seasons WHERE series_id = ? AND number > 0 AND number < 1900
		AND NOT EXISTS (SELECT 1 FROM episodes e WHERE e.series_id = seasons.series_id AND e.season = seasons.number)`, seriesID)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}
