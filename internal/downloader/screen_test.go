package downloader

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/usenet/nntp/nntptest"
	"github.com/yeixio/boxoffice/internal/usenet/yenc"
)

// postFiles posts files, by name as posted, and returns their NZB.
func postFiles(srv *nntptest.Server, title string, files [][2]any) []byte {
	var x bytes.Buffer
	fmt.Fprintf(&x, `<?xml version="1.0"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"><head><meta type="title">%s</meta></head>`, title)
	for fi, f := range files {
		name, data := f[0].(string), f[1].([]byte)
		const chunk = 4000
		parts := (len(data) + chunk - 1) / chunk
		fmt.Fprintf(&x, `<file poster="t" date="1" subject="%s [%d/%d] - &quot;%s&quot; yEnc (1/%d)"><groups><group>a.b.test</group></groups><segments>`, title, fi+1, len(files), name, parts)
		for i := range parts {
			start, end := i*chunk, min((i+1)*chunk, len(data))
			id := fmt.Sprintf("%s.%d.%d@test", title, fi, i)
			body := yenc.Encode(name, i+1, parts, int64(start), int64(len(data)), data[start:end])
			srv.Add(id, body)
			fmt.Fprintf(&x, `<segment bytes="%d" number="%d">%s</segment>`, len(body), i+1, id)
		}
		x.WriteString(`</segments></file>`)
	}
	x.WriteString(`</nzb>`)
	return x.Bytes()
}

// A release posted the way Mike's were: RAR volumes under hashes, the
// first a .rar and the rest not named .r00, .r01…
func TestDownloadObfuscatedRAR(t *testing.T) {
	srv := nntptest.New()
	defer srv.Close()
	var files [][2]any
	for i, to := range []string{"39f5d2cb5d2e2f132f5f3091c5087f1c.rar", "77aa01bc", "e1e1e1e1e1.rar"} {
		b, err := os.ReadFile(filepath.Join("..", "usenet", "unpack", "testdata", fmt.Sprintf("multi.part%d.rar", i+1)))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, [2]any{to, b})
	}
	e, st := newEngine(t, srv)
	enable(t, st, Settings{})
	run(t, e)
	d, err := e.Add(context.Background(), store.Download{Source: "upload"}, postFiles(srv, "Amerikatsi.2022", files))
	if err != nil {
		t.Fatal(err)
	}
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	if d.Status != store.DownloadCompleted {
		t.Fatalf("failed: %s", d.Message)
	}
	left := listFiles(filepath.Join(e.Dir, d.Folder))
	if len(left) != 2 || left[0] != "movie.mkv" {
		t.Fatalf("left %v", left)
	}
}

func windowsProgram() []byte {
	b := make([]byte, 600)
	b[0], b[1] = 'M', 'Z'
	binary.LittleEndian.PutUint32(b[0x3c:], 0x80)
	copy(b[0x80:], "PE\x00\x00")
	return b
}

func TestDownloadQuarantined(t *testing.T) {
	srv := nntptest.New()
	defer srv.Close()
	e, st := newEngine(t, srv)
	enable(t, st, Settings{Import: true})
	failed := make(chan string, 1)
	e.Failed = func(_ context.Context, _ store.Download, why string) { failed <- why }
	run(t, e)
	nzb := postFiles(srv, "Some.Movie.2024", [][2]any{{"Some.Movie.2024.mkv.exe", windowsProgram()}, {"readme.txt", []byte("codec needed")}})
	d, err := e.Add(context.Background(), store.Download{Source: "search", Kind: "movie", TitleID: 1}, nzb)
	if err != nil {
		t.Fatal(err)
	}
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	if d.Status != store.DownloadFailed || !strings.Contains(d.Message, "pretending to be a video") {
		t.Fatalf("%s: %s", d.Status, d.Message)
	}
	if why := <-failed; !strings.HasPrefix(why, "Quarantined") {
		t.Fatalf("failed hook heard %q", why)
	}
	if d.Quarantined != filepath.Join(e.Dir, QuarantineDir, d.Folder) {
		t.Fatalf("quarantined at %q", d.Quarantined)
	}
	info, err := os.Stat(filepath.Join(d.Quarantined, "Some.Movie.2024.mkv.exe"))
	if err != nil || info.Mode().Perm()&0o111 != 0 {
		t.Fatalf("quarantined file: %v %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(e.Dir, d.Folder)); !os.IsNotExist(err) {
		t.Fatal("still in the downloads folder")
	}
	// Removing it with its files leaves quarantine alone.
	if err := e.Remove(context.Background(), d.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(d.Quarantined); err != nil {
		t.Fatal("quarantine was deleted")
	}
}

func TestDownloadNoVideo(t *testing.T) {
	srv := nntptest.New()
	defer srv.Close()
	e, st := newEngine(t, srv)
	enable(t, st, Settings{})
	run(t, e)
	nzb := postFiles(srv, "Other.Movie.2024", [][2]any{{"other.movie.2024.nfo", []byte("visit our site")}})
	d, err := e.Add(context.Background(), store.Download{Source: "search", Kind: "movie", TitleID: 1}, nzb)
	if err != nil {
		t.Fatal(err)
	}
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	if d.Status != store.DownloadFailed || !strings.Contains(d.Message, "no video") || d.Quarantined == "" {
		t.Fatalf("%s: %s", d.Status, d.Message)
	}
}

type flakyImporter struct{ fail bool }

func (f *flakyImporter) Import(_ context.Context, _ store.Download, _ string, _ []string) (string, error) {
	if f.fail {
		return "", errors.New("the library is offline")
	}
	return "/library/Amerikatsi (2022)", nil
}

func obfuscatedSet(t *testing.T) [][2]any {
	var files [][2]any
	for i, to := range []string{"39f5d2cb5d2e2f132f5f3091c5087f1c.rar", "77aa01bc", "e1e1e1e1e1.rar"} {
		b, err := os.ReadFile(filepath.Join("..", "usenet", "unpack", "testdata", fmt.Sprintf("multi.part%d.rar", i+1)))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, [2]any{to, b})
	}
	return files
}

// smallMedia lets the test files, a few KB, pass for a movie.
func smallMedia(t *testing.T) {
	m, ep := minMovieSize, minEpSize
	minMovieSize, minEpSize = 1, 1
	t.Cleanup(func() { minMovieSize, minEpSize = m, ep })
}

// A download whose unpacking fails can be unpacked again without
// downloading it again.
func TestReprocess(t *testing.T) {
	smallMedia(t)
	srv := nntptest.New()
	defer srv.Close()
	locked, err := os.ReadFile(filepath.Join("..", "usenet", "unpack", "testdata", "locked.rar"))
	if err != nil {
		t.Fatal(err)
	}
	e, st := newEngine(t, srv)
	enable(t, st, Settings{})
	run(t, e)
	ctx := context.Background()
	d, err := e.Add(ctx, store.Download{Source: "upload"}, postFiles(srv, "Locked", [][2]any{{"a1b2c3.rar", locked}}))
	if err != nil {
		t.Fatal(err)
	}
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	if d.Status != store.DownloadFailed || !e.CanReprocess(d) {
		t.Fatalf("%s: %s (can reprocess: %v)", d.Status, d.Message, e.CanReprocess(d))
	}
	fetched := srv.Fetches.Load()
	if err := e.Reprocess(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	d = waitFor(t, st, d.ID, store.DownloadFailed)
	if !strings.Contains(d.Message, "password") {
		t.Fatalf("message %q", d.Message)
	}
	if n := srv.Fetches.Load(); n != fetched {
		t.Fatalf("fetched %d more articles", n-fetched)
	}
	// Gone files can't be unpacked again.
	os.RemoveAll(filepath.Join(e.Dir, d.Folder))
	if e.CanReprocess(d) {
		t.Fatal("reprocessable with no files")
	}
}

// When moving into the library fails, the download stays unpacked and
// done, the release isn't blocked, and moving it later does only that.
func TestImportLater(t *testing.T) {
	smallMedia(t)
	srv := nntptest.New()
	defer srv.Close()
	e, st := newEngine(t, srv)
	imp := &flakyImporter{fail: true}
	e.Importer = imp
	blocked := false
	e.Failed = func(context.Context, store.Download, string) { blocked = true }
	enable(t, st, Settings{Import: true})
	run(t, e)
	ctx := context.Background()
	d, err := e.Add(ctx, store.Download{Source: "search", Kind: "movie", TitleID: 1}, postFiles(srv, "Amerikatsi.2022", obfuscatedSet(t)))
	if err != nil {
		t.Fatal(err)
	}
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	if d.Status != store.DownloadCompleted || !e.CanImport(d) || !strings.Contains(d.Message, "Move into library") || blocked {
		t.Fatalf("%s: %s (can import: %v, blocked: %v)", d.Status, d.Message, e.CanImport(d), blocked)
	}
	// Unpacked already: only movie.mkv and readme.txt are left.
	if left := listFiles(filepath.Join(e.Dir, d.Folder)); len(left) != 2 {
		t.Fatalf("left %v", left)
	}
	fetched := srv.Fetches.Load()
	imp.fail = false
	if err := e.Import(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for (d.ImportedTo == "" || d.Status != store.DownloadCompleted) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		d, _ = st.Download(ctx, d.ID)
	}
	if d.ImportedTo == "" || d.Status != store.DownloadCompleted || !strings.HasPrefix(d.Message, "Moved into the library") {
		t.Fatalf("%s: %s", d.Status, d.Message)
	}
	if n := srv.Fetches.Load(); n != fetched {
		t.Fatalf("fetched %d more articles", n-fetched)
	}
	// What was left is cleaned up once it's in.
	if _, err := os.Stat(filepath.Join(e.Dir, d.Folder)); !os.IsNotExist(err) {
		t.Fatalf("download folder left: %v", err)
	}
}

// A release whose video is a short clip, not the movie, is quarantined.
func TestTooShort(t *testing.T) {
	srv := nntptest.New()
	defer srv.Close()
	e, st := newEngine(t, srv)
	enable(t, st, Settings{})
	run(t, e)
	ctx := context.Background()
	profiles, err := st.Profiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	m, err := st.AddMovie(ctx, store.Movie{TMDBID: 1, Title: "Amerikatsi", Year: 2022, Runtime: 120, ProfileID: profiles[0].ID, RootFolder: "/m", Path: "/m/Amerikatsi (2022)"})
	if err != nil {
		t.Fatal(err)
	}
	clip, err := os.ReadFile(filepath.Join("..", "usenet", "screen", "testdata", "75s.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := e.Add(ctx, store.Download{Source: "search", Kind: "movie", TitleID: m.ID}, postFiles(srv, "Amerikatsi.2022", [][2]any{{"amerikatsi.2022.mkv", clip}}))
	if err != nil {
		t.Fatal(err)
	}
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	if d.Status != store.DownloadFailed || d.Quarantined == "" || !strings.Contains(d.Message, "runs 1 minute,") {
		t.Fatalf("%s: %s", d.Status, d.Message)
	}
}

// Mike's case: a bundle with the movie and a short clip under hashed
// names. Only the main video, the biggest, has to be full length.
func TestBundleWithClip(t *testing.T) {
	srv := nntptest.New()
	defer srv.Close()
	e, st := newEngine(t, srv)
	enable(t, st, Settings{})
	run(t, e)
	ctx := context.Background()
	profiles, _ := st.Profiles(ctx)
	m, err := st.AddMovie(ctx, store.Movie{TMDBID: 2, Title: "Amerikatsi", Year: 2022, Runtime: 115, ProfileID: profiles[0].ID, RootFolder: "/m", Path: "/m/Amerikatsi (2022)"})
	if err != nil {
		t.Fatal(err)
	}
	read := func(n string) []byte {
		b, err := os.ReadFile(filepath.Join("..", "usenet", "screen", "testdata", n))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	nzb := postFiles(srv, "Amerikatsi.2022.bundle", [][2]any{
		{"27e5be22f26c98f6e42419a29501947a.mkv", read("75s.mkv")},
		{"9b1d0c4f8e2a7b3c6d5e4f3a2b1c0d9e.mkv", read("115m.mkv")},
	})
	d, err := e.Add(ctx, store.Download{Source: "search", Kind: "movie", TitleID: m.ID}, nzb)
	if err != nil {
		t.Fatal(err)
	}
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	if d.Status != store.DownloadCompleted {
		t.Fatalf("%s: %s", d.Status, d.Message)
	}
}

func TestMainVideos(t *testing.T) {
	dir := t.TempDir()
	for name, size := range map[string]int{
		"Show.S01E01.mkv": 900, "Show.S01E02.mkv": 850, "Show.S01E03.mkv": 880,
		"Show.S01E01.sample.mkv": 50, "Extras/Making.Of.mkv": 600, "a1b2c3.mkv": 40,
	} {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755)
		os.WriteFile(filepath.Join(dir, name), make([]byte, size), 0o644)
	}
	files := listFiles(dir)
	var got []string
	for _, v := range mainVideos(dir, files, true) {
		got = append(got, v.name)
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"Show.S01E01.mkv", "Show.S01E02.mkv", "Show.S01E03.mkv"}) {
		t.Fatalf("series: %v", got)
	}
	if v := mainVideos(dir, files, false); len(v) != 1 || v[0].name != "Show.S01E01.mkv" {
		t.Fatalf("movie: %v", v)
	}
	// Only extras: the biggest of them is it.
	if v := mainVideos(dir, []string{"Show.S01E01.sample.mkv", "Extras/Making.Of.mkv"}, false); len(v) != 1 || v[0].name != "Extras/Making.Of.mkv" {
		t.Fatalf("extras only: %v", v)
	}
}
