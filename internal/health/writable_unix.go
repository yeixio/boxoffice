//go:build linux || darwin || freebsd || openbsd || netbsd

package health

import "golang.org/x/sys/unix"

// writable says whether Box Office may write in dir, without writing to it.
func writable(dir string) bool { return unix.Access(dir, unix.W_OK) == nil }

// freeSpace is how many bytes are free on the disk that holds path, or -1
// when that can't be told.
func freeSpace(path string) int64 {
	var st unix.Statfs_t
	if unix.Statfs(path, &st) != nil {
		return -1
	}
	return int64(st.Bavail) * int64(st.Bsize)
}
