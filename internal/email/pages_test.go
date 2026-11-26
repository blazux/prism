package email

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

func TestUnreadFilteringAndSearchPaginationUseTheWholeFolder(t *testing.T) {
	var connections atomic.Int64
	c := testMailbox(t, false, func() { connections.Add(1) })
	cl, err := c.dial()
	if err != nil {
		t.Fatal(err)
	}
	var ids []uint32
	seen := imap.UIDSet{}
	for i := 0; i < 12; i++ {
		subject := "other"
		if i < 9 && i != 3 {
			subject = "needle"
		}
		date := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC).Add(-time.Duration(i) * time.Hour)
		body := fmt.Sprintf("From: robot@example.org\r\nReply-To: Service <support@example.org>, autre@example.org\r\nTo: me@example.org\r\nSubject: %s %d\r\nDate: %s\r\n\r\nFixture\r\n", subject, i, date.Format(time.RFC1123Z))
		cmd := cl.Append("INBOX", int64(len(body)), nil)
		if _, err = cmd.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
		if err = cmd.Close(); err != nil {
			t.Fatal(err)
		}
		added, err := cmd.Wait()
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, uint32(added.UID))
		if i != 4 && i != 7 && i != 10 {
			seen.AddNum(added.UID)
		}
	}
	if _, err = cl.Select("INBOX", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	if err = cl.Store(seen, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagSeen}}, nil).Close(); err != nil {
		t.Fatal(err)
	}
	cl.Close()
	connections.Store(0)
	first, err := c.ListPage(3)
	if err != nil || len(first.Messages) != 3 || first.UnreadCount != 3 || first.Total != 12 || first.FolderTotal != 12 || first.NextOffset == nil || *first.NextOffset != 3 {
		t.Fatalf("all page: %+v err=%v", first, err)
	}
	for _, message := range first.Messages {
		if !message.Seen {
			t.Fatal("the all-message first page should contain no unread messages")
		}
	}
	if connections.Load() != 1 {
		t.Fatalf("page opened %d IMAP connections", connections.Load())
	}
	c.UnreadOnly = true
	for offset, want := range []uint32{ids[4], ids[7], ids[10]} {
		c.Offset = offset
		page, err := c.ListPage(1)
		if err != nil || page.Total != 3 || page.UnreadCount != 3 || len(page.Messages) != 1 || page.Messages[0].UID != want || page.Messages[0].Seen {
			t.Fatalf("unread offset %d: %+v err=%v", offset, page, err)
		}
		if offset == 2 && page.NextOffset != nil {
			t.Fatal("last unread page has a next page")
		}
	}
	c.Offset = 1
	page, err := c.SearchPage("needle", 1)
	if err != nil || page.Total != 2 || page.UnreadCount != 2 || page.FolderTotal != 12 || len(page.Messages) != 1 || page.Messages[0].UID != ids[7] || page.NextOffset != nil {
		t.Fatalf("unread search page: %+v err=%v", page, err)
	}
	c.Offset = 50
	page, err = c.SearchPage("needle", 1)
	if err != nil || page.Total != 2 || len(page.Messages) != 0 || page.NextOffset != nil {
		t.Fatalf("past last search page: %+v %v", page, err)
	}
	c.UnreadOnly, c.Offset = false, 0
	page, err = c.SearchPage("needle", 2)
	if err != nil || page.Total != 8 || page.UnreadCount != 2 || len(page.Messages) != 2 {
		t.Fatalf("all search counts: %+v %v", page, err)
	}
	assertStatus := func(wantUnread uint32) {
		t.Helper()
		connections.Store(0)
		status, err := c.FolderStatus()
		if err != nil || status.Messages != 12 || status.Unread != wantUnread || status.UIDNext == 0 || status.UIDValidity == 0 {
			t.Fatalf("status (want %d unread): %+v %v", wantUnread, status, err)
		}
		if connections.Load() != 1 {
			t.Fatalf("status opened %d IMAP connections", connections.Load())
		}
	}
	// Listing, searching and STATUS leave the unread messages untouched.
	assertStatus(3)
	message, err := c.Read(ids[4])
	if err != nil || message.ReplyTo != "support@example.org, autre@example.org" {
		t.Fatalf("Reply-To round trip: %+v %v", message, err)
	}
	// Reading the body marks this message as seen; STATUS must reflect it.
	assertStatus(2)
}

func TestReplyToRejectsMalformedEnvelopeAddresses(t *testing.T) {
	message := toMessage(&imapclient.FetchMessageBuffer{Envelope: &imap.Envelope{ReplyTo: []imap.Address{{Mailbox: "bad\r\nBcc:other", Host: "example.org"}}}})
	if message.ReplyTo != "" {
		t.Fatal("unsafe Reply-To reached the client")
	}
}

func TestSearchPreservesLimitsAboveFiveHundred(t *testing.T) {
	c := testMailbox(t, false)
	cl, err := c.dial()
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	const count = 501
	for i := 0; i < count; i++ {
		body := fmt.Sprintf("From: robot@example.test\r\nTo: me@example.test\r\nSubject: needle %d\r\nDate: Thu, 26 Nov 2026 12:00:00 +0000\r\n\r\nFixture\r\n", i)
		append := cl.Append("INBOX", int64(len(body)), nil)
		if _, err = append.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
		if err = append.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err = append.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	cl.Close()
	page, err := c.SearchPage("needle", count)
	if err != nil || len(page.Messages) != count || page.Total != count || page.Limit != count || page.NextOffset != nil {
		t.Fatalf("search was silently capped: count=%d total=%d limit=%d next=%v err=%v", len(page.Messages), page.Total, page.Limit, page.NextOffset, err)
	}
	// The legacy Search signature keeps ignoring Config.Offset, as before.
	c.Offset = 1
	messages, err := c.Search("needle", count)
	if err != nil || len(messages) != count {
		t.Fatalf("legacy search lost results: count=%d err=%v", len(messages), err)
	}
	// A very large requested limit with a nonzero offset must not overflow.
	page, err = c.SearchPage("needle", int(^uint(0)>>1))
	if err != nil || len(page.Messages) != count-1 || page.NextOffset != nil {
		t.Fatalf("large search page: count=%d next=%v err=%v", len(page.Messages), page.NextOffset, err)
	}
	c.Offset = 0
	page, err = c.ListPage(count)
	if err != nil || len(page.Messages) != 500 || page.Limit != 500 || page.NextOffset == nil || *page.NextOffset != 500 {
		t.Fatalf("list bound changed: count=%d limit=%d next=%v err=%v", len(page.Messages), page.Limit, page.NextOffset, err)
	}
}

func TestMailboxIdentityGuardsDoNotIncludePasswordsOrTransport(t *testing.T) {
	c := Config{IMAPHost: "imap.example.org", SMTPHost: "smtp.example.org", IMAPPort: 993, SMTPPort: 587, User: "me@example.org", Pass: "secret"}
	want := c.AccountFingerprint()
	c.Pass, c.Security, c.Insecure = "rotated", "starttls", true
	if c.AccountFingerprint() != want {
		t.Fatal("password rotation or TLS settings changed mailbox identity")
	}
	if c.CheckAccount("") != nil || c.CheckAccount(want) != nil {
		t.Fatal("existing or matching callers rejected")
	}
	c.User = "different@example.org"
	if !errors.Is(c.CheckAccount(want), ErrAccountChanged) {
		t.Fatal("old view accepted against a different mailbox")
	}
	encoded, err := json.Marshal(FolderStatus{HighestModSeq: 9007199254740993})
	if err != nil || !strings.Contains(string(encoded), `"highest_modseq":"9007199254740993"`) {
		t.Fatal("modseq loses precision in JavaScript", string(encoded), err)
	}
}
