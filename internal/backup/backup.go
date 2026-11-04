// Package backup keeps copies of Box Office's database: on a schedule,
// before a migration or a restore, and whenever an admin asks. Each copy is
// kept on the server's own disk and, when an admin sets one up, sent to a
// second place too: another folder (such as a NAS share) or S3-compatible
// storage (AWS S3, Backblaze B2, Cloudflare R2, MinIO…). A passphrase, when
// set, encrypts them.
//
// A backup is a portable dump of the tables, not a database file, so the
// same one restores into SQLite or PostgreSQL.
package backup

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
)

// Why a backup was made.
const (
	ReasonScheduled     = "scheduled"
	ReasonManual        = "manual"
	ReasonBeforeMigrate = "before-migration"
	ReasonBeforeRestore = "before-restore"
)

// Settings say how often backups are made, how many are kept, and where
// else they go.
type Settings struct {
	// EveryHours between scheduled backups; 0 turns them off.
	EveryHours int `json:"everyHours"`
	// Keep is how many scheduled backups are kept in each place.
	Keep int `json:"keep"`
	// Passphrase encrypts backups when set. Without it a backup can't be
	// restored, so keep it somewhere safe.
	Passphrase string `json:"passphrase,omitempty"`
	// Remote is the second place backups go; Kind "" for none.
	Remote Remote `json:"remote"`
}

// Remote is where else backups go.
type Remote struct {
	Kind string `json:"kind"` // "", folder or s3
	// Folder is a path on this server, for kind folder.
	Folder string `json:"folder,omitempty"`
	S3     S3     `json:"s3,omitzero"`
}

// DefaultSettings: a backup a day, two weeks of them, only on this disk.
var DefaultSettings = Settings{EveryHours: 24, Keep: 14}

// SettingKey is where the settings are kept.
const SettingKey = "backups"

// secretSettings are left out of backups: what it takes to reach the
// backups shouldn't sit inside them.
var secretSettings = []string{SettingKey}

// Normalize keeps the settings in range.
func (s *Settings) Normalize() {
	s.EveryHours = min(max(s.EveryHours, 0), 24*30)
	s.Keep = min(max(s.Keep, 1), 365)
	s.Remote.Kind = strings.ToLower(strings.TrimSpace(s.Remote.Kind))
}

// Check says what's wrong with the settings, if anything.
func (s Settings) Check() error {
	switch s.Remote.Kind {
	case "":
	case "folder":
		if !filepath.IsAbs(s.Remote.Folder) {
			return errors.New("enter the folder's full path, such as /mnt/nas/backups")
		}
	case "s3":
		return s.Remote.S3.Check()
	default:
		return fmt.Errorf("unknown kind of backup place %q", s.Remote.Kind)
	}
	return nil
}

// Backup is one backup file.
type Backup struct {
	Name      string    `json:"name"`
	Reason    string    `json:"reason"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"createdAt"`
	Encrypted bool      `json:"encrypted"`
	// Where it is: local, remote, or both.
	Local  bool `json:"local"`
	Remote bool `json:"remote"`
}

// Manager makes, lists, prunes and restores backups.
type Manager struct {
	Store *store.Store
	Dir   string // on this server's disk
	Log   *slog.Logger
	now   func() time.Time
	mu    sync.Mutex
}

func (m *Manager) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

func (m *Manager) Settings(ctx context.Context) (Settings, error) {
	s := DefaultSettings
	v, err := m.Store.Setting(ctx, SettingKey)
	if err != nil || v == "" {
		return s, err
	}
	err = json.Unmarshal([]byte(v), &s)
	s.Normalize()
	return s, err
}

func (m *Manager) SaveSettings(ctx context.Context, s Settings) error {
	s.Normalize()
	if err := s.Check(); err != nil {
		return err
	}
	b, _ := json.Marshal(s)
	return m.Store.SetSetting(ctx, SettingKey, string(b))
}

// TestRemote writes a small file to the remote place, reads it back and
// removes it, to show an admin the settings work before relying on them.
func (m *Manager) TestRemote(ctx context.Context, s Settings) error {
	s.Normalize()
	if err := s.Check(); err != nil {
		return err
	}
	r, err := remoteFor(s.Remote)
	if err != nil || r == nil {
		return err
	}
	name := fmt.Sprintf("boxoffice-test-%d.txt", m.clock().UnixNano())
	want := []byte("Box Office can write here.")
	if err := r.Put(ctx, name, bytes.NewReader(want), int64(len(want))); err != nil {
		return fmt.Errorf("couldn't write: %w", err)
	}
	rc, err := r.Get(ctx, name)
	if err != nil {
		return fmt.Errorf("wrote, but couldn't read back: %w", err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	r.Delete(ctx, name)
	if !bytes.Equal(got, want) {
		return errors.New("wrote, but read back something else")
	}
	return nil
}

var nameRE = regexp.MustCompile(`^boxoffice-(\d{8}-\d{6})(?:-(\d+))?-([a-z-]+)\.json\.gz(\.enc)?$`)

func parseName(name string) (Backup, bool) {
	sm := nameRE.FindStringSubmatch(name)
	if sm == nil {
		return Backup{}, false
	}
	t, err := time.Parse("20060102-150405", sm[1])
	if err != nil {
		return Backup{}, false
	}
	return Backup{Name: name, Reason: sm[3], CreatedAt: t, Encrypted: sm[4] != ""}, true
}

// ValidName says whether name is a backup's file name, which also keeps
// names from reaching outside the folder.
func ValidName(name string) bool { _, ok := parseName(name); return ok }

// Make makes a backup now, keeps it on this disk, and sends it to the
// remote place if there is one. A failed upload is reported, but the local
// copy stays.
func (m *Manager) Make(ctx context.Context, reason string) (Backup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.Settings(ctx)
	if err != nil {
		return Backup{}, err
	}
	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		return Backup{}, err
	}
	at := m.clock().UTC()
	base := "boxoffice-" + at.Format("20060102-150405")
	ext := ".json.gz"
	if s.Passphrase != "" {
		ext += ".enc"
	}
	name := base + "-" + reason + ext
	for i := 2; fileExists(filepath.Join(m.Dir, name)); i++ {
		name = fmt.Sprintf("%s-%d-%s%s", base, i, reason, ext)
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if err := m.Store.Dump(ctx, zw, secretSettings...); err != nil {
		return Backup{}, err
	}
	if err := zw.Close(); err != nil {
		return Backup{}, err
	}
	data := buf.Bytes()
	if s.Passphrase != "" {
		if data, err = encrypt(data, s.Passphrase); err != nil {
			return Backup{}, err
		}
	}
	tmp := filepath.Join(m.Dir, "."+name+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return Backup{}, err
	}
	if err := os.Rename(tmp, filepath.Join(m.Dir, name)); err != nil {
		os.Remove(tmp)
		return Backup{}, err
	}
	b := Backup{Name: name, Reason: reason, Size: int64(len(data)), CreatedAt: at, Encrypted: s.Passphrase != "", Local: true}
	m.Log.Info("backup made", "name", name, "size", b.Size)
	m.prune(ctx, localStore{m.Dir}, s.Keep)
	if r, err := remoteFor(s.Remote); err != nil {
		return b, err
	} else if r != nil {
		if err := r.Put(ctx, name, bytes.NewReader(data), int64(len(data))); err != nil {
			return b, fmt.Errorf("the backup is on this server, but sending it to %s failed: %w", s.Remote.Kind, err)
		}
		b.Remote = true
		m.prune(ctx, r, s.Keep)
	}
	return b, nil
}

func fileExists(p string) bool { _, err := os.Lstat(p); return err == nil }

// prune keeps the newest keep scheduled backups in a place. Backups made
// for other reasons are kept twice as long, counted on their own.
func (m *Manager) prune(ctx context.Context, p place, keep int) {
	names, err := p.List(ctx)
	if err != nil {
		m.Log.Warn("couldn't list backups to prune", "err", err)
		return
	}
	byReason := map[string][]Backup{}
	for _, n := range names {
		if b, ok := parseName(n); ok {
			byReason[b.Reason] = append(byReason[b.Reason], b)
		}
	}
	for reason, list := range byReason {
		limit := keep
		if reason != ReasonScheduled {
			limit = keep * 2
		}
		slices.SortFunc(list, func(a, b Backup) int { return b.CreatedAt.Compare(a.CreatedAt) })
		for _, b := range list[min(limit, len(list)):] {
			if err := p.Delete(ctx, b.Name); err != nil {
				m.Log.Warn("couldn't delete an old backup", "name", b.Name, "err", err)
			}
		}
	}
}

// List lists backups on this disk and in the remote place, newest first.
func (m *Manager) List(ctx context.Context) ([]Backup, error) {
	out := map[string]*Backup{}
	entries, err := os.ReadDir(m.Dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, e := range entries {
		b, ok := parseName(e.Name())
		if !ok {
			continue
		}
		if info, err := e.Info(); err == nil {
			b.Size = info.Size()
		}
		b.Local = true
		out[b.Name] = &b
	}
	s, err := m.Settings(ctx)
	if err != nil {
		return nil, err
	}
	var remoteErr error
	if r, err := remoteFor(s.Remote); err == nil && r != nil {
		names, err := r.List(ctx)
		remoteErr = err
		for _, n := range names {
			if b, ok := out[n]; ok {
				b.Remote = true
			} else if nb, ok := parseName(n); ok {
				nb.Remote = true
				out[n] = &nb
			}
		}
	}
	list := make([]Backup, 0, len(out))
	for _, b := range out {
		list = append(list, *b)
	}
	slices.SortFunc(list, func(a, b Backup) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return list, remoteErr
}

// Open reads a backup, from this disk if it's here, else from the remote place.
func (m *Manager) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	if !ValidName(name) {
		return nil, os.ErrNotExist
	}
	if f, err := os.Open(filepath.Join(m.Dir, name)); err == nil {
		return f, nil
	}
	s, err := m.Settings(ctx)
	if err != nil {
		return nil, err
	}
	r, err := remoteFor(s.Remote)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, os.ErrNotExist
	}
	return r.Get(ctx, name)
}

// Restore replaces the database with a backup, after backing up what's
// there now. Backup settings stay as they are.
func (m *Manager) Restore(ctx context.Context, name string) error {
	rc, err := m.Open(ctx, name)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(rc, 2<<30))
	rc.Close()
	if err != nil {
		return err
	}
	return m.RestoreData(ctx, data)
}

// RestoreData restores a backup an admin uploaded.
func (m *Manager) RestoreData(ctx context.Context, data []byte) error {
	s, err := m.Settings(ctx)
	if err != nil {
		return err
	}
	if isEncrypted(data) {
		if s.Passphrase == "" {
			return errors.New("this backup is encrypted; enter its passphrase under Backups first")
		}
		if data, err = decrypt(data, s.Passphrase); err != nil {
			return err
		}
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return store.ErrNotBackup
	}
	plain, err := io.ReadAll(io.LimitReader(zr, 8<<30))
	if err != nil {
		return store.ErrNotBackup
	}
	// Check it reads as a backup before touching anything.
	if !bytes.Contains(plain[:min(len(plain), 200)], []byte(store.DumpFormat)) {
		return store.ErrNotBackup
	}
	if _, err := m.Make(ctx, ReasonBeforeRestore); err != nil {
		return fmt.Errorf("didn't restore, because backing up the current database first failed: %w", err)
	}
	if err := m.Store.Restore(ctx, bytes.NewReader(plain), secretSettings...); err != nil {
		return err
	}
	m.Log.Info("backup restored")
	return nil
}

// Run makes scheduled backups until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	for {
		m.runDue(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Minute):
		}
	}
}

func (m *Manager) runDue(ctx context.Context) {
	s, err := m.Settings(ctx)
	if err != nil || s.EveryHours == 0 {
		return
	}
	list, _ := m.List(ctx)
	for _, b := range list {
		if b.Reason == ReasonScheduled && b.Local {
			if m.clock().Sub(b.CreatedAt) < time.Duration(s.EveryHours)*time.Hour {
				return
			}
			break
		}
	}
	if _, err := m.Make(ctx, ReasonScheduled); err != nil {
		m.Log.Warn("scheduled backup failed", "err", err)
	}
}

// place is somewhere backups are kept.
type place interface {
	List(ctx context.Context) ([]string, error)
	Put(ctx context.Context, name string, r io.Reader, size int64) error
	Get(ctx context.Context, name string) (io.ReadCloser, error)
	Delete(ctx context.Context, name string) error
}

func remoteFor(r Remote) (place, error) {
	switch r.Kind {
	case "":
		return nil, nil
	case "folder":
		return localStore{r.Folder}, nil
	case "s3":
		return newS3(r.S3)
	}
	return nil, fmt.Errorf("unknown kind of backup place %q", r.Kind)
}

// localStore is a folder.
type localStore struct{ dir string }

func (l localStore) List(ctx context.Context) ([]string, error) {
	entries, err := os.ReadDir(l.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out, err
}

func (l localStore) Put(ctx context.Context, name string, r io.Reader, size int64) error {
	if err := os.MkdirAll(l.dir, 0o700); err != nil {
		return err
	}
	tmp := filepath.Join(l.dir, "."+name+".tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, filepath.Join(l.dir, name))
}

func (l localStore) Get(ctx context.Context, name string) (io.ReadCloser, error) {
	return os.Open(filepath.Join(l.dir, filepath.Base(name)))
}

func (l localStore) Delete(ctx context.Context, name string) error {
	return os.Remove(filepath.Join(l.dir, filepath.Base(name)))
}

// KeepFile saves a file that isn't one of Box Office's own backups, such as
// a backup downloaded from Radarr before a migration, next to the backups
// on this disk and in the remote place. It's never pruned.
func (m *Manager) KeepFile(ctx context.Context, name string, data []byte) (string, error) {
	s, err := m.Settings(ctx)
	if err != nil {
		return "", err
	}
	// Another app's backup holds its passwords and keys, so it's kept
	// encrypted like Box Office's own when there's a passphrase.
	if s.Passphrase != "" {
		if data, err = encrypt(data, s.Passphrase); err != nil {
			return "", err
		}
		name += ".enc"
	}
	dir := filepath.Join(m.Dir, "imported")
	if err := (localStore{dir}).Put(ctx, name, bytes.NewReader(data), int64(len(data))); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	if r, err := remoteFor(s.Remote); err != nil {
		return path, err
	} else if r != nil {
		if err := r.Put(ctx, name, bytes.NewReader(data), int64(len(data))); err != nil {
			return path, fmt.Errorf("kept on this server, but sending it to %s failed: %w", s.Remote.Kind, err)
		}
	}
	return path, nil
}
