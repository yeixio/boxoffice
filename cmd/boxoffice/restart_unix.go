//go:build unix

package main

import (
	"fmt"
	"os"
	"syscall"
)

// execSelf replaces this process with a fresh run of exe, keeping its
// arguments, environment and process ID. Everything Go opened is closed on
// exec, so the new run can listen on the same port.
func execSelf(exe string) error {
	if err := syscall.Exec(exe, os.Args, os.Environ()); err != nil {
		return fmt.Errorf("start Box Office again: %w", err)
	}
	return nil
}
