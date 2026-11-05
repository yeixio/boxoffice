package downloader

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/usenet/par2"
	"github.com/yeixio/boxoffice/internal/usenet/unpack"
)

// postProcess checks and repairs the files, unpacks them, and imports the
// result.
func (e *Engine) postProcess(ctx context.Context, r *running, d *store.Download, job *fetchJob) error {
	status := func(s, stage string) {
		r.setStage(stage)
		e.Store.SetDownloadStatus(context.WithoutCancel(ctx), d.ID, s, "")
	}
	status(store.DownloadVerifying, "")
	notes, err := e.repair(ctx, r, d, job, status)
	if err != nil {
		return err
	}
	status(store.DownloadUnpacking, "")
	if err := e.unpack(ctx, d, job); err != nil {
		return err
	}
	files := listFiles(job.dir)
	if len(files) == 0 {
		return errors.New("the download finished with no files in it")
	}
	lead := ""
	if len(notes) > 0 {
		lead = sentence(strings.Join(notes, "; ")) + ". "
	}
	msg := lead + "Downloaded to " + d.Folder
	set, err := LoadSettings(ctx, e.Store)
	if err != nil {
		return err
	}
	switch {
	case d.Kind == "":
		// Added by hand: it stays in the downloads folder.
	case !set.Import:
		msg += ". Moving downloads into the library is off, so it's waiting there."
	case e.Importer == nil:
	default:
		status(store.DownloadImporting, "")
		to, err := e.Importer.Import(ctx, *d, job.dir, files)
		if err != nil {
			return fmt.Errorf("downloaded, but couldn't move it into the library: %w", err)
		}
		e.Store.SetDownloadImported(context.WithoutCancel(ctx), d.ID, to)
		d.ImportedTo = to
		msg = lead + "Moved into the library at " + to
		// Nothing's left worth keeping.
		e.removeFolder(*d)
	}
	r.setStage("")
	return e.Store.SetDownloadStatus(context.WithoutCancel(ctx), d.ID, store.DownloadCompleted, msg)
}

// repair checks the files against par2, fetching recovery volumes and
// repairing as needed. With no par2 files, it trusts what came.
func (e *Engine) repair(ctx context.Context, r *running, d *store.Download, job *fetchJob, status func(s, stage string)) ([]string, error) {
	var notes []string
	par2Files := func() []string {
		var out []string
		for _, f := range listFiles(job.dir) {
			if ok, _ := isPar2(f); ok {
				out = append(out, filepath.Join(job.dir, f))
			}
		}
		return out
	}
	set, err := par2.Load(par2Files())
	if errors.Is(err, par2.ErrNoSet) {
		if r.failed.Load() > 0 {
			return nil, fmt.Errorf("%s of the release is missing from your news servers, and it came with no par2 files to repair it", humanBytes(r.failed.Load()))
		}
		return []string{"no par2 files to check it with"}, nil
	}
	if err != nil {
		return nil, err
	}
	renamed, err := set.Match(job.dir, listFiles(job.dir))
	if err != nil {
		return nil, err
	}
	if len(renamed) > 0 {
		notes = append(notes, fmt.Sprintf("renamed %d obfuscated %s", len(renamed), plural(len(renamed), "file", "files")))
	}
	rep, err := set.Verify(ctx, job.dir)
	if err != nil {
		return nil, err
	}
	if rep.OK() {
		return notes, nil
	}
	// Fetch just enough recovery volumes, smallest first that covers it.
	if need := rep.NeedBlocks - rep.HaveBlocks; need > 0 {
		held := job.held()
		slices.SortFunc(held, func(a, b *nzbFile) int { return a.blocks - b.blocks })
		var pick []*nzbFile
		got := 0
		for _, f := range held {
			if got >= need {
				break
			}
			pick = append(pick, f)
			got += f.blocks
		}
		if got < need {
			return nil, fmt.Errorf("%d blocks are damaged, and there are only %d recovery blocks; try another release", rep.NeedBlocks, rep.HaveBlocks+got)
		}
		var extra int64
		for _, f := range pick {
			f.held = false
			extra += f.file.Bytes()
		}
		status(store.DownloadDownloading, fmt.Sprintf("fetching %d recovery blocks", got))
		r.size.Add(extra)
		d.Size += extra
		e.mu.Lock()
		srv := e.srv
		e.mu.Unlock()
		if err := e.download(ctx, r, d, srv, job, pick); err != nil {
			return nil, err
		}
		status(store.DownloadVerifying, "")
		if set, err = par2.Load(par2Files()); err != nil {
			return nil, err
		}
		if rep, err = set.Verify(ctx, job.dir); err != nil {
			return nil, err
		}
		if rep.OK() {
			return notes, nil
		}
	}
	if !rep.Repairable() {
		return nil, fmt.Errorf("%d blocks are damaged, and there are only %d recovery blocks; try another release", rep.NeedBlocks, rep.HaveBlocks)
	}
	status(store.DownloadRepairing, fmt.Sprintf("rebuilding %d blocks", rep.NeedBlocks))
	if err := set.Repair(ctx, job.dir, rep, 0); err != nil {
		return nil, err
	}
	if rep, err = set.Verify(ctx, job.dir); err != nil {
		return nil, err
	}
	if !rep.OK() {
		return nil, errors.New("repair didn't fix every file")
	}
	return append(notes, "repaired"), nil
}

func isPar2(name string) (bool, int) {
	low := strings.ToLower(name)
	return strings.HasSuffix(low, ".par2"), 0
}

// unpack extracts the archives, then deletes them and the par2 files.
func (e *Engine) unpack(ctx context.Context, d *store.Download, job *fetchJob) error {
	files := listFiles(job.dir)
	archives := unpack.Find(job.dir, files)
	var volumes []string
	for _, a := range archives {
		res, err := unpack.Extract(ctx, a, job.dir, d.Password)
		if errors.Is(err, unpack.ErrPassword) {
			if d.Password == "" {
				return errors.New("the archive needs a password, and the release didn't give one")
			}
			return errors.New("the archive's password isn't right")
		}
		if err != nil {
			return fmt.Errorf("unpack %s: %w", filepath.Base(a.Path), err)
		}
		volumes = append(volumes, res.Volumes...)
	}
	if len(archives) > 0 {
		for _, v := range volumes {
			if _, ok := e.inside(filepath.Join(d.Folder, filepath.Base(v))); ok {
				os.Remove(filepath.Join(job.dir, filepath.Base(v)))
			}
		}
		// Any other volume of the same sets, such as .r00 files rardecode
		// read without listing.
		for _, f := range files {
			if unpack.IsVolume(f) {
				os.Remove(filepath.Join(job.dir, f))
			}
		}
	}
	for _, f := range listFiles(job.dir) {
		low := strings.ToLower(f)
		if strings.HasSuffix(low, ".par2") || strings.HasSuffix(low, ".sfv") {
			os.Remove(filepath.Join(job.dir, f))
		}
	}
	return nil
}

// listFiles lists the files in dir and its subfolders, relative to it,
// leaving out Box Office's own partial files.
func listFiles(dir string) []string {
	var out []string
	filepath.WalkDir(dir, func(p string, de os.DirEntry, err error) error {
		if err != nil || de.IsDir() || !de.Type().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		if strings.HasPrefix(filepath.Base(rel), ".boxoffice-") {
			return nil
		}
		out = append(out, rel)
		return nil
	})
	slices.Sort(out)
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
