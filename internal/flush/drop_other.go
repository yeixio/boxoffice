//go:build !linux

package flush

import "os"

func dropCache(*os.File) {}
