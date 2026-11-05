package downloader

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"slices"
	"strings"
	"time"
)

// QuarantineItem is one release held in the quarantine folder.
type QuarantineItem struct {
	// Path is relative to the downloads folder, as the file browser has it.
	Path  string `json:"path"`
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	Files int    `json:"files"`
	// Since is when it was quarantined, as near as Box Office knows.
	Since time.Time `json:"since"`
	// Reason is why, when the download it came from is still known.
	Reason   string         `json:"reason,omitempty"`
	Download *EntryDownload `json:"download,omitempty"`
	Busy     bool           `json:"busy,omitempty"`
}

// Quarantine lists what's in the quarantine folder, newest first.
func (e *Engine) Quarantine(ctx context.Context) ([]QuarantineItem, error) {
	e.init()
	root, err := e.root()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	fsys := root.FS()
	entries, err := fs.ReadDir(fsys, QuarantineDir)
	if errors.Is(err, fs.ErrNotExist) {
		return []QuarantineItem{}, nil
	}
	if err != nil {
		return nil, err
	}
	state, err := e.folderUse(ctx)
	if err != nil {
		return nil, err
	}
	out := []QuarantineItem{}
	for _, de := range entries {
		rel := QuarantineDir + "/" + de.Name()
		it := QuarantineItem{Path: rel, Name: de.Name(), Busy: state.busy[rel]}
		if info, err := de.Info(); err == nil {
			it.Since = info.ModTime()
			if de.IsDir() {
				it.Size, it.Files = treeSize(fsys, rel)
			} else {
				it.Size, it.Files = info.Size(), 1
			}
		}
		if d, ok := state.owner[rel]; ok {
			it.Download = &EntryDownload{ID: d.ID, Name: d.Name, Status: d.Status, Kind: d.Kind, TitleID: d.TitleID}
			it.Reason = quarantineReason(d.Message)
			if !d.FinishedAt.IsZero() {
				it.Since = d.FinishedAt
			}
		}
		out = append(out, it)
	}
	slices.SortFunc(out, func(a, b QuarantineItem) int { return b.Since.Compare(a.Since) })
	return out, nil
}

// quarantineReason takes why from a quarantined download's message.
func quarantineReason(msg string) string {
	if _, why, ok := strings.Cut(msg, "doesn't look safe: "); ok {
		why, _, _ = strings.Cut(why, ". Box Office didn't")
		return sentence(why)
	}
	return msg
}

// PurgeQuarantine deletes what's been in quarantine longer than the
// settings allow, and says how many it deleted. With no limit set, it
// deletes nothing.
func (e *Engine) PurgeQuarantine(ctx context.Context) int {
	set, err := LoadSettings(ctx, e.Store)
	if err != nil || set.QuarantineDays <= 0 {
		return 0
	}
	if _, err := os.Stat(e.Dir); err != nil {
		return 0
	}
	items, err := e.Quarantine(ctx)
	if err != nil {
		e.Log.Warn("couldn't look in quarantine", "err", err)
		return 0
	}
	cut := time.Now().Add(-time.Duration(set.QuarantineDays) * 24 * time.Hour)
	n := 0
	for _, it := range items {
		if it.Busy || it.Since.IsZero() || it.Since.After(cut) {
			continue
		}
		if err := e.DeletePath(ctx, it.Path); err != nil {
			e.Log.Warn("couldn't delete from quarantine", "path", it.Path, "err", err)
			continue
		}
		e.Log.Info("deleted from quarantine after the days set", "path", it.Path, "days", set.QuarantineDays)
		n++
	}
	return n
}
