//go:build linux || darwin || freebsd || openbsd || netbsd

package arrapi

import "golang.org/x/sys/unix"

// FreeSpace is how many bytes are free on the disk that holds path, or 0
// when that can't be told.
func FreeSpace(path string) int64 {
	var st unix.Statfs_t
	if unix.Statfs(path, &st) != nil {
		return 0
	}
	return int64(st.Bavail) * int64(st.Bsize)
}
