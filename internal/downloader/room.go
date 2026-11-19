package downloader

import (
	"context"
	"errors"
	"fmt"
	"syscall"
	"time"

	"github.com/yeixio/boxoffice/internal/monitor"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/usenet/unpack"
)

// Room on the downloads folder's disk. A download isn't started, and an
// archive isn't unpacked, when that would leave less free than the Keep
// free setting says: the queue waits and says why, as it does when the
// folder is away, and carries on by itself once there's room. Nothing
// fails for it, and no release is blocked.

// roomError is a download stopped because the disk was out of room, or
// would have been: need is what unpacking it takes, when that's known.
type roomError struct {
	need int64
	error
}

func (r roomError) Unwrap() error { return r.error }

// roomTrouble marks err as a roomError when it's the disk being full.
func roomTrouble(err error) error {
	var re roomError
	if err != nil && !errors.As(err, &re) && errors.Is(err, syscall.ENOSPC) {
		return roomError{error: err}
	}
	return err
}

// freeWait is how long the disk gets to say how much is free; one that
// doesn't answer (a share that hung) isn't held against the download.
const freeWait = 5 * time.Second

// free is how much room the downloads folder's disk has, when it says.
func (e *Engine) free() (int64, bool) {
	if e.Free != nil {
		return e.Free(e.Dir)
	}
	type answer struct {
		free int64
		ok   bool
	}
	c := make(chan answer, 1)
	go func() {
		d, ok := monitor.DiskUsage(e.Dir)
		c <- answer{int64(d.Free), ok} //nolint:gosec
	}()
	select {
	case a := <-c:
		return a.free, a.ok
	case <-time.After(freeWait):
		return 0, false
	}
}

// noRoom says why d can't start for lack of room, or "" when it can: what
// it has left to fetch, and what unpacking it took when that was tried,
// must fit with the Keep free setting's room to spare.
func (e *Engine) noRoom(set Settings, d store.Download) string {
	e.mu.Lock()
	unpacking, stopped := e.unpackNeed[d.ID]
	e.mu.Unlock()
	keep := int64(set.KeepFree) * 1_000_000_000
	// With nothing to keep free, only a download the disk already turned
	// away waits: it would only fill the disk again.
	if keep == 0 && !stopped {
		return ""
	}
	free, ok := e.free()
	if !ok {
		return ""
	}
	need := max(d.Size-d.DoneBytes, 0) + unpacking
	if free >= need+keep {
		return ""
	}
	why := fmt.Sprintf("The downloads folder's disk has %s free, and %s needs about %s more", humanBytes(free), d.Name, humanBytes(need))
	if keep > 0 {
		why += fmt.Sprintf(", with %s kept free", humanBytes(keep))
	}
	return why + ". Downloads wait, and carry on by themselves once there's room: free some space, or change Keep free in Settings → Downloads."
}

// unpackRoom is the check an archive's size gets before it's written out,
// and now and then while it is.
func (e *Engine) unpackRoom(ctx context.Context) unpack.Room {
	set, err := LoadSettings(ctx, e.Store)
	keep := int64(set.KeepFree) * 1_000_000_000
	// The most it was asked for is the whole of the biggest archive, which
	// is what trying again will take.
	var whole int64
	return func(need int64) error {
		whole = max(whole, need)
		if err != nil || keep == 0 {
			return nil
		}
		free, ok := e.free()
		if !ok || free >= need+keep {
			return nil
		}
		return roomError{need: whole, error: fmt.Errorf("the downloads folder's disk has %s free, and unpacking takes %s with %s kept free", humanBytes(free), humanBytes(need), humanBytes(keep))}
	}
}
