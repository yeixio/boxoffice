package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Member is someone who uses the request site: they ask for titles and
// report problems, but can't sign in to the admin UI.
type Member struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	// Source is local, for an account made here, or matinee, for one that
	// signs in with its Matinee account.
	Source string `json:"source"`
	// AutoApprove is '' to follow Settings, or on or off for this person.
	AutoApprove string `json:"autoApprove"`
	// QuotaMovies and QuotaShows are how many a week this person may ask
	// for: -1 follows Settings, 0 is no limit.
	QuotaMovies int       `json:"quotaMovies"`
	QuotaShows  int       `json:"quotaShows"`
	Disabled    bool      `json:"disabled"`
	CreatedAt   time.Time `json:"createdAt"`
	SeenAt      time.Time `json:"seenAt,omitzero"`
	// Language is the one they picked, or '' to follow the browser.
	Language string `json:"language"`
	hash     string
}

func (m Member) PasswordHash() string { return m.hash }

// Name is what the request site calls them.
func (m Member) Name() string {
	if m.DisplayName != "" {
		return m.DisplayName
	}
	return m.Username
}

const memberCols = "id, username, display_name, password_hash, source, auto_approve, quota_movies, quota_shows, disabled, created_at, seen_at, language"

func scanMember(sc interface{ Scan(...any) error }) (Member, error) {
	var m Member
	var disabled int
	var created, seen int64
	err := sc.Scan(&m.ID, &m.Username, &m.DisplayName, &m.hash, &m.Source, &m.AutoApprove, &m.QuotaMovies, &m.QuotaShows, &disabled, &created, &seen, &m.Language)
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	m.Disabled = disabled != 0
	m.CreatedAt = time.Unix(created, 0).UTC()
	if seen > 0 {
		m.SeenAt = time.Unix(seen, 0).UTC()
	}
	return m, err
}

// MemberName is how usernames are kept: trimmed and lowercased, so
// "Ellen" and "ellen" are one person.
func MemberName(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// CreateMember adds someone to the request site. QuotaMovies and
// QuotaShows of -1 follow Settings.
func (s *Store) CreateMember(ctx context.Context, m Member, passwordHash string) (Member, error) {
	m.Username = MemberName(m.Username)
	if m.Source == "" {
		m.Source = "local"
	}
	id, err := s.insert(ctx, `INSERT INTO members (username, display_name, password_hash, source, auto_approve, quota_movies, quota_shows, disabled, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, m.Username, m.DisplayName, passwordHash, m.Source, m.AutoApprove, m.QuotaMovies, m.QuotaShows, b2i(m.Disabled), now())
	if err != nil {
		return m, err
	}
	return s.Member(ctx, id)
}

func (s *Store) Member(ctx context.Context, id int64) (Member, error) {
	return scanMember(s.row(ctx, "SELECT "+memberCols+" FROM members WHERE id = ?", id))
}

func (s *Store) MemberByName(ctx context.Context, username string) (Member, error) {
	return scanMember(s.row(ctx, "SELECT "+memberCols+" FROM members WHERE username = ?", MemberName(username)))
}

func (s *Store) Members(ctx context.Context) ([]Member, error) {
	rows, err := s.query(ctx, "SELECT "+memberCols+" FROM members ORDER BY username")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Member{}
	for rows.Next() {
		m, err := scanMember(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UpdateMember saves what an admin can change about someone: their name,
// approval and limits, and whether they may sign in. Turning someone off
// signs them out.
func (s *Store) UpdateMember(ctx context.Context, m Member) error {
	res, err := s.exec(ctx, "UPDATE members SET display_name = ?, auto_approve = ?, quota_movies = ?, quota_shows = ?, disabled = ? WHERE id = ?",
		m.DisplayName, m.AutoApprove, m.QuotaMovies, m.QuotaShows, b2i(m.Disabled), m.ID)
	if err != nil {
		return err
	}
	if err := affected(res); err != nil {
		return err
	}
	if m.Disabled {
		_, err = s.exec(ctx, "DELETE FROM member_sessions WHERE member_id = ?", m.ID)
	}
	return err
}

// SetMemberPassword changes a local account's password and signs it out
// everywhere.
func (s *Store) SetMemberPassword(ctx context.Context, id int64, passwordHash string) error {
	res, err := s.exec(ctx, "UPDATE members SET password_hash = ? WHERE id = ?", passwordHash, id)
	if err != nil {
		return err
	}
	if err := affected(res); err != nil {
		return err
	}
	_, err = s.exec(ctx, "DELETE FROM member_sessions WHERE member_id = ?", id)
	return err
}

// DeleteMember removes someone, with their requests and reports.
func (s *Store) DeleteMember(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// SQLite only follows ON DELETE CASCADE with foreign keys on, so the
	// children go first by hand.
	for _, t := range []string{"member_sessions", "requests", "issues"} {
		if _, err := tx.ExecContext(ctx, s.q("DELETE FROM "+t+" WHERE member_id = ?"), id); err != nil {
			return err
		}
	}
	res, err := tx.ExecContext(ctx, s.q("DELETE FROM members WHERE id = ?"), id)
	if err != nil {
		return err
	}
	if err := affected(res); err != nil {
		return err
	}
	return tx.Commit()
}

// SetMemberLanguage keeps the language someone picked on the request site,
// or ” for their browser's.
func (s *Store) SetMemberLanguage(ctx context.Context, id int64, language string) error {
	res, err := s.exec(ctx, "UPDATE members SET language = ? WHERE id = ?", language, id)
	if err != nil {
		return err
	}
	return affected(res)
}

// CreateMemberSession signs someone in to the request site.
func (s *Store) CreateMemberSession(ctx context.Context, memberID int64, ttl time.Duration) (string, error) {
	t := NewToken()
	if _, err := s.exec(ctx, "INSERT INTO member_sessions (token_hash, member_id, expires_at) VALUES (?, ?, ?)", hashToken(t), memberID, time.Now().Add(ttl).Unix()); err != nil {
		return "", err
	}
	_, err := s.exec(ctx, "UPDATE members SET seen_at = ? WHERE id = ?", now(), memberID)
	return t, err
}

// SessionMember is who a request site session belongs to, while it lasts
// and they may still sign in.
func (s *Store) SessionMember(ctx context.Context, token string) (Member, error) {
	return scanMember(s.row(ctx, `SELECT `+prefixed("m.", memberCols)+` FROM member_sessions ms JOIN members m ON m.id = ms.member_id
		WHERE ms.token_hash = ? AND ms.expires_at > ? AND m.disabled = 0`, hashToken(token), now()))
}

func (s *Store) DeleteMemberSession(ctx context.Context, token string) error {
	_, err := s.exec(ctx, "DELETE FROM member_sessions WHERE token_hash = ? OR expires_at < ?", hashToken(token), now())
	return err
}

func prefixed(p, cols string) string {
	parts := strings.Split(cols, ", ")
	for i := range parts {
		parts[i] = p + parts[i]
	}
	return strings.Join(parts, ", ")
}

// ---- requests ----

// Request is a title someone asked for on the request site.
type Request struct {
	ID       int64  `json:"id"`
	MemberID int64  `json:"memberId"`
	Member   string `json:"member,omitempty"` // their name, for admins
	Kind     string `json:"kind"`             // movie or series
	TMDBID   int64  `json:"tmdbId"`
	Title    string `json:"title"`
	Year     int    `json:"year,omitempty"`
	Poster   string `json:"poster,omitempty"`
	// Status is pending, approved or declined.
	Status    string    `json:"status"`
	Note      string    `json:"note,omitempty"`
	Reply     string    `json:"reply,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	DecidedAt time.Time `json:"decidedAt,omitzero"`
}

const (
	RequestPending  = "pending"
	RequestApproved = "approved"
	RequestDeclined = "declined"
)

const requestCols = "r.id, r.member_id, COALESCE(NULLIF(m.display_name, ''), m.username), r.kind, r.tmdb_id, r.title, r.year, r.poster, r.status, r.note, r.reply, r.created_at, r.decided_at"

func scanRequest(sc interface{ Scan(...any) error }) (Request, error) {
	var r Request
	var created, decided int64
	err := sc.Scan(&r.ID, &r.MemberID, &r.Member, &r.Kind, &r.TMDBID, &r.Title, &r.Year, &r.Poster, &r.Status, &r.Note, &r.Reply, &created, &decided)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	r.CreatedAt = time.Unix(created, 0).UTC()
	if decided > 0 {
		r.DecidedAt = time.Unix(decided, 0).UTC()
	}
	return r, err
}

func (s *Store) AddRequest(ctx context.Context, r Request) (Request, error) {
	if r.Status == "" {
		r.Status = RequestPending
	}
	var decided int64
	if r.Status != RequestPending {
		decided = now()
	}
	id, err := s.insert(ctx, `INSERT INTO requests (member_id, kind, tmdb_id, title, year, poster, status, note, created_at, decided_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, r.MemberID, r.Kind, r.TMDBID, r.Title, r.Year, r.Poster, r.Status, r.Note, now(), decided)
	if err != nil {
		return r, err
	}
	return s.Request(ctx, id)
}

func (s *Store) Request(ctx context.Context, id int64) (Request, error) {
	return scanRequest(s.row(ctx, "SELECT "+requestCols+" FROM requests r JOIN members m ON m.id = r.member_id WHERE r.id = ?", id))
}

func (s *Store) requests(ctx context.Context, where string, args ...any) ([]Request, error) {
	rows, err := s.query(ctx, "SELECT "+requestCols+" FROM requests r JOIN members m ON m.id = r.member_id WHERE "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Request{}
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Requests lists requests, newest first: those with status, or all when
// status is empty.
func (s *Store) Requests(ctx context.Context, status string, limit int) ([]Request, error) {
	if status == "" {
		return s.requests(ctx, "1 = 1 ORDER BY r.created_at DESC, r.id DESC LIMIT ?", limit)
	}
	return s.requests(ctx, "r.status = ? ORDER BY r.created_at DESC, r.id DESC LIMIT ?", status, limit)
}

// MemberRequests is what one person asked for, newest first.
func (s *Store) MemberRequests(ctx context.Context, memberID int64) ([]Request, error) {
	return s.requests(ctx, "r.member_id = ? ORDER BY r.created_at DESC, r.id DESC LIMIT 500", memberID)
}

// TitleRequests is every request for one title, oldest first.
func (s *Store) TitleRequests(ctx context.Context, kind string, tmdbID int64) ([]Request, error) {
	return s.requests(ctx, "r.kind = ? AND r.tmdb_id = ? ORDER BY r.created_at, r.id", kind, tmdbID)
}

// CountRequests is how many titles of a kind someone asked for since a
// time, leaving out declined ones, for weekly limits.
func (s *Store) CountRequests(ctx context.Context, memberID int64, kind string, since time.Time) (int, error) {
	var n int
	err := s.row(ctx, "SELECT COUNT(*) FROM requests WHERE member_id = ? AND kind = ? AND status <> ? AND created_at >= ?",
		memberID, kind, RequestDeclined, since.Unix()).Scan(&n)
	return n, err
}

// DecideRequest approves or declines a pending request. A request that
// was decided already is ErrInUse.
func (s *Store) DecideRequest(ctx context.Context, id int64, status, reply string) error {
	res, err := s.exec(ctx, "UPDATE requests SET status = ?, reply = ?, decided_at = ? WHERE id = ? AND status = ?", status, reply, now(), id, RequestPending)
	if err != nil {
		return err
	}
	if err := affected(res); errors.Is(err, ErrNotFound) {
		if _, err := s.Request(ctx, id); err != nil {
			return err
		}
		return ErrInUse
	}
	return err
}

// DeleteRequest removes a request.
func (s *Store) DeleteRequest(ctx context.Context, id int64) error {
	res, err := s.exec(ctx, "DELETE FROM requests WHERE id = ?", id)
	if err != nil {
		return err
	}
	return affected(res)
}

// CountPending is how many requests wait for an admin and how many
// problem reports are open, for the admin UI's menu.
func (s *Store) CountPending(ctx context.Context) (requests, issues int, err error) {
	if err = s.row(ctx, "SELECT COUNT(*) FROM requests WHERE status = ?", RequestPending).Scan(&requests); err != nil {
		return
	}
	err = s.row(ctx, "SELECT COUNT(*) FROM issues WHERE status = ?", IssueOpen).Scan(&issues)
	return
}

// ---- problem reports ----

// Issue is a problem someone reported with a title in the library.
type Issue struct {
	ID       int64  `json:"id"`
	MemberID int64  `json:"memberId"`
	Member   string `json:"member,omitempty"`
	Kind     string `json:"kind"`
	TMDBID   int64  `json:"tmdbId"`
	Title    string `json:"title"`
	Year     int    `json:"year,omitempty"`
	Poster   string `json:"poster,omitempty"`
	Season   int    `json:"season,omitempty"`
	Episode  int    `json:"episode,omitempty"`
	// Problem is one of the Problem constants.
	Problem    string    `json:"problem"`
	Message    string    `json:"message,omitempty"`
	Status     string    `json:"status"` // open or resolved
	Reply      string    `json:"reply,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	ResolvedAt time.Time `json:"resolvedAt,omitzero"`
}

const (
	IssueOpen     = "open"
	IssueResolved = "resolved"
)

// The problems people can report.
var Problems = []string{"wont_play", "wrong_title", "picture", "sound", "subtitles", "other"}

const issueCols = "i.id, i.member_id, COALESCE(NULLIF(m.display_name, ''), m.username), i.kind, i.tmdb_id, i.title, i.year, i.poster, i.season, i.episode, i.problem, i.message, i.status, i.reply, i.created_at, i.resolved_at"

func scanIssue(sc interface{ Scan(...any) error }) (Issue, error) {
	var i Issue
	var created, resolved int64
	err := sc.Scan(&i.ID, &i.MemberID, &i.Member, &i.Kind, &i.TMDBID, &i.Title, &i.Year, &i.Poster, &i.Season, &i.Episode, &i.Problem, &i.Message, &i.Status, &i.Reply, &created, &resolved)
	if errors.Is(err, sql.ErrNoRows) {
		return i, ErrNotFound
	}
	i.CreatedAt = time.Unix(created, 0).UTC()
	if resolved > 0 {
		i.ResolvedAt = time.Unix(resolved, 0).UTC()
	}
	return i, err
}

func (s *Store) AddIssue(ctx context.Context, i Issue) (Issue, error) {
	id, err := s.insert(ctx, `INSERT INTO issues (member_id, kind, tmdb_id, title, year, poster, season, episode, problem, message, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, i.MemberID, i.Kind, i.TMDBID, i.Title, i.Year, i.Poster, i.Season, i.Episode, i.Problem, i.Message, IssueOpen, now())
	if err != nil {
		return i, err
	}
	return s.Issue(ctx, id)
}

func (s *Store) Issue(ctx context.Context, id int64) (Issue, error) {
	return scanIssue(s.row(ctx, "SELECT "+issueCols+" FROM issues i JOIN members m ON m.id = i.member_id WHERE i.id = ?", id))
}

func (s *Store) issues(ctx context.Context, where string, args ...any) ([]Issue, error) {
	rows, err := s.query(ctx, "SELECT "+issueCols+" FROM issues i JOIN members m ON m.id = i.member_id WHERE "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Issue{}
	for rows.Next() {
		i, err := scanIssue(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// Issues lists problem reports, newest first: those with status, or all
// when status is empty.
func (s *Store) Issues(ctx context.Context, status string, limit int) ([]Issue, error) {
	if status == "" {
		return s.issues(ctx, "1 = 1 ORDER BY i.created_at DESC, i.id DESC LIMIT ?", limit)
	}
	return s.issues(ctx, "i.status = ? ORDER BY i.created_at DESC, i.id DESC LIMIT ?", status, limit)
}

// MemberIssues is what one person reported, newest first.
func (s *Store) MemberIssues(ctx context.Context, memberID int64) ([]Issue, error) {
	return s.issues(ctx, "i.member_id = ? ORDER BY i.created_at DESC, i.id DESC LIMIT 500", memberID)
}

// ResolveIssue marks a report fixed, or open again, with a reply.
func (s *Store) ResolveIssue(ctx context.Context, id int64, resolved bool, reply string) error {
	status, at := IssueOpen, int64(0)
	if resolved {
		status, at = IssueResolved, now()
	}
	res, err := s.exec(ctx, "UPDATE issues SET status = ?, reply = ?, resolved_at = ? WHERE id = ?", status, reply, at, id)
	if err != nil {
		return err
	}
	return affected(res)
}
