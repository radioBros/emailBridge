//go:build !embedweb

// Package webui embeds the admin build (S13 §8); this build has none (no embedweb tag).
package webui

import "io/fs"

// FS returns nil: the binary was built without -tags embedweb.
func FS() fs.FS { return nil }
