//go:build linux || darwin || freebsd || openbsd || netbsd

package library

import "golang.org/x/sys/unix"

// writable says whether Box Office may write in dir, without writing to
// it. A read-only mount answers no.
func writable(dir string) bool { return unix.Access(dir, unix.W_OK) == nil }
