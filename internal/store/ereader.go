package store

import (
	"context"
	"time"
)

// EreaderSend is an ebook Box Office emailed to an e-reader, or tried to.
type EreaderSend struct {
	ID         int64     `json:"id"`
	DeviceID   string    `json:"deviceId"`
	DeviceName string    `json:"deviceName"`
	BookID     int64     `json:"bookId,omitempty"`
	Title      string    `json:"title"`
	Auto       bool      `json:"auto"`
	Error      string    `json:"error,omitempty"`
	At         time.Time `json:"at"`
}

// keepEreaderSends is how many sends are kept: the list in Settings, and
// what stops a book going to the same reader twice.
const keepEreaderSends = 2000

// AddEreaderSend notes a send.
func (s *Store) AddEreaderSend(ctx context.Context, e EreaderSend) (EreaderSend, error) {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	id, err := s.insert(ctx, "INSERT INTO ereader_sends (device_id, device_name, book_id, title, auto, error, sent_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		e.DeviceID, e.DeviceName, e.BookID, e.Title, b2i(e.Auto), e.Error, e.At.Unix())
	if err != nil {
		return e, err
	}
	e.ID = id
	s.exec(ctx, "DELETE FROM ereader_sends WHERE id <= ?", id-keepEreaderSends)
	return e, nil
}

// EreaderSends lists the latest sends, newest first.
func (s *Store) EreaderSends(ctx context.Context, limit int) ([]EreaderSend, error) {
	rows, err := s.query(ctx, "SELECT id, device_id, device_name, book_id, title, auto, error, sent_at FROM ereader_sends ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EreaderSend{}
	for rows.Next() {
		var e EreaderSend
		var auto int
		var at int64
		if err := rows.Scan(&e.ID, &e.DeviceID, &e.DeviceName, &e.BookID, &e.Title, &auto, &e.Error, &at); err != nil {
			return nil, err
		}
		e.Auto, e.At = auto == 1, time.Unix(at, 0).UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}

// SentToEreader says whether a book has gone to a reader already.
func (s *Store) SentToEreader(ctx context.Context, deviceID string, bookID int64) (bool, error) {
	var n int
	err := s.row(ctx, "SELECT COUNT(*) FROM ereader_sends WHERE device_id = ? AND book_id = ? AND error = ''", deviceID, bookID).Scan(&n)
	return n > 0, err
}
