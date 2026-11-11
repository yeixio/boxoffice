package downloader

import (
	"errors"
	"io/fs"
	"syscall"
	"testing"
)

// An obfuscated post's yEnc names are hashes; the subject may still give
// the real name, which is kept (Earth 2: 6f2d940f….r00 is
// earth.2.s01e09-med.r00).
func TestHashedNames(t *testing.T) {
	for name, want := range map[string]bool{
		"6f2d940f3fa058e99b527056bf33b655b02c7bd6f05d9bd1fb6a630e5f2e0a62.rar": true,
		"ee75c6d4a1b2c3d4e5f6.r00":                true,
		"aB3dE6gH9jK2mN5pQ8rS1tV4.mkv":            true,
		"earth.2.s01e09-med.r00":                  false,
		"Doogie.Howser.M.D.S04E21.mkv.part01.rar": false,
		"movie.mkv":           false,
		"0123456789abcde.rar": false, // too short to tell
	} {
		if got := hashedName(name); got != want {
			t.Errorf("%s: %v", name, got)
		}
	}
	if !sameExt("earth.2.s01e09-med.r00", "ee75c6d4a1b2c3d4e5f6.R00") || sameExt("earth.2.sfv", "ee75c6d4a1b2c3d4e5f6.r00") || sameExt("x/y.r00", "ee75c6d4a1b2c3d4e5f6.r00") {
		t.Error("sameExt")
	}
}

// A volume that went away as it was read (a stale handle on the share,
// Doogie Howser's part08) is passing trouble, tried again later.
func TestVanishedVolume(t *testing.T) {
	err := vanished(&fs.PathError{Op: "read", Path: "/downloads/x.part08.rar", Err: syscall.ENOENT})
	var se shareError
	if !errors.As(shareTrouble(err), &se) {
		t.Fatalf("%v isn't passing", err)
	}
	// One that was never there isn't.
	err = vanished(&fs.PathError{Op: "open", Path: "/downloads/x.part08.rar", Err: syscall.ENOENT})
	if errors.As(shareTrouble(err), &se) {
		t.Fatalf("%v is passing", err)
	}
}
