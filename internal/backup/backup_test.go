package backup

import (
	"context"
	"encoding/xml"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/quality"
	"github.com/yeixio/boxoffice/internal/store/storetest"
)

func newManager(t *testing.T) *Manager {
	t.Helper()
	st := storetest.Open(t)
	return &Manager{Store: st, Dir: t.TempDir(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// fakeS3 is a bucket that checks requests are signed.
type fakeS3 struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AK/") || r.Header.Get("X-Amz-Date") == "" {
		w.WriteHeader(403)
		io.WriteString(w, "<Error><Code>AccessDenied</Code><Message>unsigned</Message></Error>")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key := strings.TrimPrefix(r.URL.Path, "/bucket/")
	switch {
	case r.Method == "GET" && r.URL.Query().Get("list-type") == "2":
		type content struct{ Key string }
		var out struct {
			XMLName  xml.Name `xml:"ListBucketResult"`
			Contents []content
		}
		prefix := r.URL.Query().Get("prefix")
		var keys []string
		for k := range f.objects {
			if strings.HasPrefix(k, prefix) {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			out.Contents = append(out.Contents, content{k})
		}
		xml.NewEncoder(w).Encode(out)
	case r.Method == "PUT":
		b, _ := io.ReadAll(r.Body)
		f.objects[key] = b
	case r.Method == "GET":
		b, ok := f.objects[key]
		if !ok {
			w.WriteHeader(404)
			io.WriteString(w, "<Error><Code>NoSuchKey</Code></Error>")
			return
		}
		w.Write(b)
	case r.Method == "DELETE":
		delete(f.objects, key)
		w.WriteHeader(204)
	}
}

func TestBackupRestoreEncryptedToS3(t *testing.T) {
	m := newManager(t)
	ctx := context.Background()
	bucket := &fakeS3{objects: map[string][]byte{}}
	ts := httptest.NewServer(bucket)
	defer ts.Close()

	s := Settings{EveryHours: 24, Keep: 3, Passphrase: "a long passphrase",
		Remote: Remote{Kind: "s3", S3: S3{Endpoint: ts.URL, Region: "us-east-1", Bucket: "bucket", Prefix: "bo/", AccessKey: "AK", SecretKey: "SK", PathStyle: true}}}
	if err := m.TestRemote(ctx, s); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveSettings(ctx, s); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Store.CreateProfile(ctx, quality.Profile{Name: "Kept", Allowed: []int{10}, Cutoff: 10}); err != nil {
		t.Fatal(err)
	}
	b, err := m.Make(ctx, ReasonManual)
	if err != nil {
		t.Fatal(err)
	}
	if !b.Encrypted || !b.Remote || !strings.HasSuffix(b.Name, ".json.gz.enc") {
		t.Fatalf("backup: %+v", b)
	}
	if _, ok := bucket.objects["bo/"+b.Name]; !ok {
		t.Fatalf("not in the bucket: %v", bucket.objects)
	}
	raw, _ := os.ReadFile(filepath.Join(m.Dir, b.Name))
	if strings.Contains(string(raw), "Kept") || !isEncrypted(raw) {
		t.Fatal("the backup on disk isn't encrypted")
	}

	// Change things, then restore: the change is undone, the backup
	// settings stay, and a backup of the state before the restore is made.
	if _, err := m.Store.CreateProfile(ctx, quality.Profile{Name: "Later", Allowed: []int{10}, Cutoff: 10}); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(m.Dir, b.Name)) // only the bucket has it now
	if err := m.Restore(ctx, b.Name); err != nil {
		t.Fatal(err)
	}
	ps, _ := m.Store.Profiles(ctx)
	names := map[string]bool{}
	for _, p := range ps {
		names[p.Name] = true
	}
	if !names["Kept"] || names["Later"] {
		t.Fatalf("profiles after restore: %v", names)
	}
	if got, _ := m.Settings(ctx); got.Passphrase != s.Passphrase || got.Remote.S3.Bucket != "bucket" {
		t.Fatalf("backup settings after restore: %+v", got)
	}
	list, _ := m.List(ctx)
	reasons := map[string]bool{}
	for _, l := range list {
		reasons[l.Reason] = true
	}
	if !reasons[ReasonBeforeRestore] || !reasons[ReasonManual] {
		t.Fatalf("backups: %+v", list)
	}

	// The wrong passphrase can't read it.
	s.Passphrase = "something else"
	m.SaveSettings(ctx, s)
	if err := m.Restore(ctx, b.Name); err == nil {
		t.Fatal("restored with the wrong passphrase")
	}
}

func TestPruneKeepsTheNewest(t *testing.T) {
	m := newManager(t)
	ctx := context.Background()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return at }
	m.SaveSettings(ctx, Settings{EveryHours: 24, Keep: 2})
	var names []string
	for range 4 {
		b, err := m.Make(ctx, ReasonScheduled)
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, b.Name)
		at = at.Add(24 * time.Hour)
	}
	list, _ := m.List(ctx)
	if len(list) != 2 || list[0].Name != names[3] && list[1].Name != names[3] {
		t.Fatalf("kept %+v", list)
	}
}

func TestKeepFileEncrypts(t *testing.T) {
	m := newManager(t)
	ctx := context.Background()
	m.SaveSettings(ctx, Settings{EveryHours: 24, Keep: 2, Passphrase: "pass phrase"})
	path, err := m.KeepFile(ctx, "radarr-backup.zip", []byte("PK secret stuff"))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.HasSuffix(path, ".zip.enc") || strings.Contains(string(raw), "secret") {
		t.Fatalf("kept %s unencrypted", path)
	}
	plain, err := decrypt(raw, "pass phrase")
	if err != nil || string(plain) != "PK secret stuff" {
		t.Fatalf("decrypt: %q %v", plain, err)
	}
}
