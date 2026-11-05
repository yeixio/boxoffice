package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// secs is a time as Unix seconds, 0 for the zero time.
func secs(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func fromSecs(n int64) time.Time {
	if n <= 0 {
		return time.Time{}
	}
	return time.Unix(n, 0).UTC()
}

// ---- indexers ----

// IndexerStatus is how an indexer is doing.
type IndexerStatus struct {
	IndexerID    int64     `json:"indexerId"`
	Failures     int       `json:"failures"`              // errors in a row
	RestingUntil time.Time `json:"restingUntil,omitzero"` // not asked again before then
	LastError    string    `json:"lastError,omitempty"`
	FailedAt     time.Time `json:"failedAt,omitzero"`
	APIMax       int       `json:"apiMax,omitempty"` // the daily limits the indexer reports
	GrabMax      int       `json:"grabMax,omitempty"`
	RSSGUID      string    `json:"-"`
	RSSAt        time.Time `json:"-"`
	RSSSyncedAt  time.Time `json:"rssSyncedAt,omitzero"`
}

// Resting says whether the indexer is resting after errors at t.
func (st IndexerStatus) Resting(t time.Time) bool { return t.Before(st.RestingUntil) }

const indexerStatusCols = "indexer_id, failures, resting_until, last_error, failed_at, api_max, grab_max, rss_guid, rss_at, rss_synced_at"

func scanIndexerStatus(sc interface{ Scan(...any) error }) (IndexerStatus, error) {
	var st IndexerStatus
	var resting, failed, rssAt, synced int64
	err := sc.Scan(&st.IndexerID, &st.Failures, &resting, &st.LastError, &failed, &st.APIMax, &st.GrabMax, &st.RSSGUID, &rssAt, &synced)
	st.RestingUntil, st.FailedAt, st.RSSAt, st.RSSSyncedAt = fromSecs(resting), fromSecs(failed), fromSecs(rssAt), fromSecs(synced)
	return st, err
}

// IndexerStatuses is every indexer's status, by indexer.
func (s *Store) IndexerStatuses(ctx context.Context) (map[int64]IndexerStatus, error) {
	rows, err := s.query(ctx, "SELECT "+indexerStatusCols+" FROM indexer_status")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]IndexerStatus{}
	for rows.Next() {
		st, err := scanIndexerStatus(rows)
		if err != nil {
			return nil, err
		}
		out[st.IndexerID] = st
	}
	return out, rows.Err()
}

// IndexerStatus is one indexer's status; a new one has none yet.
func (s *Store) IndexerStatus(ctx context.Context, id int64) (IndexerStatus, error) {
	st, err := scanIndexerStatus(s.row(ctx, "SELECT "+indexerStatusCols+" FROM indexer_status WHERE indexer_id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return IndexerStatus{IndexerID: id}, nil
	}
	return st, err
}

func (s *Store) ensureIndexerStatus(ctx context.Context, id int64) error {
	_, err := s.exec(ctx, "INSERT INTO indexer_status (indexer_id) VALUES (?) ON CONFLICT (indexer_id) DO NOTHING", id)
	return err
}

// IndexerWorked clears an indexer's errors.
func (s *Store) IndexerWorked(ctx context.Context, id int64) error {
	if err := s.ensureIndexerStatus(ctx, id); err != nil {
		return err
	}
	_, err := s.exec(ctx, "UPDATE indexer_status SET failures = 0, resting_until = 0, last_error = '' WHERE indexer_id = ? AND (failures > 0 OR resting_until > 0)", id)
	return err
}

// IndexerFailed counts an error in a row and rests the indexer until
// until. It returns the errors in a row, this one included.
func (s *Store) IndexerFailed(ctx context.Context, id int64, why string, until time.Time) (int, error) {
	if err := s.ensureIndexerStatus(ctx, id); err != nil {
		return 0, err
	}
	if _, err := s.exec(ctx, "UPDATE indexer_status SET failures = failures + 1, resting_until = ?, last_error = ?, failed_at = ? WHERE indexer_id = ?",
		secs(until), why, now(), id); err != nil {
		return 0, err
	}
	var n int
	err := s.row(ctx, "SELECT failures FROM indexer_status WHERE indexer_id = ?", id).Scan(&n)
	return n, err
}

// RestIndexer sets how long an indexer rests, without counting an error;
// a zero time ends the rest.
func (s *Store) RestIndexer(ctx context.Context, id int64, until time.Time) error {
	if err := s.ensureIndexerStatus(ctx, id); err != nil {
		return err
	}
	_, err := s.exec(ctx, "UPDATE indexer_status SET resting_until = ? WHERE indexer_id = ?", secs(until), id)
	return err
}

// NoteIndexerLimits keeps the daily limits an indexer reports, and how
// much of them it says is used today, when that's more than Box Office
// counted (other apps may share the account).
func (s *Store) NoteIndexerLimits(ctx context.Context, id int64, apiMax, apiUsed, grabMax, grabUsed int) error {
	if err := s.ensureIndexerStatus(ctx, id); err != nil {
		return err
	}
	if _, err := s.exec(ctx, "UPDATE indexer_status SET api_max = CASE WHEN ? > 0 THEN ? ELSE api_max END, grab_max = CASE WHEN ? > 0 THEN ? ELSE grab_max END WHERE indexer_id = ?",
		apiMax, apiMax, grabMax, grabMax, id); err != nil {
		return err
	}
	if apiUsed <= 0 && grabUsed <= 0 {
		return nil
	}
	day := UsageDay(time.Now())
	if err := s.ensureUsage(ctx, id, day); err != nil {
		return err
	}
	_, err := s.exec(ctx, `UPDATE indexer_usage SET queries = CASE WHEN queries < ? THEN ? ELSE queries END, grabs = CASE WHEN grabs < ? THEN ? ELSE grabs END
		WHERE indexer_id = ? AND day = ?`, apiUsed, apiUsed, grabUsed, grabUsed, id, day)
	return err
}

// MarkRSS notes the newest release RSS sync has seen on an indexer.
func (s *Store) MarkRSS(ctx context.Context, id int64, guid string, at time.Time) error {
	if err := s.ensureIndexerStatus(ctx, id); err != nil {
		return err
	}
	if guid == "" {
		_, err := s.exec(ctx, "UPDATE indexer_status SET rss_synced_at = ? WHERE indexer_id = ?", now(), id)
		return err
	}
	_, err := s.exec(ctx, "UPDATE indexer_status SET rss_guid = ?, rss_at = ?, rss_synced_at = ? WHERE indexer_id = ?", guid, secs(at), now(), id)
	return err
}

// UsageDay is the day an indexer's usage counts toward: indexers reset
// their limits at midnight UTC.
func UsageDay(t time.Time) string { return t.UTC().Format(time.DateOnly) }

// IndexerUsage is what an indexer was asked on one day.
type IndexerUsage struct {
	Queries int `json:"queries"` // API calls: searches and RSS
	Grabs   int `json:"grabs"`   // NZBs downloaded
	Auto    int `json:"auto"`    // searches Box Office made by itself
}

func (s *Store) ensureUsage(ctx context.Context, id int64, day string) error {
	_, err := s.exec(ctx, "INSERT INTO indexer_usage (indexer_id, day) VALUES (?, ?) ON CONFLICT (indexer_id, day) DO NOTHING", id, day)
	return err
}

// CountIndexerUse adds to an indexer's usage today.
func (s *Store) CountIndexerUse(ctx context.Context, id int64, queries, grabs, auto int) error {
	day := UsageDay(time.Now())
	if err := s.ensureUsage(ctx, id, day); err != nil {
		return err
	}
	_, err := s.exec(ctx, "UPDATE indexer_usage SET queries = queries + ?, grabs = grabs + ?, auto = auto + ? WHERE indexer_id = ? AND day = ?", queries, grabs, auto, id, day)
	return err
}

// IndexerUsageOn is every indexer's usage on a day, by indexer.
func (s *Store) IndexerUsageOn(ctx context.Context, day string) (map[int64]IndexerUsage, error) {
	rows, err := s.query(ctx, "SELECT indexer_id, queries, grabs, auto FROM indexer_usage WHERE day = ?", day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]IndexerUsage{}
	for rows.Next() {
		var id int64
		var u IndexerUsage
		if err := rows.Scan(&id, &u.Queries, &u.Grabs, &u.Auto); err != nil {
			return nil, err
		}
		out[id] = u
	}
	return out, rows.Err()
}

// PruneIndexerUsage forgets usage from before a day.
func (s *Store) PruneIndexerUsage(ctx context.Context, before string) error {
	_, err := s.exec(ctx, "DELETE FROM indexer_usage WHERE day < ?", before)
	return err
}

// ---- data ----

// DataDay is the day bytes downloaded count toward, in local time, as an
// internet provider's billing month would.
func DataDay(t time.Time) string { return t.Local().Format(time.DateOnly) }

// AddDataUsage adds bytes downloaded from a news server today.
func (s *Store) AddDataUsage(ctx context.Context, serverID, n int64) error {
	if n <= 0 {
		return nil
	}
	day := DataDay(time.Now())
	_, err := s.exec(ctx, `INSERT INTO data_usage (day, server_id, bytes) VALUES (?, ?, ?)
		ON CONFLICT (day, server_id) DO UPDATE SET bytes = data_usage.bytes + excluded.bytes`, day, serverID, n)
	return err
}

// DataUsage is the bytes downloaded from each news server from a day on
// (inclusive), by server.
func (s *Store) DataUsage(ctx context.Context, since string) (map[int64]int64, error) {
	rows, err := s.query(ctx, "SELECT server_id, CAST(SUM(bytes) AS BIGINT) FROM data_usage WHERE day >= ? GROUP BY server_id", since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var id, n int64
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}
