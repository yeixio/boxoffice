//go:build !(linux || darwin || freebsd || openbsd || netbsd)

package health

func writable(string) bool   { return true }
func freeSpace(string) int64 { return -1 }
