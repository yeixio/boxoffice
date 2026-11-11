package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Download statuses. Queued, paused and the working ones are the queue;
// completed and failed are history.
const (
	DownloadQueued      = "queued"
	DownloadDownloading = "downloading"
	DownloadPaused      = "paused"
	// DownloadDownloaded is fetched, and waiting for its turn to be
	// checked and unpacked while another is.
	DownloadDownloaded = "downloaded"
	DownloadVerifying  = "verifying"
	DownloadRepairing  = "repairing"
	DownloadUnpacking  = "unpacking"
	DownloadImporting  = "importing"
	DownloadCompleted  = "completed"
	DownloadFailed     = "failed"
)

// Download is one release being downloaded, or downloaded.
type Download struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Status      string  `json:"status"`
	Kind        string  `json:"kind,omitempty"`
	TitleID     int64   `json:"titleId,omitempty"`
	EpisodeIDs  []int64 `json:"episodeIds,omitempty"`
	Quality     int     `json:"quality"`
	Indexer     string  `json:"indexer,omitempty"`
	Source      string  `json:"source,omitempty"`
	Password    string  `json:"-"`
	Size        int64   `json:"size"`
	DoneBytes   int64   `json:"doneBytes"`
	FailedBytes int64   `json:"failedBytes"`
	DoneFiles   []int   `json:"-"`
	// DoneNames is what each fetched file was saved as, by its index in
	// the NZB: the name inside the file's posts, which is often not the
	// one in the NZB's subject (obfuscated releases).
	DoneNames  map[int]string `json:"-"`
	Position   int            `json:"position"`
	Folder     string         `json:"folder,omitempty"`
	Message    string         `json:"message,omitempty"`
	ImportedTo string         `json:"importedTo,omitempty"`
	// Quarantined is where the files were moved when they looked unsafe.
	Quarantined string `json:"quarantined,omitempty"`
	// Missing says it failed because it can't be used: too much of it is
	// gone from the news servers to repair, or it's locked with a password
	// nobody gave. Nothing to fix, so it's kept out of sight.
	Missing bool `json:"missing,omitempty"`
	// Fetched says every file it needed was downloaded, so it can be
	// checked and unpacked again without downloading.
	Fetched bool `json:"-"`
	// CanReprocess says its files are still there to check and unpack
	// again, without downloading them again. It's worked out, not stored.
	CanReprocess bool `json:"canReprocess,omitempty"`
	// CanImport says it's waiting to be moved into the library.
	CanImport bool `json:"canImport,omitempty"`
	// StepDone and StepTotal say how far checking, repairing or unpacking
	// has got, while it runs.
	StepDone int64 `json:"stepDone,omitempty"`
	// Edition is the movie edition it was grabbed as (Director's Cut,
	// say), and Mode how it goes into the library: "" or "replace" takes
	// the movie's copy's place, "alongside" goes in next to it.
	Edition    string    `json:"edition,omitempty"`
	Mode       string    `json:"mode,omitempty"`
	StepTotal  int64     `json:"stepTotal,omitempty"`
	AddedAt    time.Time `json:"addedAt"`
	StartedAt  time.Time `json:"startedAt,omitzero"`
	FinishedAt time.Time `json:"finishedAt,omitzero"`
}

// Finished says whether the download is history.
func (d Download) Finished() bool { return d.Status == DownloadCompleted || d.Status == DownloadFailed }

const downloadCols = `id, name, status, kind, title_id, episode_ids, quality, indexer, source, password, size, done_bytes, failed_bytes,
	done_files, position, folder, message, imported_to, quarantined, added_at, started_at, finished_at, edition, mode, missing, fetched, done_names`

func scanDownload(r interface{ Scan(...any) error }) (Download, error) {
	var d Download
	var eps, files, names string
	var added, started, finished int64
	err := r.Scan(&d.ID, &d.Name, &d.Status, &d.Kind, &d.TitleID, &eps, &d.Quality, &d.Indexer, &d.Source, &d.Password, &d.Size,
		&d.DoneBytes, &d.FailedBytes, &files, &d.Position, &d.Folder, &d.Message, &d.ImportedTo, &d.Quarantined, &added, &started, &finished, &d.Edition, &d.Mode, &d.Missing, &d.Fetched, &names)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	if err != nil {
		return d, err
	}
	json.Unmarshal([]byte(eps), &d.EpisodeIDs)
	json.Unmarshal([]byte(files), &d.DoneFiles)
	json.Unmarshal([]byte(names), &d.DoneNames)
	d.AddedAt = time.Unix(added, 0).UTC()
	if started > 0 {
		d.StartedAt = time.Unix(started, 0).UTC()
	}
	if finished > 0 {
		d.FinishedAt = time.Unix(finished, 0).UTC()
	}
	return d, nil
}

func jsonText(v any) string {
	b, _ := json.Marshal(v)
	if string(b) == "null" {
		return "[]"
	}
	return string(b)
}

// AddDownload puts a download at the end of the queue.
func (s *Store) AddDownload(ctx context.Context, d Download) (Download, error) {
	var pos int
	if err := s.row(ctx, "SELECT COALESCE(MAX(position), 0) FROM downloads").Scan(&pos); err != nil {
		return d, err
	}
	if d.Status == "" {
		d.Status = DownloadQueued
	}
	id, err := s.insert(ctx, `INSERT INTO downloads (name, status, kind, title_id, episode_ids, quality, indexer, source, password, size, position, added_at, edition, mode)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.Name, d.Status, d.Kind, d.TitleID, jsonText(d.EpisodeIDs), d.Quality, d.Indexer, d.Source, d.Password, d.Size, pos+1, now(), d.Edition, d.Mode)
	if err != nil {
		return d, err
	}
	return s.Download(ctx, id)
}

func (s *Store) Download(ctx context.Context, id int64) (Download, error) {
	return scanDownload(s.row(ctx, "SELECT "+downloadCols+" FROM downloads WHERE id = ?", id))
}

func (s *Store) downloads(ctx context.Context, where string, args ...any) ([]Download, error) {
	rows, err := s.query(ctx, "SELECT "+downloadCols+" FROM downloads WHERE "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Download{}
	for rows.Next() {
		d, err := scanDownload(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DownloadQueue is everything not finished, in queue order.
func (s *Store) DownloadQueue(ctx context.Context) ([]Download, error) {
	return s.downloads(ctx, "status NOT IN ('completed', 'failed') ORDER BY position, id")
}

// DownloadHistory is finished downloads, newest first.
func (s *Store) DownloadHistory(ctx context.Context, limit int) ([]Download, error) {
	return s.downloads(ctx, "status IN ('completed', 'failed') ORDER BY finished_at DESC, id DESC LIMIT ?", limit)
}

// historyOrder is how a page of the history can be sorted, by name.
var historyOrder = map[string]string{
	"finished": "finished_at",
	"added":    "added_at",
	"name":     "LOWER(name)",
	"size":     "size",
	// What needs a look first: failed (quarantined among them), then
	// waiting to go into the library, then done.
	"status": "CASE WHEN status = 'failed' THEN 0 WHEN imported_to = '' AND kind <> '' THEN 2 ELSE 3 END",
}

// HistoryPage says which finished downloads to list, and how.
type HistoryPage struct {
	// Sort is one of historyOrder's keys; newest finished first when it
	// isn't one.
	Sort string
	Asc  bool
	// Missing includes failures for releases that can't be used (gone
	// from the news servers, or locked with a password), which are left
	// out otherwise.
	Missing bool
	// Query keeps those whose name has every word of it, ignoring case
	// and the dots and underscores release names use for spaces.
	Query         string
	Offset, Limit int
}

// DownloadHistoryPage is one page of finished downloads, how many there
// are in all, and how many failures for missing articles were left out.
func (s *Store) DownloadHistoryPage(ctx context.Context, p HistoryPage) (list []Download, total, hidden int, err error) {
	where := "status IN ('completed', 'failed')"
	var args []any
	for _, w := range nameWords(p.Query) {
		where += ` AND LOWER(REPLACE(REPLACE(REPLACE(name, '.', ' '), '_', ' '), '-', ' ')) LIKE ? ESCAPE '\'`
		args = append(args, "%"+likeEscaper.Replace(w)+"%")
	}
	if err := s.row(ctx, "SELECT COUNT(*) FROM downloads WHERE "+where+" AND missing <> 0", args...).Scan(&hidden); err != nil {
		return nil, 0, 0, err
	}
	if !p.Missing {
		where += " AND missing = 0"
	} else {
		hidden = 0
	}
	if err := s.row(ctx, "SELECT COUNT(*) FROM downloads WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, 0, err
	}
	col, ok := historyOrder[p.Sort]
	asc := p.Asc
	if !ok {
		col, asc = historyOrder["finished"], false
	}
	dir := " DESC"
	if asc {
		dir = " ASC"
	}
	list, err = s.downloads(ctx, where+" ORDER BY "+col+dir+", id DESC LIMIT ? OFFSET ?", append(args, p.Limit, max(p.Offset, 0))...)
	return list, total, hidden, err
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)

// nameWords splits a search into lowercase words, the way release names
// separate them.
func nameWords(q string) []string {
	return strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return r == ' ' || r == '.' || r == '_' || r == '-' || r == '\t'
	})
}

// SetDownloadFetched records that a download has all the files it needs.
func (s *Store) SetDownloadFetched(ctx context.Context, id int64) error {
	_, err := s.exec(ctx, "UPDATE downloads SET fetched = 1 WHERE id = ?", id)
	return err
}

// SetDownloadMissing marks a download as failed for articles gone from the
// news servers.
func (s *Store) SetDownloadMissing(ctx context.Context, id int64) error {
	_, err := s.exec(ctx, "UPDATE downloads SET missing = 1 WHERE id = ?", id)
	return err
}

// SetDownloadStatus moves a download to a status, with a note. Finishing
// stamps the time; starting to download stamps that.
func (s *Store) SetDownloadStatus(ctx context.Context, id int64, status, message string) error {
	q := "UPDATE downloads SET status = ?, message = ?"
	switch status {
	case DownloadCompleted, DownloadFailed:
		q += ", finished_at = " + itoa(now())
	case DownloadDownloading:
		q += ", started_at = CASE WHEN started_at = 0 THEN " + itoa(now()) + " ELSE started_at END"
	case DownloadQueued:
		q += ", finished_at = 0"
	}
	res, err := s.exec(ctx, q+" WHERE id = ?", status, message, id)
	if err != nil {
		return err
	}
	return affected(res)
}

// SaveDownloadProgress records how far a download has got.
func (s *Store) SaveDownloadProgress(ctx context.Context, d Download) error {
	names := "{}"
	if len(d.DoneNames) > 0 {
		b, _ := json.Marshal(d.DoneNames)
		names = string(b)
	}
	_, err := s.exec(ctx, "UPDATE downloads SET done_bytes = ?, failed_bytes = ?, done_files = ?, done_names = ?, size = ?, folder = ? WHERE id = ?",
		d.DoneBytes, d.FailedBytes, jsonText(d.DoneFiles), names, d.Size, d.Folder, d.ID)
	return err
}

// SetDownloadImported records where a download's files went.
func (s *Store) SetDownloadImported(ctx context.Context, id int64, to string) error {
	_, err := s.exec(ctx, "UPDATE downloads SET imported_to = ? WHERE id = ?", to, id)
	return err
}

// SetDownloadMessage changes what a download says, and nothing else.
func (s *Store) SetDownloadMessage(ctx context.Context, id int64, message string) error {
	_, err := s.exec(ctx, "UPDATE downloads SET message = ? WHERE id = ?", message, id)
	return err
}

// SetDownloadQuarantined records where a download's unsafe files went.
func (s *Store) SetDownloadQuarantined(ctx context.Context, id int64, to string) error {
	_, err := s.exec(ctx, "UPDATE downloads SET quarantined = ? WHERE id = ?", to, id)
	return err
}

// ResetDownload puts a finished download back in the queue, to try again
// from the start.
func (s *Store) ResetDownload(ctx context.Context, id int64) error {
	var pos int
	if err := s.row(ctx, "SELECT COALESCE(MAX(position), 0) FROM downloads").Scan(&pos); err != nil {
		return err
	}
	res, err := s.exec(ctx, `UPDATE downloads SET status = 'queued', message = '', done_bytes = 0, failed_bytes = 0, done_files = '[]', done_names = '{}', quarantined = '', missing = 0, fetched = 0,
		started_at = 0, finished_at = 0, position = ? WHERE id = ?`, pos+1, id)
	if err != nil {
		return err
	}
	return affected(res)
}

// RequeueDownload puts a finished download back in the queue keeping what
// it fetched, so only what's missing is downloaded and the rest is checked
// and unpacked again.
func (s *Store) RequeueDownload(ctx context.Context, id int64) error {
	var pos int
	if err := s.row(ctx, "SELECT COALESCE(MAX(position), 0) FROM downloads").Scan(&pos); err != nil {
		return err
	}
	res, err := s.exec(ctx, "UPDATE downloads SET status = 'queued', message = '', missing = 0, finished_at = 0, position = ? WHERE id = ?", pos+1, id)
	if err != nil {
		return err
	}
	return affected(res)
}

// MoveDownloadToTop puts a download first in the queue.
func (s *Store) MoveDownloadToTop(ctx context.Context, id int64) error {
	var pos int
	if err := s.row(ctx, "SELECT COALESCE(MIN(position), 0) FROM downloads").Scan(&pos); err != nil {
		return err
	}
	res, err := s.exec(ctx, "UPDATE downloads SET position = ? WHERE id = ?", pos-1, id)
	if err != nil {
		return err
	}
	return affected(res)
}

// MoveDownloadToBottom puts a queued download last.
func (s *Store) MoveDownloadToBottom(ctx context.Context, id int64) error {
	var pos int
	if err := s.row(ctx, "SELECT COALESCE(MAX(position), 0) FROM downloads").Scan(&pos); err != nil {
		return err
	}
	res, err := s.exec(ctx, "UPDATE downloads SET position = ? WHERE id = ?", pos+1, id)
	if err != nil {
		return err
	}
	return affected(res)
}

func (s *Store) DeleteDownload(ctx context.Context, id int64) error {
	res, err := s.exec(ctx, "DELETE FROM downloads WHERE id = ?", id)
	if err != nil {
		return err
	}
	return affected(res)
}

// ClearDownloadHistory forgets finished downloads, and says which.
func (s *Store) ClearDownloadHistory(ctx context.Context) ([]Download, error) {
	list, err := s.downloads(ctx, "status IN ('completed', 'failed')")
	if err != nil {
		return nil, err
	}
	_, err = s.exec(ctx, "DELETE FROM downloads WHERE status IN ('completed', 'failed')")
	return list, err
}

// RequeueInterruptedDownloads puts downloads that were mid-way when Box
// Office stopped back in the queue, to carry on.
func (s *Store) RequeueInterruptedDownloads(ctx context.Context) error {
	_, err := s.exec(ctx, "UPDATE downloads SET status = 'queued' WHERE status IN ('downloading', 'downloaded', 'verifying', 'repairing', 'unpacking', 'importing')")
	return err
}

// GrabbedBefore says whether a release of that name was added to the
// downloads for the title already.
func (s *Store) GrabbedBefore(ctx context.Context, kind string, titleID int64, name string) (bool, error) {
	var n int
	err := s.row(ctx, "SELECT COUNT(*) FROM downloads WHERE kind = ? AND title_id = ? AND name = ?", kind, titleID, name).Scan(&n)
	return n > 0, err
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// Pending is a title's downloads that may still bring it a file: those
// not finished, and finished ones from the last two weeks that are waiting
// to be moved into the library. Automatic searches leave such a title
// alone, so a copy isn't grabbed twice.
func (s *Store) Pending(ctx context.Context, kind string, titleID int64) ([]Download, error) {
	since := time.Now().AddDate(0, 0, -14).Unix()
	return s.downloads(ctx, `kind = ? AND title_id = ? AND (status NOT IN ('completed', 'failed')
		OR (status = 'completed' AND imported_to = '' AND quarantined = '' AND finished_at > ?)) ORDER BY id`, kind, titleID, since)
}

// ResumeDownloadsPaused puts back in the queue paused downloads whose
// message begins with one of prefixes or holds note, and says how many.
func (s *Store) ResumeDownloadsPaused(ctx context.Context, prefixes []string, note string) (int64, error) {
	var conds []string
	var args []any
	for _, p := range prefixes {
		conds = append(conds, "message LIKE ?")
		args = append(args, p+"%")
	}
	if note != "" {
		conds = append(conds, "message LIKE ?")
		args = append(args, "%"+note+"%")
	}
	if len(conds) == 0 {
		return 0, nil
	}
	res, err := s.exec(ctx, "UPDATE downloads SET status = ?, message = '', finished_at = 0 WHERE status = ? AND ("+strings.Join(conds, " OR ")+")",
		append([]any{DownloadQueued, DownloadPaused}, args...)...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
