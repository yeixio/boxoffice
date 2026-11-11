package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/yeixio/boxoffice/internal/api"
)

// errRestart is what run gives back when the Restart button in Settings
// stopped it, after everything stopped cleanly and the database closed.
var errRestart = errors.New("restart")

// restart starts Box Office again after it stopped for a restart. In
// Kubernetes it exits, and the kubelet starts the container again, which
// also picks up changed Secrets. Everywhere else, Docker included, it
// starts itself again in the same process, so it comes back whatever the
// container's restart policy.
func restart() error {
	if api.RestartHow() == "kubernetes" {
		fmt.Fprintln(os.Stderr, "boxoffice: stopped to restart; Kubernetes starts it again")
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find Box Office's own program to restart it: %w", err)
	}
	return execSelf(exe)
}
