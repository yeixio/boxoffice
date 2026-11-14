package store

import (
	"context"
	"strings"
	"time"
)

// Subtitle states (the subtitles table). The first three mean the file
// has subtitles in that language; missing ones are looked for again
// later, and waiting ones once a provider's day is over.
const (
	SubEmbedded = "embedded" // inside the video file
	SubOnDisk   = "on_disk"  // a subtitle file beside it that Box Office didn't fetch
	SubFetched  = "fetched"  // downloaded by Box Office
	SubMissing  = "missing"  // looked for, none found
	SubWaiting  = "waiting"  // found, but couldn't be fetched for now
)

// SubtitleHas says whether a state means the file has subtitles in that
// language.
func SubtitleHas(state string) bool {
	return state == SubEmbedded || state == SubOnDisk || state == SubFetched
}

// Subtitle is what's known of one file's subtitles in one language.
type Subtitle struct {
	FileID      int64     `json:"-"`
	Language    string    `json:"language"`
	State       string    `json:"state"`
	Provider    string    `json:"provider,omitempty"`
	Path        string    `json:"path,omitempty"`
	Release     string    `json:"release,omitempty"`
	MatchesFile bool      `json:"matchesFile,omitempty"`
	SDH         bool      `json:"sdh,omitempty"`
	Tries       int       `json:"tries,omitempty"`
	Note        string    `json:"note,omitempty"`
	CheckedAt   time.Time `json:"checkedAt"`
	NextAt      time.Time `json:"nextAt,omitzero"`
}

// SubtitleFile is a movie or episode file that subtitles can be looked
// for, with what finds them.
type SubtitleFile struct {
	ID      int64
	Kind    string // movie, edition or episode
	OwnerID int64  // the movie or episode
	Path    string
	Size    int64
	AddedAt time.Time
	// The movie's, or the episode's show's.
	TitleID int64
	TMDBID  int64
	IMDBID  string
	Title   string
	Year    int
	Season  int
	Episode int
	// Locked is the Do not touch profile: nothing is added beside it.
	Locked bool
}

// SubtitleFiles is every movie and episode file, oldest first.
func (s *Store) SubtitleFiles(ctx context.Context) ([]SubtitleFile, error) {
	return s.subtitleFiles(ctx, "")
}

// SubtitleFilesOf is the files of one movie (kind movie) or show (kind
// series), or of the episodes listed.
func (s *Store) SubtitleFilesOf(ctx context.Context, kind string, id int64, episodes ...int64) ([]SubtitleFile, error) {
	all, err := s.subtitleFiles(ctx, "")
	if err != nil {
		return nil, err
	}
	var out []SubtitleFile
	for _, f := range all {
		switch {
		case kind == "movie" && f.Kind != "episode" && f.TitleID == id,
			kind == "series" && f.Kind == "episode" && f.TitleID == id && (len(episodes) == 0 || containsID(episodes, f.OwnerID)):
			out = append(out, f)
		}
	}
	return out, nil
}

func containsID(list []int64, id int64) bool {
	for _, v := range list {
		if v == id {
			return true
		}
	}
	return false
}

func (s *Store) subtitleFiles(ctx context.Context, where string) ([]SubtitleFile, error) {
	rows, err := s.query(ctx, `SELECT f.id, f.kind, f.owner_id, f.path, f.size, f.added_at,
		COALESCE(m.id, t.id, 0), COALESCE(m.tmdb_id, t.tmdb_id, 0), COALESCE(m.imdb_id, t.imdb_id, ''), COALESCE(m.title, t.title, ''),
		COALESCE(m.year, t.year, 0), COALESCE(e.season, 0), COALESCE(e.number, 0), COALESCE(p.locked, 0)
		FROM media_files f
		LEFT JOIN movies m ON f.kind IN ('movie', 'edition') AND m.id = f.owner_id
		LEFT JOIN episodes e ON f.kind = 'episode' AND e.id = f.owner_id
		LEFT JOIN series t ON t.id = e.series_id
		LEFT JOIN quality_profiles p ON p.id = COALESCE(m.profile_id, t.profile_id)
		WHERE f.kind IN `+checkKinds+where+` ORDER BY f.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SubtitleFile
	for rows.Next() {
		var f SubtitleFile
		var added int64
		var locked int
		if err := rows.Scan(&f.ID, &f.Kind, &f.OwnerID, &f.Path, &f.Size, &added, &f.TitleID, &f.TMDBID, &f.IMDBID, &f.Title,
			&f.Year, &f.Season, &f.Episode, &locked); err != nil {
			return nil, err
		}
		if f.TitleID == 0 {
			continue // its title is gone
		}
		f.AddedAt, f.Locked = time.Unix(added, 0).UTC(), locked == 1
		out = append(out, f)
	}
	return out, rows.Err()
}

// Subtitles is what's known of every file's subtitles, by file.
func (s *Store) Subtitles(ctx context.Context) (map[int64][]Subtitle, error) {
	return s.subtitles(ctx, "")
}

// SubtitlesOf is what's known of some files' subtitles, by file.
func (s *Store) SubtitlesOf(ctx context.Context, fileIDs ...int64) (map[int64][]Subtitle, error) {
	if len(fileIDs) == 0 {
		return map[int64][]Subtitle{}, nil
	}
	if len(fileIDs) > 500 {
		return s.subtitles(ctx, "") // a long show: one read is quicker
	}
	ids := make([]string, len(fileIDs))
	args := make([]any, len(fileIDs))
	for i, id := range fileIDs {
		ids[i], args[i] = "?", id
	}
	return s.subtitles(ctx, " WHERE file_id IN ("+strings.Join(ids, ", ")+")", args...)
}

func (s *Store) subtitles(ctx context.Context, where string, args ...any) (map[int64][]Subtitle, error) {
	rows, err := s.query(ctx, `SELECT file_id, language, state, provider, path, release, matches_file, sdh, tries, note, checked_at, next_at
		FROM subtitles`+where+` ORDER BY file_id, language`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]Subtitle{}
	for rows.Next() {
		var x Subtitle
		var match, sdh int
		var checked, next int64
		if err := rows.Scan(&x.FileID, &x.Language, &x.State, &x.Provider, &x.Path, &x.Release, &match, &sdh, &x.Tries, &x.Note, &checked, &next); err != nil {
			return nil, err
		}
		x.MatchesFile, x.SDH = match == 1, sdh == 1
		x.CheckedAt = time.Unix(checked, 0).UTC()
		if next > 0 {
			x.NextAt = time.Unix(next, 0).UTC()
		}
		out[x.FileID] = append(out[x.FileID], x)
	}
	return out, rows.Err()
}

// SaveSubtitle notes what's known of a file's subtitles in a language.
func (s *Store) SaveSubtitle(ctx context.Context, x Subtitle) error {
	checked := x.CheckedAt
	if checked.IsZero() {
		checked = time.Now()
	}
	var next int64
	if !x.NextAt.IsZero() {
		next = x.NextAt.Unix()
	}
	_, err := s.exec(ctx, `INSERT INTO subtitles (file_id, language, state, provider, path, release, matches_file, sdh, tries, note, checked_at, next_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (file_id, language) DO UPDATE SET state = excluded.state, provider = excluded.provider, path = excluded.path,
			release = excluded.release, matches_file = excluded.matches_file, sdh = excluded.sdh, tries = excluded.tries,
			note = excluded.note, checked_at = excluded.checked_at, next_at = excluded.next_at`,
		x.FileID, x.Language, x.State, x.Provider, x.Path, x.Release, b2i(x.MatchesFile), b2i(x.SDH), x.Tries, x.Note, checked.Unix(), next)
	return err
}

// ForgetSubtitle drops what's known of a file's subtitles in a language,
// so it's looked at afresh.
func (s *Store) ForgetSubtitle(ctx context.Context, fileID int64, language string) error {
	_, err := s.exec(ctx, "DELETE FROM subtitles WHERE file_id = ? AND language = ?", fileID, language)
	return err
}

// PruneSubtitles forgets the subtitles of files that are gone from the
// library.
func (s *Store) PruneSubtitles(ctx context.Context) error {
	_, err := s.exec(ctx, "DELETE FROM subtitles WHERE file_id NOT IN (SELECT id FROM media_files)")
	return err
}

// RetrySubtitles has missing and waiting subtitles looked for again now:
// those of the files listed, or every file's when there are none.
func (s *Store) RetrySubtitles(ctx context.Context, fileIDs ...int64) error {
	if len(fileIDs) == 0 {
		_, err := s.exec(ctx, "UPDATE subtitles SET next_at = 0 WHERE state IN (?, ?)", SubMissing, SubWaiting)
		return err
	}
	for _, id := range fileIDs {
		if _, err := s.exec(ctx, "UPDATE subtitles SET next_at = 0 WHERE file_id = ? AND state IN (?, ?)", id, SubMissing, SubWaiting); err != nil {
			return err
		}
	}
	return nil
}

// SubtitleUse is a day's subtitle lookups with one provider.
type SubtitleUse struct {
	Searches  int `json:"searches"`
	Downloads int `json:"downloads"`
}

// AddSubtitleUse counts lookups today; provider "" counts files.
func (s *Store) AddSubtitleUse(ctx context.Context, provider string, searches, downloads int) error {
	_, err := s.exec(ctx, `INSERT INTO subtitle_usage (day, provider, searches, downloads) VALUES (?, ?, ?, ?)
		ON CONFLICT (day, provider) DO UPDATE SET searches = subtitle_usage.searches + excluded.searches,
			downloads = subtitle_usage.downloads + excluded.downloads`,
		DataDay(time.Now()), provider, searches, downloads)
	return err
}

// SubtitleUseOn is a day's lookups (as DataDay writes it), by provider.
// Days before the week before are forgotten.
func (s *Store) SubtitleUseOn(ctx context.Context, day string) (map[string]SubtitleUse, error) {
	s.exec(ctx, "DELETE FROM subtitle_usage WHERE day < ?", DataDay(time.Now().AddDate(0, 0, -7)))
	rows, err := s.query(ctx, "SELECT provider, searches, downloads FROM subtitle_usage WHERE day = ?", day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]SubtitleUse{}
	for rows.Next() {
		var p string
		var u SubtitleUse
		if err := rows.Scan(&p, &u.Searches, &u.Downloads); err != nil {
			return nil, err
		}
		out[p] = u
	}
	return out, rows.Err()
}

// PickedLanguages are the languages admins and members chose to see Box
// Office and its request site in, most used first, for suggesting which
// subtitles to fetch. Those following their browser aren't counted.
func (s *Store) PickedLanguages(ctx context.Context) ([]string, error) {
	rows, err := s.query(ctx, `SELECT language, COUNT(*) FROM (SELECT language FROM users UNION ALL SELECT language FROM members) l
		WHERE language <> '' GROUP BY language ORDER BY COUNT(*) DESC, language`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var l string
		var n int
		if err := rows.Scan(&l, &n); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
