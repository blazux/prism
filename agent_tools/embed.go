package agenttools

import (
	"embed"
	"io/fs"
)

//go:embed *.py .apt-packages
var files embed.FS

// Files returns the immutable resources bundled with this version of Prism.
func Files() fs.FS { return files }
