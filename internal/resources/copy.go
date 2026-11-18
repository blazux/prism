package resources

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"prism/internal/workspace"
)

// CreateWidgetCopy creates a new HTML/metadata pair. Both files are reserved
// exclusively, so imports preserve existing edits, locks and even orphan files.
// Callers hold the environment's resource lock while publishing the new widget.
func CreateWidgetCopy(root, dir, preferredID string, content, metadata []byte) (string, error) {
	if preferredID == "" || preferredID != filepath.Base(preferredID) {
		return "", fmt.Errorf("invalid widget copy id")
	}
	for n := 1; n <= 10000; n++ {
		id := preferredID
		if n > 1 {
			id = fmt.Sprintf("%s-%d", preferredID, n)
		}
		metaPath := filepath.Join(dir, id+".meta.json")
		htmlPath := filepath.Join(dir, id+".html")
		if err := workspace.CreateFile(root, metaPath, metadata); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return "", err
		}
		if err := workspace.CreateFile(root, htmlPath, content); err != nil {
			_ = workspace.Remove(root, metaPath)
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return "", err
		}
		return id, nil
	}
	return "", fmt.Errorf("cannot allocate another copy of this widget")
}
