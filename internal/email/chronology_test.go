package email

import (
	"fmt"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
)

func TestMailboxChronologyAcrossTimezonesAndImports(t *testing.T) {
	c := testMailbox(t, false)
	cl, err := c.dial()
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	var ids []uint32
	// Append newest first so import/UID order disagrees with chronological order.
	for i, date := range []string{"Thu, 15 Oct 2026 04:41:59 -0700", "Thu, 15 Oct 2026 06:28:32 +0000", "Wed, 14 Oct 2026 10:00:00 +0200"} {
		body := fmt.Sprintf("From: news@example.org\r\nTo: me@example.org\r\nSubject: needle %d\r\nDate: %s\r\n\r\nFixture\r\n", i, date)
		cmd := cl.Append("INBOX", int64(len(body)), nil)
		if _, err := cmd.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Close(); err != nil {
			t.Fatal(err)
		}
		appended, err := cmd.Wait()
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, uint32(appended.UID))
	}
	rows, err := c.List(2)
	if err != nil || len(rows) != 2 {
		t.Fatalf("list: %v %v", rows, err)
	}
	if rows[0].UID != ids[0] || rows[1].UID != ids[1] {
		t.Fatalf("list used UID or textual dates: %v", rows)
	}
	c.Offset = 1
	page, err := c.List(1)
	if err != nil || len(page) != 1 || page[0].UID != ids[1] {
		t.Fatalf("page: %v %v", page, err)
	}
	rows, err = c.Search("needle", 1)
	if err != nil || len(rows) != 1 || rows[0].UID != ids[0] {
		t.Fatalf("search limit applied before chronological sorting: %v %v", rows, err)
	}
}

func TestMissingHeaderDateUsesInternalDateInListReadAndSearch(t *testing.T) {
	c := testMailbox(t, false)
	cl, err := c.dial()
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	want := time.Date(2026, 10, 15, 11, 41, 59, 0, time.UTC)
	body := "From: fixture@example.org\r\nTo: me@example.org\r\nSubject: needle missing date\r\n\r\nFixture\r\n"
	cmd := cl.Append("INBOX", int64(len(body)), &imap.AppendOptions{Time: want})
	if _, err := cmd.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Close(); err != nil {
		t.Fatal(err)
	}
	appended, err := cmd.Wait()
	if err != nil {
		t.Fatal(err)
	}
	for name, read := range map[string]func() ([]Message, error){
		"list":   func() ([]Message, error) { return c.List(1) },
		"search": func() ([]Message, error) { return c.Search("needle", 1) },
		"read":   func() ([]Message, error) { m, err := c.Read(uint32(appended.UID)); return []Message{m}, err },
	} {
		rows, err := read()
		if err != nil || len(rows) != 1 || !rows[0].Date.Equal(want) {
			t.Fatalf("%s lost internal date: %v %v", name, rows, err)
		}
	}
}
