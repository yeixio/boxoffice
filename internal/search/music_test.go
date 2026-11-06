package search

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/mediatypes"
	"github.com/yeixio/boxoffice/internal/store"
)

func albumSetup(t *testing.T, srvURL string) (*Service, *store.Store, store.Artist, store.Album) {
	s, st, _ := setup(t, srvURL)
	ctx := context.Background()
	root := t.TempDir()
	st.AddRootFolder(ctx, mediatypes.Music, root)
	a, err := st.AddArtist(ctx, store.Artist{MBID: "a74b1b7f-71a5-4011-9441-d0b5e4122711", Name: "Radiohead", Monitored: true,
		RootFolder: root, Path: filepath.Join(root, "Radiohead")})
	if err != nil {
		t.Fatal(err)
	}
	st.UpsertAlbum(ctx, store.Album{ArtistID: a.ID, MBID: "b1392450-e666-3926-a536-22c65f834433", Title: "OK Computer", Type: "Album", Released: "1997-05-21", Monitored: true})
	st.UpsertAlbum(ctx, store.Album{ArtistID: a.ID, MBID: "c1392450-e666-3926-a536-22c65f834433", Title: "Kid A", Type: "Album", Released: "2000-10-02", Monitored: true})
	st.UpsertAlbum(ctx, store.Album{ArtistID: a.ID, MBID: "d1392450-e666-3926-a536-22c65f834433", Title: "Not Out", Type: "Album", Released: "2999-01-01", Monitored: true})
	bs, _ := st.Albums(ctx, a.ID)
	for _, b := range bs {
		if b.Title == "OK Computer" {
			return s, st, a, b
		}
	}
	t.Fatal("no album")
	return nil, nil, a, store.Album{}
}

func TestAlbumSearch(t *testing.T) {
	srv, asked := indexer(t,
		bookItem("Radiohead - OK Computer (1997) [MP3 320]", "mp3", 120e6, 3010),
		bookItem("Radiohead-OK_Computer-(1997)-FLAC", "flac", 450e6, 3040),
		bookItem("Radiohead - OK Computer OKNOTOK 1997 2017 (2017) FLAC", "reissue", 900e6, 3040),
		bookItem("Radiohead - Discography 1993-2016 FLAC", "disco", 5e9, 3040),
		bookItem("Radiohead - Kid A (2000) FLAC", "other", 400e6, 3040),
		bookItem("Radiohead - OK Computer - Live 1080p BluRay x264", "video", 4e9, 3020),
		bookItem("Radiohead - OK Computer - Airbag (single) MP3", "track", 1e6, 3010),
	)
	s, st, _, b := albumSetup(t, srv.URL)
	ctx := context.Background()

	if _, err := s.Album(ctx, b.ID); err != ErrMusicOff {
		t.Fatalf("searched with music off: %v", err)
	}
	mediatypes.SetEnabled(ctx, st, mediatypes.Music, true)
	res, err := s.Album(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	q := (*asked)[len(*asked)-1]
	if !strings.Contains(q, "t=search") || !strings.Contains(q, "cat=3000") || !strings.Contains(q, "q=radiohead+ok+computer") {
		t.Fatalf("asked %s", q)
	}
	order := []string{}
	for _, c := range res.Releases {
		if c.Accepted {
			order = append(order, c.GUID)
		}
	}
	// FLAC first, then MP3; the reissue from another year comes after.
	if strings.Join(order, ",") != "flac,mp3,reissue" && strings.Join(order, ",") != "flac,reissue,mp3" {
		t.Fatalf("accepted %v", order)
	}
	if res.Releases[0].GUID != "flac" || res.Releases[0].Format != "FLAC" {
		t.Fatalf("best %+v", res.Releases[0])
	}
	for _, c := range res.Releases {
		if (c.GUID == "disco" || c.GUID == "other" || c.GUID == "video" || c.GUID == "track") && c.Accepted {
			t.Errorf("%s accepted", c.Title)
		}
	}

	// MP3 only takes the MP3.
	SetMusicQuality(ctx, st, MusicMP3)
	res, _ = s.Album(ctx, b.ID)
	if got := res.Releases[0]; got.GUID != "mp3" || !got.Accepted {
		t.Fatalf("best for MP3 only %+v", got)
	}
	for _, c := range res.Releases {
		if c.Format == "FLAC" && c.Accepted {
			t.Errorf("FLAC accepted for MP3 only: %s", c.Title)
		}
	}
	// FLAC only refuses the MP3.
	SetMusicQuality(ctx, st, MusicFLAC)
	res, _ = s.Album(ctx, b.ID)
	for _, c := range res.Releases {
		if c.GUID == "mp3" && c.Accepted {
			t.Error("MP3 accepted for FLAC only")
		}
	}

	// The job grabs the best, once downloading is on.
	downloader.SaveSettings(ctx, st, downloader.Settings{Enabled: true})
	r, err := s.jobAlbum(ctx, AlbumJob(b.ID))
	if err != nil || r.Status != "done" || !strings.Contains(r.Message, "grabbed Radiohead-OK_Computer-(1997)-FLAC") {
		t.Fatalf("job %+v %v", r, err)
	}
	pending, _ := st.Pending(ctx, store.AlbumKind, b.ID)
	if len(pending) != 1 || pending[0].Kind != store.AlbumKind {
		t.Fatalf("pending %+v", pending)
	}
	if r, _ := s.jobAlbum(ctx, AlbumJob(b.ID)); r.Status != "skipped" {
		t.Fatalf("grabbed twice: %+v", r)
	}
}

func TestWantedAlbums(t *testing.T) {
	srv, _ := indexer(t)
	s, st, a, b := albumSetup(t, srv.URL)
	ctx := context.Background()
	count := func() (n int, subjects []string) {
		list, err := s.WantedList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range list {
			if w.Kind == store.AlbumKind {
				n++
				subjects = append(subjects, w.Subject)
			}
		}
		return
	}
	if n, _ := count(); n != 0 {
		t.Fatal("albums wanted while music is off")
	}
	mediatypes.SetEnabled(ctx, st, mediatypes.Music, true)
	if n, list := count(); n != 2 {
		t.Fatalf("wanted %v", list) // not the one that isn't out
	}
	// An album in the artist's folder already, as Lidarr left it, isn't
	// wanted any more.
	os.MkdirAll(filepath.Join(a.Path, "OK Computer (1997)"), 0o755)
	os.WriteFile(filepath.Join(a.Path, "OK Computer (1997)", "01 - Airbag.flac"), make([]byte, 100), 0o644)
	if n, list := count(); n != 1 || list[0] != "Kid A by Radiohead" {
		t.Fatalf("wanted %v", list)
	}
	b, _ = st.Album(ctx, b.ID)
	if b.File == nil || b.File.Path != filepath.Join(a.Path, "OK Computer (1997)") || b.File.Size != 100 {
		t.Fatalf("album %+v", b)
	}
}

func TestAlbumMatches(t *testing.T) {
	for _, c := range []struct {
		release, artist, title string
		ok                     bool
	}{
		{"Radiohead-OK_Computer-(1997)-FLAC", "Radiohead", "OK Computer", true},
		{"Radiohead - OK Computer OKNOTOK 1997 2017", "Radiohead", "OK Computer", true},
		{"Radiohead - Kid A (2000)", "Radiohead", "OK Computer", false},
		{"OK Computer - Radiohead", "Radiohead", "OK Computer", false},
		{"Weezer - Weezer (Blue Album) FLAC", "Weezer", "Weezer", true},
		{"Weezer - Pinkerton FLAC", "Weezer", "Weezer", false},
		{"The Beatles - Abbey Road (2019 Remaster) FLAC", "The Beatles", "Abbey Road", true},
		{"Beatles - Abbey Road MP3", "The Beatles", "Abbey Road", true},
		{"Pink Floyd - The Dark Side of the Moon [FLAC]", "Pink Floyd", "The Dark Side of the Moon", true},
		{"Someone - The Dark Side of the Moon [FLAC]", "Pink Floyd", "The Dark Side of the Moon", false},
	} {
		if ok, why := albumMatches(c.release, c.artist, c.title); ok != c.ok {
			t.Errorf("%q for %q by %s: %v (%s)", c.release, c.title, c.artist, ok, why)
		}
	}
}
