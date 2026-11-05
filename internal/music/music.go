// Package music is Box Office's music list: artists from MusicBrainz, the
// albums they've made, and which of those are wanted. It works only while
// music is turned on in Settings, and it never touches files.
package music

import (
	"cmp"
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/mediatypes"
	"github.com/yeixio/boxoffice/internal/musicbrainz"
	"github.com/yeixio/boxoffice/internal/store"
)

// ErrOff means music isn't turned on in Settings.
var ErrOff = errors.New("turn on Music in Settings first")

// AlbumTypes are the kinds of album an artist can be watched for, in the
// order they're offered. "Album" is studio albums only: an album that's
// also live, a compilation or a soundtrack counts as that instead.
var AlbumTypes = []string{"Album", "EP", "Single", "Live", "Compilation", "Soundtrack", "Remix", "Other"}

// DefaultAlbumTypes are what a new artist is watched for.
var DefaultAlbumTypes = []string{"Album"}

// Wanted says whether an album is of a kind in types.
func Wanted(types []string, a musicbrainz.Album) bool {
	if len(a.SecondaryTypes) > 0 {
		for _, t := range a.SecondaryTypes {
			if slices.Contains(types, t) {
				return true
			}
		}
		return false
	}
	switch a.Type {
	case "Album", "EP", "Single":
		return slices.Contains(types, a.Type)
	}
	return slices.Contains(types, "Other")
}

// Service is the music list.
type Service struct {
	Store       *store.Store
	MusicBrainz *musicbrainz.Client
}

func (s *Service) on(ctx context.Context) error {
	on, err := mediatypes.Enabled(ctx, s.Store, mediatypes.Music)
	if err != nil {
		return err
	}
	if !on {
		return ErrOff
	}
	return nil
}

// LookupArtist is an artist found on MusicBrainz, with its id here if it's
// on the list.
type LookupArtist struct {
	musicbrainz.Artist
	ID int64 `json:"id,omitempty"`
}

// Lookup searches MusicBrainz for artists.
func (s *Service) Lookup(ctx context.Context, term string) ([]LookupArtist, error) {
	if err := s.on(ctx); err != nil {
		return nil, err
	}
	if strings.TrimSpace(term) == "" {
		return []LookupArtist{}, nil
	}
	found, err := s.MusicBrainz.SearchArtists(ctx, term)
	if err != nil {
		return nil, err
	}
	out := make([]LookupArtist, 0, len(found))
	for _, a := range found {
		l := LookupArtist{Artist: a}
		if have, err := s.Store.ArtistByMBID(ctx, a.MBID); err == nil {
			l.ID = have.ID
		}
		out = append(out, l)
	}
	return out, nil
}

// AddOptions are the choices made when adding an artist.
type AddOptions struct {
	MBID       string   `json:"mbid"`
	RootFolder string   `json:"rootFolder"`
	Monitored  bool     `json:"monitored"`
	AlbumTypes []string `json:"albumTypes"`
	// Path is the artist's folder when it's on disk already (found by a
	// root folder scan); empty puts it in the root folder by name.
	Path string `json:"-"`
}

func checkTypes(types []string) error {
	for _, t := range types {
		if !slices.Contains(AlbumTypes, t) {
			return &catalog.Error{Message: "there's no kind of album called " + t}
		}
	}
	return nil
}

// Add puts an artist on the list and fetches its albums.
func (s *Service) Add(ctx context.Context, o AddOptions) (store.Artist, error) {
	if err := s.on(ctx); err != nil {
		return store.Artist{}, err
	}
	if !musicbrainz.IsMBID(o.MBID) {
		return store.Artist{}, &catalog.Error{Message: "pick an artist from the search"}
	}
	if _, err := s.Store.ArtistByMBID(ctx, o.MBID); err == nil {
		return store.Artist{}, catalog.ErrExists
	}
	if o.AlbumTypes == nil {
		o.AlbumTypes = DefaultAlbumTypes
	}
	if err := checkTypes(o.AlbumTypes); err != nil {
		return store.Artist{}, err
	}
	root, err := s.rootFolder(ctx, o.RootFolder)
	if err != nil {
		return store.Artist{}, err
	}
	path := ""
	if o.Path != "" {
		path = filepath.Clean(o.Path)
		if filepath.Dir(path) != root {
			return store.Artist{}, &catalog.Error{Message: path + " isn't a folder in " + root}
		}
	}
	a, err := s.MusicBrainz.Artist(ctx, o.MBID)
	if errors.Is(err, musicbrainz.ErrNotFound) {
		return store.Artist{}, &catalog.Error{Message: "MusicBrainz doesn't know that artist"}
	}
	if err != nil {
		return store.Artist{}, err
	}
	added, err := s.Store.AddArtist(ctx, store.Artist{MBID: a.MBID, Name: a.Name, SortName: a.SortName, Type: a.Type, Country: a.Country,
		Disambiguation: a.Disambiguation, Monitored: o.Monitored, AlbumTypes: o.AlbumTypes, RootFolder: root,
		Path: cmp.Or(path, filepath.Join(root, catalog.FolderName(a.Name, 0)))})
	if errors.Is(err, store.ErrExists) {
		return store.Artist{}, catalog.ErrExists
	}
	if err != nil {
		return store.Artist{}, err
	}
	if err := s.refreshAlbums(ctx, added); err != nil {
		return added, err
	}
	return s.Store.Artist(ctx, added.ID)
}

func (s *Service) rootFolder(ctx context.Context, want string) (string, error) {
	folders, err := s.Store.RootFolders(ctx, mediatypes.Music)
	if err != nil {
		return "", err
	}
	if len(folders) == 0 {
		return "", &catalog.Error{Message: "add a music root folder under Settings first"}
	}
	if want == "" {
		return folders[0].Path, nil
	}
	want = filepath.Clean(want)
	for _, f := range folders {
		if f.Path == want {
			return want, nil
		}
	}
	return "", &catalog.Error{Message: want + " isn't one of the music root folders"}
}

// Refresh brings an artist's details and albums up to date from
// MusicBrainz. Albums that are new are watched for when the artist is and
// they're of a kind it's watched for; the rest keep their choice.
func (s *Service) Refresh(ctx context.Context, id int64) (store.Artist, error) {
	if err := s.on(ctx); err != nil {
		return store.Artist{}, err
	}
	have, err := s.Store.Artist(ctx, id)
	if err != nil {
		return store.Artist{}, err
	}
	a, err := s.MusicBrainz.Artist(ctx, have.MBID)
	if err != nil {
		return store.Artist{}, err
	}
	have.Name, have.SortName, have.Type, have.Country, have.Disambiguation = a.Name, a.SortName, a.Type, a.Country, a.Disambiguation
	if err := s.Store.UpdateArtist(ctx, have); err != nil {
		return store.Artist{}, err
	}
	if err := s.refreshAlbums(ctx, have); err != nil {
		return store.Artist{}, err
	}
	return s.Store.Artist(ctx, id)
}

func (s *Service) refreshAlbums(ctx context.Context, a store.Artist) error {
	albums, err := s.MusicBrainz.Albums(ctx, a.MBID)
	if err != nil {
		return err
	}
	for _, b := range albums {
		if _, err := s.Store.UpsertAlbum(ctx, store.Album{ArtistID: a.ID, MBID: b.MBID, Title: b.Title, Type: b.Type,
			SecondaryTypes: b.SecondaryTypes, Released: b.Released, Monitored: a.Monitored && Wanted(a.AlbumTypes, b)}); err != nil {
			return err
		}
	}
	return s.Store.ArtistRefreshed(ctx, a.ID)
}

// Edit is a change to an artist; nil fields stay as they are.
type Edit struct {
	Monitored  *bool    `json:"monitored"`
	AlbumTypes []string `json:"albumTypes"`
	RootFolder *string  `json:"rootFolder"`
}

// Update changes an artist. Changing which kinds of album it's watched for
// applies to every album of it: the kinds picked are watched for, the rest
// aren't.
func (s *Service) Update(ctx context.Context, id int64, e Edit) (store.Artist, error) {
	if err := s.on(ctx); err != nil {
		return store.Artist{}, err
	}
	a, err := s.Store.Artist(ctx, id)
	if err != nil {
		return store.Artist{}, err
	}
	if e.Monitored != nil {
		a.Monitored = *e.Monitored
	}
	if e.RootFolder != nil {
		root, err := s.rootFolder(ctx, *e.RootFolder)
		if err != nil {
			return store.Artist{}, err
		}
		a.RootFolder = root
		a.Path = filepath.Join(root, filepath.Base(a.Path))
	}
	if e.AlbumTypes != nil {
		if err := checkTypes(e.AlbumTypes); err != nil {
			return store.Artist{}, err
		}
		a.AlbumTypes = e.AlbumTypes
		albums, err := s.Store.Albums(ctx, id)
		if err != nil {
			return store.Artist{}, err
		}
		for _, b := range albums {
			want := Wanted(a.AlbumTypes, musicbrainz.Album{Type: b.Type, SecondaryTypes: b.SecondaryTypes})
			if want != b.Monitored {
				if err := s.Store.SetAlbumMonitored(ctx, b.ID, want); err != nil {
					return store.Artist{}, err
				}
			}
		}
	}
	if err := s.Store.UpdateArtist(ctx, a); err != nil {
		return store.Artist{}, err
	}
	return s.Store.Artist(ctx, id)
}
