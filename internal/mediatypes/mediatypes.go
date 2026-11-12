// Package mediatypes lists the kinds of media Box Office looks after beyond
// movies and TV (music, audiobooks, ebooks, comics and games) and says which of
// them an admin has turned on. Each one is off until it's turned on in
// Settings; while it's off, Box Office takes no root folders for it, shows
// no pages for it and searches for nothing of it.
package mediatypes

import (
	"context"
	"fmt"
	"slices"

	"github.com/yeixio/boxoffice/internal/store"
)

// Type is one kind of media that can be turned on.
type Type struct {
	// Key names the type in settings, root folders and the API.
	Key string `json:"key"`
	// Name is what people see.
	Name string `json:"name"`
	// Summary says in a line what turning it on gives.
	Summary string `json:"summary"`
	// Replaces names the app it stands in for, if any.
	Replaces string `json:"replaces,omitempty"`
	// Folders are where the type's library is usually mounted, in the
	// order they're suggested as root folders.
	Folders []string `json:"-"`
}

// Keys of the types.
const (
	Music     = "music"
	Audiobook = "audiobook"
	Ebook     = "ebook"
	Comic     = "comic"
	Game      = "game"
)

// All lists the types in the order Settings shows them.
var All = []Type{
	{Key: Music, Name: "Music", Replaces: "Lidarr",
		Summary: "Follow artists and get their albums, from MusicBrainz.",
		Folders: []string{"/media/music", "/data/media/music", "/music"}},
	{Key: Audiobook, Name: "Audiobooks", Replaces: "Readarr",
		Summary: "Follow authors and get their books as audiobooks.",
		Folders: []string{"/media/audiobooks", "/data/media/audiobooks", "/audiobooks"}},
	{Key: Ebook, Name: "Ebooks", Replaces: "Readarr",
		Summary: "Follow authors and get their books as ebooks.",
		Folders: []string{"/media/books", "/data/media/books", "/media/ebooks", "/data/media/ebooks", "/books"}},
	{Key: Comic, Name: "Comics and manga",
		Summary: "Follow series and get new issues and volumes.",
		Folders: []string{"/media/comics", "/data/media/comics", "/comics"}},
	{Key: Game, Name: "Games",
		Summary: "See your video game collection: arcade sets from MAME and console games, checked and with box art.",
		Folders: []string{"/media/games", "/data/media/games", "/media/roms", "/data/media/roms", "/games", "/roms"}},
}

// Lookup finds a type by its key.
func Lookup(key string) (Type, bool) {
	i := slices.IndexFunc(All, func(t Type) bool { return t.Key == key })
	if i < 0 {
		return Type{}, false
	}
	return All[i], true
}

// SettingKey is the setting that turns a type on.
func SettingKey(key string) string { return "media_" + key + "_enabled" }

// Enabled says whether a type is turned on. A type nobody turned on is off.
func Enabled(ctx context.Context, st *store.Store, key string) (bool, error) {
	if _, ok := Lookup(key); !ok {
		return false, nil
	}
	v, err := st.Setting(ctx, SettingKey(key))
	return v == "1", err
}

// State is a type and whether it's on.
type State struct {
	Type
	Enabled bool `json:"enabled"`
}

// States lists every type and whether it's on.
func States(ctx context.Context, st *store.Store) ([]State, error) {
	out := make([]State, 0, len(All))
	for _, t := range All {
		on, err := Enabled(ctx, st, t.Key)
		if err != nil {
			return nil, err
		}
		out = append(out, State{Type: t, Enabled: on})
	}
	return out, nil
}

// SetEnabled turns a type on or off. Turning one off keeps its root
// folders and anything else saved for it, ready for when it's back on.
func SetEnabled(ctx context.Context, st *store.Store, key string, on bool) error {
	if _, ok := Lookup(key); !ok {
		return fmt.Errorf("there's no media type %q", key)
	}
	v := ""
	if on {
		v = "1"
	}
	return st.SetSetting(ctx, SettingKey(key), v)
}
