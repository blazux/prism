package email

import (
	"fmt"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"strings"
)

func (c Config) mailbox() string {
	if c.Folder == "" {
		return "INBOX"
	}
	return c.Folder
}

type Folder struct {
	Name       string `json:"name"`
	Role       string `json:"role,omitempty"`
	Selectable bool   `json:"selectable"`
	Delimiter  string `json:"delimiter,omitempty"`
}

func (c Config) Folders() ([]Folder, error) {
	cl, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer cl.Close()
	return listFolders(cl)
}
func listFolders(cl *imapclient.Client) ([]Folder, error) {
	opts := &imap.ListOptions{}
	if cl.Caps().Has(imap.CapSpecialUse) {
		opts.ReturnSpecialUse = true
	}
	rows, err := cl.List("", "*", opts).Collect()
	if err != nil {
		return nil, err
	}
	out := make([]Folder, 0, len(rows))
	for _, r := range rows {
		f := Folder{Name: r.Mailbox, Selectable: true}
		if r.Delim != 0 {
			f.Delimiter = string(r.Delim)
		}
		if strings.EqualFold(r.Mailbox, "INBOX") {
			f.Role = "inbox"
		}
		for _, a := range r.Attrs {
			switch strings.ToLower(string(a)) {
			case "\\noselect":
				f.Selectable = false
			case "\\sent":
				f.Role = "sent"
			case "\\drafts":
				f.Role = "drafts"
			case "\\trash":
				f.Role = "trash"
			case "\\archive":
				f.Role = "archive"
			case "\\junk":
				f.Role = "junk"
			}
		}
		out = append(out, f)
	}
	return out, nil
}
func folderName(name string) error {
	if strings.TrimSpace(name) == "" || strings.ContainsAny(name, "\r\n\x00") {
		return fmt.Errorf("a valid folder name is required")
	}
	return nil
}
func (c Config) manageFolderExact(action, name, target string) error {
	if err := folderName(name); err != nil {
		return err
	}
	if action != "create_folder" && strings.EqualFold(name, "INBOX") {
		return fmt.Errorf("INBOX cannot be renamed or deleted")
	}
	cl, err := c.dial()
	if err != nil {
		return err
	}
	defer cl.Close()
	switch action {
	case "create_folder":
		return cl.Create(name, nil).Wait()
	case "rename_folder":
		if err := folderName(target); err != nil {
			return err
		}
		return cl.Rename(name, target, nil).Wait()
	case "delete_folder":
		rows, err := listFolders(cl)
		if err != nil {
			return err
		}
		for _, f := range rows {
			if f.Name == name && f.Role != "" {
				return fmt.Errorf("system folder %q cannot be deleted here", name)
			}
			if f.Delimiter != "" && strings.HasPrefix(f.Name, name+f.Delimiter) {
				return fmt.Errorf("move or delete subfolders first")
			}
		}
		st, err := cl.Status(name, &imap.StatusOptions{NumMessages: true}).Wait()
		if err != nil {
			return err
		}
		if st.NumMessages == nil || *st.NumMessages != 0 {
			return fmt.Errorf("folder is not empty; move its messages before deleting it")
		}
		return cl.Delete(name).Wait()
	}
	return fmt.Errorf("unknown folder action")
}
func (c Config) SpecialFolder(role string) (string, error) {
	fs, err := c.Folders()
	if err != nil {
		return "", err
	}
	for _, f := range fs {
		if f.Role == role && f.Selectable {
			return f.Name, nil
		}
	}
	names := map[string][]string{"archive": {"Archive", "Archives"}, "trash": {"Trash", "Deleted Items", "Deleted Messages"}}
	for _, f := range fs {
		for _, n := range names[role] {
			if f.Selectable && strings.EqualFold(f.Name, n) {
				return f.Name, nil
			}
		}
	}
	return "", fmt.Errorf("no %s folder advertised by this mailbox; create a folder and use move with its exact name", role)
}
func (c Config) Move(uid uint32, target string) error {
	if uid == 0 {
		return fmt.Errorf("uid from email list is required")
	}
	if err := folderName(target); err != nil {
		return err
	}
	if target == c.mailbox() {
		return fmt.Errorf("source and destination are the same")
	}
	cl, err := c.dial()
	if err != nil {
		return err
	}
	defer cl.Close()
	if _, err = cl.Select(c.mailbox(), nil).Wait(); err != nil {
		return err
	}
	return moveUID(cl, imap.UID(uid), target)
}

// The library's fallback pipelines COPY and deletion. Wait for COPY to succeed
// before deleting, and never EXPUNGE unrelated messages in a shared mailbox.
func moveUID(cl *imapclient.Client, uid imap.UID, target string) error {
	set := imap.UIDSetNum(uid)
	msgs, err := cl.Fetch(set, &imap.FetchOptions{UID: true}).Collect()
	if err != nil {
		return err
	}
	if len(msgs) == 0 {
		return fmt.Errorf("message no longer exists; refresh the folder")
	}
	if cl.Caps().Has(imap.CapMove) {
		_, err = cl.Move(set, target).Wait()
		return err
	}
	if !cl.Caps().Has(imap.CapUIDPlus) {
		return fmt.Errorf("this server needs MOVE or UIDPLUS to move messages safely")
	}
	if _, err = cl.Copy(set, target).Wait(); err != nil {
		return err
	}
	if err = cl.Store(set, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); err != nil {
		return fmt.Errorf("copied but could not remove source: %w", err)
	}
	return cl.UIDExpunge(set).Close()
}
