// Package version reports the build version, set at link time.
package version

// Version is overridden with -ldflags "-X github.com/yeixio/boxoffice/internal/version.Version=v1.2.3".
var Version = "dev"
