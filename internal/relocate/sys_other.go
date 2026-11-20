//go:build !(linux || darwin || freebsd || openbsd || netbsd)

package relocate

import "os"

func writable(dir string) bool {
	st, err := os.Stat(dir)
	return err == nil && st.IsDir()
}

func sameDisk(a, b string) bool { return false }
