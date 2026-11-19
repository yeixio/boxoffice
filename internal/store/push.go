package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// PushDevice is a browser or phone that asked for push notifications: an
// admin's (MemberID 0), or a request-site member's.
type PushDevice struct {
	ID int64 `json:"id"`
	// Endpoint is the address at the browser maker's push service that
	// reaches this device; P256dh and Auth are the device's keys, which
	// what's sent is encrypted with.
	Endpoint string `json:"endpoint"`
	P256dh   string `json:"-"`
	Auth     string `json:"-"`
	MemberID int64  `json:"-"`
	// Topics is what an admin's device hears about.
	Topics []string `json:"topics"`
	// Name says which device it is: "Chrome on Android".
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
}

const pushCols = "id, endpoint, p256dh, auth, member_id, topics, name, created_at"

func scanPush(sc interface{ Scan(...any) error }) (PushDevice, error) {
	var d PushDevice
	var topics string
	var created int64
	err := sc.Scan(&d.ID, &d.Endpoint, &d.P256dh, &d.Auth, &d.MemberID, &topics, &d.Name, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	d.Topics = []string{}
	if topics != "" {
		d.Topics = strings.Split(topics, ",")
	}
	d.CreatedAt = time.Unix(created, 0).UTC()
	return d, err
}

// SavePushDevice adds a device, or, when its endpoint is known already,
// updates whose it is and what it hears about.
func (s *Store) SavePushDevice(ctx context.Context, d PushDevice) (PushDevice, error) {
	topics := strings.Join(d.Topics, ",")
	old, err := scanPush(s.row(ctx, "SELECT "+pushCols+" FROM push_devices WHERE endpoint = ?", d.Endpoint))
	switch {
	case err == nil:
		if _, err := s.exec(ctx, "UPDATE push_devices SET p256dh = ?, auth = ?, member_id = ?, topics = ?, name = ? WHERE id = ?",
			d.P256dh, d.Auth, d.MemberID, topics, d.Name, old.ID); err != nil {
			return d, err
		}
		return s.PushDevice(ctx, old.ID)
	case !errors.Is(err, ErrNotFound):
		return d, err
	}
	id, err := s.insert(ctx, "INSERT INTO push_devices (endpoint, p256dh, auth, member_id, topics, name, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		d.Endpoint, d.P256dh, d.Auth, d.MemberID, topics, d.Name, now())
	if err != nil {
		return d, err
	}
	return s.PushDevice(ctx, id)
}

func (s *Store) PushDevice(ctx context.Context, id int64) (PushDevice, error) {
	return scanPush(s.row(ctx, "SELECT "+pushCols+" FROM push_devices WHERE id = ?", id))
}

// PushDevices lists a member's devices, or the admins' for member 0,
// oldest first.
func (s *Store) PushDevices(ctx context.Context, memberID int64) ([]PushDevice, error) {
	rows, err := s.query(ctx, "SELECT "+pushCols+" FROM push_devices WHERE member_id = ? ORDER BY id", memberID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PushDevice{}
	for rows.Next() {
		d, err := scanPush(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeletePushDevice forgets a device.
func (s *Store) DeletePushDevice(ctx context.Context, id int64) error {
	res, err := s.exec(ctx, "DELETE FROM push_devices WHERE id = ?", id)
	if err != nil {
		return err
	}
	return affected(res)
}

// DeletePushEndpoint forgets a member's device (an admin's for member 0)
// by its endpoint. One that isn't there is fine.
func (s *Store) DeletePushEndpoint(ctx context.Context, memberID int64, endpoint string) error {
	_, err := s.exec(ctx, "DELETE FROM push_devices WHERE member_id = ? AND endpoint = ?", memberID, endpoint)
	return err
}
