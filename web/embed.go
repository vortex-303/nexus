package web

import (
	"embed"
	"io/fs"
)

//go:embed all:build
var static embed.FS

// Static returns the embedded static file system (SvelteKit build output).
func Static() fs.FS {
	sub, _ := fs.Sub(static, "build")
	return sub
}
