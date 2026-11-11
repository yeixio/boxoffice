package monitor

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
)

// The log lines in memory go when the server stops, and a pod's output goes
// when Kubernetes deletes the pod. So the server also writes its lines to
// files in its data folder, and has Go write a crash there too: when it
// starts again, the run before it can still be read, with whatever ended it.
//
//	<data>/logs/current.log      this run's lines, one JSON object each
//	<data>/logs/current.1.log    this run's older lines, once current.log fills
//	<data>/logs/crash.log        this run's crash, if it crashes
//	<data>/logs/previous*.log    the same three from the run before
const (
	journalCurrent   = "current.log"
	journalOlder     = "current.1.log"
	journalCrash     = "crash.log"
	journalPrevious  = "previous.log"
	journalPrevOld   = "previous.1.log"
	journalPrevCrash = "previous-crash.log"

	// maxCrash is how much of a crash is kept: the panic and the goroutine
	// that panicked come first, and the rest is every other goroutine.
	maxCrash = 256 << 10
)

// journalMax is how big current.log grows before it becomes current.1.log,
// so a run keeps at most twice this. A variable for tests.
var journalMax int64 = 16 << 20

// Journal writes log lines to files. The zero value writes nothing.
type Journal struct {
	dir  string
	mu   sync.Mutex
	f    *os.File
	size int64
	err  error // the first write that failed; nothing more is written after it
}

// PreviousRun is what the run before this one logged, and its crash, if it
// crashed.
type PreviousRun struct {
	// Logs are its latest lines, with the problems among them.
	Logs *Logs
	// Crash is what Go wrote when it crashed: a panic or a fatal error,
	// and every goroutine's stack. Empty when it didn't crash, or when it
	// was killed (Kubernetes deleting the pod, running out of memory),
	// which leaves nothing to write.
	Crash string
	// Started and Ended are its first and last lines' times (ms).
	Started, Ended int64
}

// Crashed says the previous run ended in a crash Go wrote down.
func (p *PreviousRun) Crashed() bool { return p != nil && p.Crash != "" }

// CrashSummary is the crash's first line, such as
// "panic: runtime error: index out of range [3] with length 3".
func (p *PreviousRun) CrashSummary() string {
	if !p.Crashed() {
		return ""
	}
	for _, line := range strings.Split(p.Crash, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			if len(line) > 300 {
				line = line[:300] + "…"
			}
			return line
		}
	}
	return ""
}

// Empty says there's nothing from the run before: the first start, or the
// first since an update that brought the journal.
func (p *PreviousRun) Empty() bool {
	if p == nil {
		return true
	}
	if p.Crash != "" {
		return false
	}
	n, _ := p.Logs.Kept()
	return n == 0
}

// OpenJournal moves the last run's files aside as the previous run, reads
// them, and starts this run's: Go writes a crash to crash.log from now on,
// and l writes its lines to current.log. A previous run that can't be read
// is empty; a journal that can't be written is an error, and l keeps
// logging to memory and the output only.
func OpenJournal(dir string, l *Logs) (*PreviousRun, error) {
	prev := &PreviousRun{Logs: NewLogs(0)}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return prev, fmt.Errorf("make the log folder: %w", err)
	}
	for from, to := range map[string]string{journalCurrent: journalPrevious, journalOlder: journalPrevOld, journalCrash: journalPrevCrash} {
		to = filepath.Join(dir, to)
		if err := os.Remove(to); err != nil && !errors.Is(err, os.ErrNotExist) {
			return prev, err
		}
		if err := os.Rename(filepath.Join(dir, from), to); err != nil && !errors.Is(err, os.ErrNotExist) {
			return prev, err
		}
	}
	readPrevious(dir, prev)

	crash, err := os.OpenFile(filepath.Join(dir, journalCrash), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return prev, err
	}
	// Go writes a crash here as well as to the output. The runtime keeps
	// its own copy of the file, so crash is closed again.
	err = debug.SetCrashOutput(crash, debug.CrashOptions{})
	crash.Close()
	if err != nil {
		return prev, err
	}
	f, err := os.OpenFile(filepath.Join(dir, journalCurrent), os.O_CREATE|os.O_WRONLY|os.O_APPEND|os.O_TRUNC, 0o640)
	if err != nil {
		return prev, err
	}
	l.mu.Lock()
	l.journal = &Journal{dir: dir, f: f}
	l.mu.Unlock()
	return prev, nil
}

// readPrevious reads the previous run's lines, older file first, and its crash.
func readPrevious(dir string, prev *PreviousRun) {
	for _, name := range []string{journalPrevOld, journalPrevious} {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
		for sc.Scan() {
			var e Entry
			if json.Unmarshal(sc.Bytes(), &e) != nil || e.Time == 0 {
				continue // a line cut short by the crash
			}
			if prev.Started == 0 {
				prev.Started = e.Time
			}
			prev.Ended = e.Time
			prev.Logs.addEntry(e)
		}
		f.Close()
	}
	if f, err := os.Open(filepath.Join(dir, journalPrevCrash)); err == nil {
		b, _ := io.ReadAll(io.LimitReader(f, maxCrash+1))
		f.Close()
		crash := string(bytes.TrimSpace(b))
		if len(b) > maxCrash {
			crash = string(b[:maxCrash]) + "\n…"
		}
		prev.Crash = crash
	}
}

// write adds one line to the file. It's called in the order lines are
// logged, so it must be quick: the data folder is on a local disk.
func (j *Journal) write(e Entry) {
	if j == nil {
		return
	}
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	b = append(b, '\n')
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.f == nil || j.err != nil {
		return
	}
	if j.size+int64(len(b)) > journalMax {
		j.rotate()
		if j.err != nil {
			return
		}
	}
	n, err := j.f.Write(b)
	j.size += int64(n)
	if err != nil {
		j.fail(err)
	}
}

// rotate makes current.log current.1.log and starts a new current.log.
func (j *Journal) rotate() {
	j.f.Close()
	cur := filepath.Join(j.dir, journalCurrent)
	if err := os.Rename(cur, filepath.Join(j.dir, journalOlder)); err != nil {
		j.f = nil
		j.fail(err)
		return
	}
	f, err := os.OpenFile(cur, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		j.f = nil
		j.fail(err)
		return
	}
	j.f, j.size = f, 0
}

// fail stops writing, and says why once in the output. It can't log
// through slog: that would come back here.
func (j *Journal) fail(err error) {
	j.err = err
	fmt.Fprintf(os.Stderr, "boxoffice: stopped writing the log to %s: %v\n", j.dir, err)
}

// Close stops writing.
func (j *Journal) Close() error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.f == nil {
		return nil
	}
	err := j.f.Close()
	j.f = nil
	return err
}
