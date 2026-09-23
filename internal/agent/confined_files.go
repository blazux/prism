package agent

import (
	"os"
	"path/filepath"
	"prism/internal/workspace"
	"strings"
)

// Plugin paths may be configured separately from the execution volume. When
// they are inside it, anchor at the volume, not at an agent-writable subdirectory.
func (e *ToolExecutor) fileRoot(full string) (string, string, error) {
	if e.workspaceDir != "" {
		rel, err := filepath.Rel(e.workspaceDir, full)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
			return e.workspaceDir, rel, nil
		}
	}
	root := filepath.Dir(e.pluginDir)
	rel, err := filepath.Rel(root, full)
	return root, rel, err
}
func (e *ToolExecutor) readManagedDir(full string) ([]os.DirEntry, error) {
	root, rel, err := e.fileRoot(full)
	if err != nil {
		return nil, err
	}
	return workspace.ReadDir(root, rel)
}
func (e *ToolExecutor) removeManaged(full string) error {
	root, rel, err := e.fileRoot(full)
	if err != nil {
		return err
	}
	return workspace.Remove(root, rel)
}
func (e *ToolExecutor) readManagedFile(full string) ([]byte, error) {
	root, rel, err := e.fileRoot(full)
	if err != nil {
		return nil, err
	}
	return workspace.ReadFile(root, rel)
}
func (e *ToolExecutor) writeManagedFile(full string, data []byte) error {
	root, rel, err := e.fileRoot(full)
	if err != nil {
		return err
	}
	return workspace.WriteFile(root, rel, data)
}
