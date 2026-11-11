package downloader

import (
	"context"
	"strings"

	"github.com/yeixio/boxoffice/internal/store"
)

// SettingImportRetried is the version of Box Office that last took another
// go at moving waiting downloads into the library.
const SettingImportRetried = "downloads_import_retried"

// importFailures are the starts of what a finished download says when
// moving it into the library was tried and didn't work.
var importFailures = []string{"Downloaded, but it couldn't go into the library yet", "Downloaded, and waiting to go into the library"}

// RetryImports takes one more go, once per version of Box Office, at
// moving into the library the finished downloads that couldn't go in
// before: a newer version may know how (matching videos named with
// hashes by their tags, say). Only those it tried before are retried, and
// only while moving downloads in by itself is on; one that still can't go
// in keeps waiting for Move into library, as before.
func (e *Engine) RetryImports(ctx context.Context, version string) (int, error) {
	if e.Importer == nil || version == "" {
		return 0, nil
	}
	if done, err := e.Store.Setting(ctx, SettingImportRetried); err != nil || done == version {
		return 0, err
	}
	set, err := LoadSettings(ctx, e.Store)
	if err != nil {
		return 0, err
	}
	n := 0
	if set.Import {
		list, err := e.Store.DownloadHistory(ctx, 5000)
		if err != nil {
			return 0, err
		}
		for _, d := range list {
			if ctx.Err() != nil {
				return n, ctx.Err()
			}
			if !e.CanImport(d) || !triedImport(d.Message) || !e.claimImport(d.ID) {
				continue
			}
			if err := e.Store.SetDownloadStatus(ctx, d.ID, store.DownloadImporting, ""); err != nil {
				e.releaseImport(d.ID)
				return n, err
			}
			dir, _ := e.inside(d.Folder)
			msg := e.importInto(ctx, &d, dir, "")
			e.releaseImport(d.ID)
			if err := e.Store.SetDownloadStatus(ctx, d.ID, store.DownloadCompleted, msg); err != nil {
				return n, err
			}
			e.dropOrphan(ctx, d.ID)
			if d.ImportedTo != "" {
				n++
			}
		}
	}
	return n, e.Store.SetSetting(ctx, SettingImportRetried, version)
}

func triedImport(msg string) bool {
	for _, f := range importFailures {
		if strings.Contains(msg, f) {
			return true
		}
	}
	return false
}

// claimImport marks a download as being moved into the library, so Move
// into library isn't run on it at the same time; false if it already is.
func (e *Engine) claimImport(id int64) bool {
	e.init()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.importing == nil {
		e.importing = map[int64]bool{}
	}
	if e.importing[id] {
		return false
	}
	e.importing[id] = true
	return true
}

func (e *Engine) releaseImport(id int64) {
	e.mu.Lock()
	delete(e.importing, id)
	e.mu.Unlock()
}
