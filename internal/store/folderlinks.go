package store

import (
	"context"
	"encoding/json"
	"path/filepath"
)

// settingFolderLinks keeps the folders someone said hold more of a title
// that Box Office keeps in another folder, from a root folder scan.
const settingFolderLinks = "folders:linked"

// FolderLink says a folder holds more of a title on the list that's kept
// in another folder: "Cunk on Britain" for the show TMDB calls "Cunk
// on...". Problem files then looks through it with that title's own
// folder, so its files can be moved in and its copies told apart.
type FolderLink struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"` // movie or series
	TitleID int64  `json:"titleId"`
}

// FolderLinks lists the linked folders.
func (s *Store) FolderLinks(ctx context.Context) ([]FolderLink, error) {
	v, err := s.Setting(ctx, settingFolderLinks)
	if err != nil || v == "" {
		return nil, err
	}
	var out []FolderLink
	if err := json.Unmarshal([]byte(v), &out); err != nil {
		return nil, nil // a bad value is as good as none
	}
	return out, nil
}

// LinkFolder says a folder holds more of a title, in place of whatever
// it was linked to before.
func (s *Store) LinkFolder(ctx context.Context, l FolderLink) error {
	l.Path = filepath.Clean(l.Path)
	list, err := s.FolderLinks(ctx)
	if err != nil {
		return err
	}
	out := []FolderLink{l}
	for _, x := range list {
		if filepath.Clean(x.Path) != l.Path {
			out = append(out, x)
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return err
	}
	return s.SetSetting(ctx, settingFolderLinks, string(b))
}
