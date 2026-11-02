//go:build embedweb

// Package webui embeds the admin build (S13 §8). Built with -tags embedweb after copying web/dist to
// internal/webui/dist (the Containerfile and `task build:full` do this); without the tag FS returns nil and
// the API runs without the SPA (development, API-only tests).
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS returns the admin build rooted at dist.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil
	}
	return sub
}
