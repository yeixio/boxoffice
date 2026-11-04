// Package web embeds Box Office's built web UI (boxoffice/web/dist) into
// the server binary.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS returns the built UI, or nil when it hasn't been built.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}
