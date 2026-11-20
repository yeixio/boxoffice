//go:build linux || darwin || freebsd || openbsd || netbsd

package relocate

import "golang.org/x/sys/unix"

// writable says whether Box Office may write in dir, without writing to
// it. A read-only mount answers no.
func writable(dir string) bool { return unix.Access(dir, unix.W_OK) == nil }

// sameDisk says a and b are on the same filesystem, so a folder moves from
// one to the other by renaming it, taking no space.
func sameDisk(a, b string) bool {
	var sa, sb unix.Stat_t
	if unix.Stat(a, &sa) != nil || unix.Stat(b, &sb) != nil {
		return false
	}
	return sa.Dev == sb.Dev
}
