package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/mediatypes"
	"github.com/yeixio/boxoffice/internal/store"
)

// rootKind is the kind of root folder each app's titles go in.
var rootKind = map[string]string{"radarr": "movie", "sonarr": "series", "lidarr": mediatypes.Music}

var errMusicOff = errors.New("turn on Music under Settings first: Lidarr's artists come across into it")

// Lidarr's artists, albums and track files, as its API lists them.
type lArtist struct {
	ID              int    `json:"id"`
	ArtistName      string `json:"artistName"`
	ForeignArtistID string `json:"foreignArtistId"` // MusicBrainz id
	SortName        string `json:"sortName"`
	ArtistType      string `json:"artistType"`
	Disambiguation  string `json:"disambiguation"`
	Path            string `json:"path"`
	RootFolderPath  string `json:"rootFolderPath"`
	Monitored       bool   `json:"monitored"`
	Statistics      struct {
		TrackFileCount int `json:"trackFileCount"`
	} `json:"statistics"`
}

type lAlbum struct {
	ID             int         `json:"id"`
	ArtistID       int         `json:"artistId"`
	Title          string      `json:"title"`
	ForeignAlbumID string      `json:"foreignAlbumId"` // MusicBrainz release group id
	AlbumType      string      `json:"albumType"`
	SecondaryTypes lAlbumTypes `json:"secondaryTypes"`
	ReleaseDate    string      `json:"releaseDate"`
	Monitored      bool        `json:"monitored"`
}

// lAlbumTypes are an album's secondary types. Lidarr lists them as
// {"id", "name"}; plain names are read too. "Studio" means none.
type lAlbumTypes []string

func (t *lAlbumTypes) UnmarshalJSON(b []byte) error {
	var names []string
	if json.Unmarshal(b, &names) != nil {
		var objs []struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(b, &objs); err != nil {
			return err
		}
		for _, o := range objs {
			names = append(names, o.Name)
		}
	}
	*t = nil
	for _, n := range names {
		if n != "" && n != "Studio" {
			*t = append(*t, n)
		}
	}
	return nil
}

type lTrackFile struct {
	AlbumID int    `json:"albumId"`
	Path    string `json:"path"`
	Size    int64  `json:"size"`
}

// albumTypes are the kinds of album Box Office can watch an artist for.
var albumTypes = []string{"Album", "EP", "Single", "Live", "Compilation", "Soundtrack", "Remix", "Other"}

// albumKind is which of albumTypes an album counts as.
func albumKind(al lAlbum) string {
	for _, t := range al.SecondaryTypes {
		if slices.Contains(albumTypes, t) {
			return t
		}
	}
	if len(al.SecondaryTypes) == 0 && slices.Contains([]string{"Album", "EP", "Single"}, al.AlbumType) {
		return al.AlbumType
	}
	return "Other"
}

// watchedTypes are the kinds of album an artist's watched-for albums in
// Lidarr are, so Box Office watches for the same kinds; studio albums
// when none are.
func watchedTypes(albums []lAlbum) []string {
	var out []string
	for _, t := range albumTypes {
		if slices.ContainsFunc(albums, func(al lAlbum) bool { return al.Monitored && albumKind(al) == t }) {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return []string{"Album"}
	}
	return out
}

func (p *pending) artistAlbums(id int) []lAlbum {
	var out []lAlbum
	for _, al := range p.albums {
		if al.ArtistID == id {
			out = append(out, al)
		}
	}
	return out
}

func (m *Migrator) previewArtists(ctx context.Context, p *pending, plan *Plan, sample func(Sample)) {
	noMBID := 0
	for _, a := range p.artists {
		plan.Titles.Total++
		if a.ForeignArtistID == "" {
			plan.Titles.Skipped++
			noMBID++
			continue
		}
		if _, err := m.Catalog.Store.ArtistByMBID(ctx, a.ForeignArtistID); err == nil {
			plan.Titles.Already++
			continue
		}
		plan.Titles.New++
		sample(Sample{Title: a.ArtistName, Path: mapPath(p.req.PathMap, a.Path), HasFile: a.Statistics.TrackFileCount > 0})
	}
	if noMBID > 0 {
		plan.Warnings = append(plan.Warnings, count(noMBID, "artist has", "artists have")+" no MusicBrainz id in Lidarr and will be left out.")
	}
}

// importArtists puts Lidarr's artists on the list with their albums, and
// records the albums it has as in the library, where they are. Nothing on
// disk is moved, renamed or deleted.
func (m *Migrator) importArtists(ctx context.Context, p *pending, pr *Progress, progress func(int, int)) error {
	st := m.Catalog.Store
	warn := func(s string) { m.update(func() { pr.Warnings = append(pr.Warnings, s) }) }
	p.albumIDs = map[int]int64{}
	for i, la := range p.artists {
		progress(i, len(p.artists))
		if err := ctx.Err(); err != nil {
			return err
		}
		if la.ForeignArtistID == "" {
			m.update(func() { pr.Imported.Skipped++ })
			warn(la.ArtistName + " has no MusicBrainz id in Lidarr, so it was left out")
			continue
		}
		albums := p.artistAlbums(la.ID)
		a, err := st.ArtistByMBID(ctx, la.ForeignArtistID)
		isNew := false
		if errors.Is(err, store.ErrNotFound) {
			root := rootOf(p.req.PathMap, la.RootFolderPath, la.Path)
			m.ensureFolder(ctx, mediatypes.Music, root)
			a, err = st.AddArtist(ctx, store.Artist{MBID: la.ForeignArtistID, Name: la.ArtistName, SortName: la.SortName, Type: la.ArtistType,
				Disambiguation: la.Disambiguation, Monitored: la.Monitored, AlbumTypes: watchedTypes(albums),
				RootFolder: root, Path: mapPath(p.req.PathMap, la.Path)})
			isNew = err == nil
		}
		if err != nil {
			m.update(func() { pr.Imported.Skipped++ })
			warn(fmt.Sprintf("%s: %v", la.ArtistName, err))
			continue
		}
		for _, al := range albums {
			if al.ForeignAlbumID == "" {
				continue
			}
			released := al.ReleaseDate
			if len(released) > 10 {
				released = released[:10]
			}
			if _, err := st.UpsertAlbum(ctx, store.Album{ArtistID: a.ID, MBID: al.ForeignAlbumID, Title: al.Title, Type: al.AlbumType,
				SecondaryTypes: al.SecondaryTypes, Released: released, Monitored: al.Monitored}); err != nil {
				return err
			}
		}
		have, err := st.Albums(ctx, a.ID)
		if err != nil {
			return err
		}
		byMBID := map[string]store.Album{}
		for _, b := range have {
			byMBID[b.MBID] = b
		}
		for _, al := range albums {
			if b, ok := byMBID[normMBID(al.ForeignAlbumID)]; ok {
				p.albumIDs[al.ID] = b.ID
			}
		}
		stored, err := m.albumFiles(ctx, p, la, albums, byMBID)
		if err != nil {
			return fmt.Errorf("reading %s's files: %w", la.ArtistName, err)
		}
		// Albums Lidarr doesn't list files for, but that are in the
		// artist's folder, are found by their folder's name.
		found, err := downloader.FindAlbums(ctx, st, a)
		if err != nil {
			warn(fmt.Sprintf("%s: looking in its folder: %v", la.ArtistName, err))
		}
		m.update(func() {
			if isNew {
				pr.Imported.New++
			} else {
				pr.Imported.Already++
			}
			pr.Files += stored + found
		})
	}
	progress(len(p.artists), len(p.artists))
	return nil
}

// albumFiles records each of an artist's albums Lidarr has tracks for as
// in the library: the folder its tracks are in, and their size.
func (m *Migrator) albumFiles(ctx context.Context, p *pending, la lArtist, albums []lAlbum, byMBID map[string]store.Album) (int, error) {
	if la.Statistics.TrackFileCount == 0 {
		return 0, nil
	}
	var files []lTrackFile
	if err := p.l.get(ctx, "/trackfile", map[string][]string{"artistId": {strconv.Itoa(la.ID)}}, &files); err != nil {
		return 0, err
	}
	tracks := map[int][]string{}
	sizes := map[int]int64{}
	for _, f := range files {
		tracks[f.AlbumID] = append(tracks[f.AlbumID], mapPath(p.req.PathMap, f.Path))
		sizes[f.AlbumID] += f.Size
	}
	artistDir := filepath.Clean(mapPath(p.req.PathMap, la.Path))
	stored := 0
	for _, al := range albums {
		b, ok := byMBID[normMBID(al.ForeignAlbumID)]
		if !ok || b.File != nil || len(tracks[al.ID]) == 0 {
			continue
		}
		dir := downloader.AlbumDir(tracks[al.ID])
		// Tracks loose in the artist's folder, spread over several, or
		// outside it have no album folder of their own to record.
		if rel, err := filepath.Rel(artistDir, dir); dir == "" || err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			continue
		}
		if _, err := m.Catalog.Store.AddFile(ctx, store.File{Kind: store.AlbumKind, OwnerID: b.ID, Path: dir, Size: sizes[al.ID]}); err != nil {
			if errors.Is(err, store.ErrExists) {
				continue // another album has the folder
			}
			return stored, err
		}
		stored++
	}
	return stored, nil
}

func normMBID(id string) string { return strings.ToLower(id) }
