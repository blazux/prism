package help

import (
	"embed"
	"io/fs"
)

//go:embed *.md
var files embed.FS

// Files returns the immutable resources bundled with this version of Prism.
func Files() fs.FS { return files }
