// Package web embeds the static UI assets (HTML, CSS, JavaScript, SVG)
// into the binary at build time so the tool ships as a single file.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var staticFS embed.FS

// FS returns the embedded UI rooted at the `static` directory.
func FS() fs.FS {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err) // unreachable: directory exists at compile time
	}
	return sub
}
