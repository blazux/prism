package email

import (
	"fmt"
	"github.com/emersion/go-imap/v2"
	"strings"
)

// Retain the error-only API for existing callers.
func (c Config) ManageFolder(action, name, target string) error {
	_, err := c.ManageFolderResolved(action, name, target)
	return err
}

// Resolve a simple user name before CREATE/RENAME, then return the actual path
// so both the UI and the agent can immediately use the created/renamed folder.
func (c Config) ManageFolderResolved(action, name, target string) (string, error) {
	resolved := name
	if action == "create_folder" || action == "rename_folder" {
		candidate := name
		if action == "rename_folder" {
			candidate = target
		}
		if err := folderName(candidate); err != nil {
			return "", err
		}
		cl, err := c.dial()
		if err != nil {
			return "", err
		}
		rows, err := listFolders(cl)
		if err != nil {
			cl.Close()
			return "", err
		}
		var personal []imap.NamespaceDescriptor
		if cl.Caps().Has(imap.CapNamespace) {
			ns, err := cl.Namespace().Wait()
			if err != nil {
				cl.Close()
				return "", err
			}
			personal = ns.Personal
		}
		cl.Close()
		source := ""
		if action == "rename_folder" {
			source = name
		}
		resolved, err = resolveFolderName(candidate, source, rows, personal)
		if err != nil {
			return "", err
		}
		if action == "create_folder" {
			name = resolved
		} else {
			target = resolved
		}
	}
	if err := c.manageFolderExact(action, name, target); err != nil {
		return "", err
	}
	return resolved, nil
}

func resolveFolderName(name, source string, rows []Folder, personal []imap.NamespaceDescriptor) (string, error) {
	if err := folderName(name); err != nil {
		return "", err
	}
	// Explicit server paths are already unambiguous. Namespace containers
	// themselves aren't creatable mailboxes.
	for _, f := range rows {
		if !f.Selectable && f.Delimiter != "" {
			if name == f.Name {
				return "", fmt.Errorf("%q is a folder container; enter a name inside it", name)
			}
			if strings.HasPrefix(name, f.Name+f.Delimiter) {
				return name, nil
			}
		}
	}
	for _, ns := range personal {
		if ns.Prefix != "" && strings.HasPrefix(name, ns.Prefix) {
			return name, nil
		}
	}
	// Renaming a leaf keeps its parent (including Labels vs Folders).
	for _, f := range rows {
		if f.Name == source && f.Delimiter != "" && !strings.Contains(name, f.Delimiter) {
			if i := strings.LastIndex(source, f.Delimiter); i >= 0 {
				return source[:i+len(f.Delimiter)] + name, nil
			}
		}
	}
	// Proton Bridge advertises separate non-selectable Folders and Labels
	// containers. A generic "create folder" belongs to Folders, never Labels.
	folders, labels := "", ""
	for _, f := range rows {
		if !f.Selectable && f.Delimiter != "" {
			switch f.Name {
			case "Folders":
				folders = f.Name + f.Delimiter
			case "Labels":
				labels = f.Name + f.Delimiter
			}
		}
	}
	if folders != "" && labels != "" {
		return folders + name, nil
	}
	if len(personal) == 1 && personal[0].Prefix != "" {
		return personal[0].Prefix + name, nil
	}
	// Flat mailboxes and ambiguous layouts keep the caller's exact name.
	return name, nil
}
