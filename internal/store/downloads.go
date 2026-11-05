package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Download statuses. Queued, paused and the working ones are the queue;
// completed and failed are history.
const (
	DownloadQueued      = "queued"
	DownloadDownloading = "downloading"
	DownloadPaused      = "paused"
	DownloadVerifying   = "verifying"
	DownloadRepairing   = "repairing"
	DownloadUnpacking   = "unpacking"
	DownloadImporting   = "importing"
	DownloadCompleted   = "completed"
	DownloadFailed      = "failed"
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
	Position    int     `json:"position"`
	Folder      string  `json:"folder,omitempty"`
	Message     string  `json:"message,omitempty"`
	ImportedTo  string  `json:"importedTo,omitempty"`
	// Quarantined is where the files were moved when they looked unsafe.
	Quarantined string `json:"quarantined,omitempty"`
	// CanReprocess says its files are still there to check and unpack
	// again, without downloading them again. It's worked out, not stored.
	CanReprocess bool `json:"canReprocess,omitempty"`
	// CanImport says it's waiting to be moved into the library.
	CanImport bool `json:"canImport,omitempty"`
	// StepDone and StepTotal say how far checking, repairing or unpacking
	// has got, while it runs.
	StepDone   int64     `json:"stepDone,omitempty"`
	StepTotal  int64     `json:"stepTotal,omitempty"`
	AddedAt    time.Time `json:"addedAt"`
	StartedAt  time.Time `json:"startedAt,omitzero"`
	FinishedAt time.Time `json:"finishedAt,omitzero"`
}

// Finished says whether the download is history.
func (d Download) Finished() bool { return d.Status == DownloadCompleted || d.Status == DownloadFailed }

const downloadCols = `id, name, status, kind, title_id, episode_ids, quality, indexer, source, password, size, done_bytes, failed_bytes,
	done_files, position, folder, message, imported_to, quarantined, added_at, started_at, finished_at`

func scanDownload(r interface{ Scan(...any) error }) (Download, error) {
	var d Download
	var eps, files string
	var added, started, finished int64
	err := r.Scan(&d.ID, &d.Name, &d.Status, &d.Kind, &d.TitleID, &eps, &d.Quality, &d.Indexer, &d.Source, &d.Password, &d.Size,
		&d.DoneBytes, &d.FailedBytes, &files, &d.Position, &d.Folder, &d.Message, &d.ImportedTo, &d.Quarantined, &added, &started, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	if err != nil {
		return d, err
	}
	json.Unmarshal([]byte(eps), &d.EpisodeIDs)
	json.Unmarshal([]byte(files), &d.DoneFiles)
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
	id, err := s.insert(ctx, `INSERT INTO downloads (name, status, kind, title_id, episode_ids, quality, indexer, source, password, size, position, added_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.Name, d.Status, d.Kind, d.TitleID, jsonText(d.EpisodeIDs), d.Quality, d.Indexer, d.Source, d.Password, d.Size, pos+1, now())
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

// DownloadHistoryPage is one page of finished downloads, sorted by one of
// historyOrder's keys (newest finished first when it isn't one), and how
// many there are in all.
func (s *Store) DownloadHistoryPage(ctx context.Context, sort string, asc bool, offset, limit int) ([]Download, int, error) {
	var total int
	if err := s.row(ctx, "SELECT COUNT(*) FROM downloads WHERE status IN ('completed', 'failed')").Scan(&total); err != nil {
		return nil, 0, err
	}
	col, ok := historyOrder[sort]
	if !ok {
		col, asc = historyOrder["finished"], false
	}
	dir := " DESC"
	if asc {
		dir = " ASC"
	}
	list, err := s.downloads(ctx, "status IN ('completed', 'failed') ORDER BY "+col+dir+", id DESC LIMIT ? OFFSET ?", limit, max(offset, 0))
	return list, total, err
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
	_, err := s.exec(ctx, "UPDATE downloads SET done_bytes = ?, failed_bytes = ?, done_files = ?, size = ?, folder = ? WHERE id = ?",
		d.DoneBytes, d.FailedBytes, jsonText(d.DoneFiles), d.Size, d.Folder, d.ID)
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
	res, err := s.exec(ctx, `UPDATE downloads SET status = 'queued', message = '', done_bytes = 0, failed_bytes = 0, done_files = '[]', quarantined = '',
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
	res, err := s.exec(ctx, "UPDATE downloads SET status = 'queued', message = '', finished_at = 0, position = ? WHERE id = ?", pos+1, id)
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
	_, err := s.exec(ctx, "UPDATE downloads SET status = 'queued' WHERE status IN ('downloading', 'verifying', 'repairing', 'unpacking', 'importing')")
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
