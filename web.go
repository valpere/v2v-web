// Package v2vweb embeds the browser client so the server ships as one binary.
package v2vweb

import (
	"embed"
	"io/fs"
)

//go:embed web
var files embed.FS

// Client is the contents of web/ (index.html, assets/…).
func Client() fs.FS {
	sub, err := fs.Sub(files, "web")
	if err != nil {
		panic(err) // the directory is embedded at build time
	}
	return sub
}
