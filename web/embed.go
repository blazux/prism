package web

import (
	"embed"
	"io/fs"
)

//go:embed *.html *.js *.css *.svg apps vendor
var files embed.FS

// Files returns the immutable resources bundled with this version of Prism.
func Files() fs.FS { return files }
