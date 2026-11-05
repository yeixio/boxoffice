package store

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"time"
)

// Notification events.
const (
	EventGrabbed     = "download.grabbed"     // a release was found and is downloading
	EventImported    = "download.imported"    // it's in the library
	EventDownloaded  = "download.completed"   // finished, but left in the downloads folder
	EventFailed      = "download.failed"      // it didn't download or unpack
	EventQuarantined = "download.quarantined" // it looked unsafe, so it was put aside
)

// Notification levels.
const (
	LevelInfo     = "info"
	LevelWarning  = "warning"
	LevelCritical = "critical"
)

// Who a notification is for.
const (
	// AudienceEveryone is the people who asked for the title, and admins.
	AudienceEveryone = "everyone"
	// AudienceAdmins is admins only: problems people can't do anything about.
	AudienceAdmins = "admins"
)

// Notification is something Box Office tells people about.
type Notification struct {
	ID       int64     `json:"id"`
	Event    string    `json:"event"`
	Level    string    `json:"level"`
	Audience string    `json:"audience"`
	Title    string    `json:"title"`
	Body     string    `json:"body,omitempty"`
	Media    *Media    `json:"media,omitempty"`
	Download int64     `json:"downloadId,omitempty"`
	At       time.Time `json:"at"`
}

// Media is the title a notification is about, with the ids Matinee matches
// it by.
type Media struct {
	Kind     string `json:"kind"` // movie or series
	Title    string `json:"title"`
	Year     int    `json:"year,omitempty"`
	TMDBID   int64  `json:"tmdbId,omitempty"`
	TVDBID   int64  `json:"tvdbId,omitempty"`
	IMDBID   string `json:"imdbId,omitempty"`
	Season   int    `json:"season,omitempty"`
	Episodes []int  `json:"episodes,omitempty"`
	Poster   string `json:"poster,omitempty"`
}

// keepNotifications is how long notifications stay for Matinee to read.
const keepNotifications = 30 * 24 * time.Hour

// AddNotification saves a notification and gives it its id.
func (s *Store) AddNotification(ctx context.Context, n Notification) (Notification, error) {
	if n.At.IsZero() {
		n.At = time.Now()
	}
	n.ID = 0
	b, _ := json.Marshal(n)
	id, err := s.insert(ctx, "INSERT INTO notifications (at, data) VALUES (?, ?)", n.At.Unix(), string(b))
	if err != nil {
		return n, err
	}
	n.ID = id
	s.exec(ctx, "DELETE FROM notifications WHERE at < ?", time.Now().Add(-keepNotifications).Unix())
	return n, nil
}

// Notifications lists notifications after an id, oldest first.
func (s *Store) Notifications(ctx context.Context, after int64, limit int) ([]Notification, error) {
	rows, err := s.query(ctx, "SELECT id, data FROM notifications WHERE id > ? ORDER BY id LIMIT ?", after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Notification{}
	for rows.Next() {
		var id int64
		var data string
		if err := rows.Scan(&id, &data); err != nil {
			return nil, err
		}
		var n Notification
		if json.Unmarshal([]byte(data), &n) != nil {
			continue
		}
		n.ID = id
		out = append(out, n)
	}
	return out, rows.Err()
}

// LastNotification is the newest notification's id, 0 for none.
func (s *Store) LastNotification(ctx context.Context) (int64, error) {
	var id int64
	err := s.row(ctx, "SELECT COALESCE(MAX(id), 0) FROM notifications").Scan(&id)
	return id, err
}

const settingInstance = "instance_id"

// InstanceID is this Box Office's own id, made up the first time it's
// asked for. Matinee uses it to tell one Box Office from another, and to
// see that its Radarr and Sonarr connections go to the same one.
func (s *Store) InstanceID(ctx context.Context) (string, error) {
	id, err := s.Setting(ctx, settingInstance)
	if err != nil || id != "" {
		return id, err
	}
	b := make([]byte, 16)
	rand.Read(b)
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	id = fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
	if err := s.SetSetting(ctx, settingInstance, id); err != nil {
		return "", err
	}
	return id, nil
}
