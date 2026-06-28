package email

import (
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
)

func TestAddr(t *testing.T) {
	if got := addr(nil); got != "" {
		t.Errorf("empty = %q", got)
	}
	got := addr([]imap.Address{{Name: "Ada", Mailbox: "ada", Host: "example.org"}})
	if got != "Ada <ada@example.org>" {
		t.Errorf("named = %q", got)
	}
	got = addr([]imap.Address{{Mailbox: "ada", Host: "example.org"}})
	if got != "ada@example.org" {
		t.Errorf("bare = %q", got)
	}
}

func TestConfigFromDefault(t *testing.T) {
	if (Config{User: "u@x.io"}).from() != "u@x.io" {
		t.Error("from should default to user")
	}
	if (Config{User: "u@x.io", From: "other@x.io"}).from() != "other@x.io" {
		t.Error("explicit from should win")
	}
}

func TestParseBodyMIME(t *testing.T) {
	raw := "Content-Type: text/plain; charset=utf-8\r\n\r\nHello world"
	if got, _ := parseBody([]byte(raw)); !strings.Contains(got, "Hello world") {
		t.Errorf("parseBody = %q", got)
	}
}
