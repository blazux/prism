package email

import (
	"fmt"

	"github.com/emersion/go-imap/v2"
)

// FolderStatus is cheap enough to poll without fetching mailbox envelopes.
type FolderStatus struct {
	Folder        string `json:"folder"`
	Messages      uint32 `json:"messages"`
	Unread        uint32 `json:"unread"`
	UIDNext       uint32 `json:"uid_next"`
	UIDValidity   uint32 `json:"uid_validity"`
	HighestModSeq uint64 `json:"highest_modseq,omitempty,string"`
}

func (c Config) FolderStatus() (FolderStatus, error) {
	status := FolderStatus{Folder: c.mailbox()}
	cl, err := c.dial()
	if err != nil {
		return status, err
	}
	defer cl.Close()
	data, err := cl.Status(status.Folder, &imap.StatusOptions{
		NumMessages: true, NumUnseen: true, UIDNext: true, UIDValidity: true,
		HighestModSeq: cl.Caps().Has(imap.CapCondStore),
	}).Wait()
	if err != nil {
		return status, fmt.Errorf("mailbox status: %w", err)
	}
	if data.NumMessages == nil || data.NumUnseen == nil {
		return status, fmt.Errorf("mailbox status did not include message counts")
	}
	status.Messages, status.Unread = *data.NumMessages, *data.NumUnseen
	status.UIDNext, status.UIDValidity = uint32(data.UIDNext), data.UIDValidity
	status.HighestModSeq = data.HighestModSeq
	return status, nil
}
