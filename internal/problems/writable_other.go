//go:build !(linux || darwin || freebsd || openbsd || netbsd)

package problems

import "os"

func writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".boxoffice-write-test")
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(f.Name())
	return true
}
