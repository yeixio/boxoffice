package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Passkey is a passkey someone signs in with: an admin's (MemberID 0), or
// a request-site member's.
type Passkey struct {
	ID int64 `json:"id"`
	// Credential is the authenticator's name for it, and PublicKey its
	// public key, both base64url.
	Credential string `json:"-"`
	PublicKey  string `json:"-"`
	Alg        int    `json:"-"`
	SignCount  uint32 `json:"-"`
	// Host is the host name it was made at, the only one it works at.
	Host     string `json:"host"`
	MemberID int64  `json:"-"`
	// Name says which it is: "iPhone", or the browser it was made in.
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	// UsedAt is when it last signed in; zero when it never has.
	UsedAt time.Time `json:"usedAt,omitzero"`
}

const passkeyCols = "id, credential, public_key, alg, sign_count, host, member_id, name, created_at, used_at"

func scanPasskey(sc interface{ Scan(...any) error }) (Passkey, error) {
	var p Passkey
	var count, created, used int64
	err := sc.Scan(&p.ID, &p.Credential, &p.PublicKey, &p.Alg, &count, &p.Host, &p.MemberID, &p.Name, &created, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	p.SignCount = uint32(count)
	p.CreatedAt = time.Unix(created, 0).UTC()
	if used > 0 {
		p.UsedAt = time.Unix(used, 0).UTC()
	}
	return p, err
}

// AddPasskey keeps a new passkey.
func (s *Store) AddPasskey(ctx context.Context, p Passkey) (Passkey, error) {
	id, err := s.insert(ctx, "INSERT INTO passkeys (credential, public_key, alg, sign_count, host, member_id, name, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		p.Credential, p.PublicKey, p.Alg, int64(p.SignCount), p.Host, p.MemberID, p.Name, now())
	if err != nil {
		return p, err
	}
	return scanPasskey(s.row(ctx, "SELECT "+passkeyCols+" FROM passkeys WHERE id = ?", id))
}

// PasskeyByCredential finds a passkey by the authenticator's name for it.
func (s *Store) PasskeyByCredential(ctx context.Context, credential string) (Passkey, error) {
	return scanPasskey(s.row(ctx, "SELECT "+passkeyCols+" FROM passkeys WHERE credential = ?", credential))
}

// Passkeys lists a member's passkeys, or the admin's for member 0, oldest
// first.
func (s *Store) Passkeys(ctx context.Context, memberID int64) ([]Passkey, error) {
	rows, err := s.query(ctx, "SELECT "+passkeyCols+" FROM passkeys WHERE member_id = ? ORDER BY id", memberID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Passkey{}
	for rows.Next() {
		p, err := scanPasskey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// HasPasskeys says whether anyone of a kind, admins or members, has a
// passkey for a host: whether its sign-in page should offer them.
func (s *Store) HasPasskeys(ctx context.Context, members bool, host string) (bool, error) {
	q := "SELECT COUNT(*) FROM passkeys WHERE host = ? AND member_id = 0"
	if members {
		q = "SELECT COUNT(*) FROM passkeys WHERE host = ? AND member_id > 0"
	}
	var n int
	err := s.row(ctx, q, host).Scan(&n)
	return n > 0, err
}

// PasskeyUsed keeps a passkey's new count and when it signed in.
func (s *Store) PasskeyUsed(ctx context.Context, id int64, count uint32) error {
	_, err := s.exec(ctx, "UPDATE passkeys SET sign_count = ?, used_at = ? WHERE id = ?", int64(count), now(), id)
	return err
}

// DeletePasskey forgets one of a member's passkeys (the admin's for
// member 0).
func (s *Store) DeletePasskey(ctx context.Context, memberID, id int64) error {
	res, err := s.exec(ctx, "DELETE FROM passkeys WHERE id = ? AND member_id = ?", id, memberID)
	if err != nil {
		return err
	}
	return affected(res)
}
