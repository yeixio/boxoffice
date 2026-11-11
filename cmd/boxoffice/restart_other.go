//go:build !unix

package main

import (
	"fmt"
	"os"
	"os/exec"
)

// execSelf starts a fresh run of exe beside this one, which then exits.
func execSelf(exe string) error {
	c := exec.Command(exe, os.Args[1:]...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Start(); err != nil {
		return fmt.Errorf("start Box Office again: %w", err)
	}
	return nil
}
