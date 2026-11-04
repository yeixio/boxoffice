//go:build !(linux || darwin || freebsd || openbsd || netbsd)

package arrapi

// FreeSpace is how many bytes are free on the disk that holds path, or 0
// when that can't be told.
func FreeSpace(path string) int64 { return 0 }
