package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

// User is an admin of this server. Box Office has no other kind of user:
// the people who ask for titles do it in Matinee.
type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	hash     string
}

func (u User) PasswordHash() string { return u.hash }

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	return n, s.row(ctx, "SELECT COUNT(*) FROM users").Scan(&n)
}

func (s *Store) CreateUser(ctx context.Context, username, passwordHash string) (User, error) {
	id, err := s.insert(ctx, "INSERT INTO users (username, password_hash, created_at) VALUES (?, ?, ?)", username, passwordHash, now())
	return User{ID: id, Username: username, hash: passwordHash}, err
}

func (s *Store) UserByName(ctx context.Context, username string) (User, error) {
	var u User
	err := s.row(ctx, "SELECT id, username, password_hash FROM users WHERE username = ?", username).Scan(&u.ID, &u.Username, &u.hash)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

func (s *Store) SetPassword(ctx context.Context, id int64, passwordHash string) error {
	res, err := s.exec(ctx, "UPDATE users SET password_hash = ? WHERE id = ?", passwordHash, id)
	if err != nil {
		return err
	}
	if err := affected(res); err != nil {
		return err
	}
	// Signed in elsewhere with the old password: not any more.
	_, err = s.exec(ctx, "DELETE FROM sessions WHERE user_id = ?", id)
	return err
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

// NewToken makes a random token, as hex.
func NewToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// CreateSession signs a user in and returns the session token. Only its
// hash is stored.
func (s *Store) CreateSession(ctx context.Context, userID int64, ttl time.Duration) (string, error) {
	t := NewToken()
	_, err := s.exec(ctx, "INSERT INTO sessions (token_hash, user_id, expires_at) VALUES (?, ?, ?)", hashToken(t), userID, time.Now().Add(ttl).Unix())
	return t, err
}

// SessionUser is who a session token belongs to, while it lasts.
func (s *Store) SessionUser(ctx context.Context, token string) (User, error) {
	var u User
	err := s.row(ctx, `SELECT u.id, u.username, u.password_hash FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.expires_at > ?`, hashToken(token), now()).Scan(&u.ID, &u.Username, &u.hash)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.exec(ctx, "DELETE FROM sessions WHERE token_hash = ? OR expires_at < ?", hashToken(token), now())
	return err
}
