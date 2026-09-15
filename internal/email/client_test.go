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

// A "\r\n" in a caller-supplied header value (subject, recipient, reply-id)
// must never reach the wire raw — it would inject arbitrary extra MIME
// headers into the outgoing message. Send() runs every such value through
// this before building the header block.
func TestSanitizeHeaderVal_StripsCRLF(t *testing.T) {
	in := "Legit subject\r\nBcc: attacker@evil.example\r\nX-Injected: yes"
	got := sanitizeHeaderVal(in)
	if strings.ContainsAny(got, "\r\n") {
		t.Fatalf("sanitizeHeaderVal left a CR/LF in the result: %q", got)
	}
	if !strings.Contains(got, "Legit subject") {
		t.Fatalf("sanitizeHeaderVal should preserve the legitimate text, got %q", got)
	}
}

// sendImplicitTLS (port 465) must honor Config.Insecure the same way
// sendStartTLS already does — both should build the tls.Config via the same
// helper, not one bypassing it with a bare &tls.Config{}.
func TestTLSConfig_HonorsInsecure(t *testing.T) {
	secure := Config{SMTPHost: "smtp.example.com"}.tlsConfig("smtp.example.com")
	if secure.InsecureSkipVerify {
		t.Error("InsecureSkipVerify should be false when Config.Insecure is unset")
	}
	insecure := Config{SMTPHost: "smtp.example.com", Insecure: true}.tlsConfig("smtp.example.com")
	if !insecure.InsecureSkipVerify {
		t.Error("InsecureSkipVerify should be true when Config.Insecure is set (e.g. Proton Mail Bridge's self-signed cert)")
	}
}

func TestParseBodyMIME(t *testing.T) {
	raw := "Content-Type: text/plain; charset=utf-8\r\n\r\nHello world"
	if got, _ := parseBody([]byte(raw)); !strings.Contains(got, "Hello world") {
		t.Errorf("parseBody = %q", got)
	}
}

// Ordinary French mail is often iso-8859-1 or windows-1252. The library has no
// charset decoder unless one is registered, and its "unknown charset" error is
// documented as non-fatal — but it was treated as fatal, so the whole message
// was abandoned and the raw MIME source was shown as the body.
func TestLatin1MessageIsDecodedNotDumpedRaw(t *testing.T) {
	body := []byte("Subject: test\r\nContent-Type: text/plain; charset=iso-8859-1\r\n\r\nR\xe9union pr\xe9vue \xe0 9h.\r\n")
	text, atts := parseBody(body)
	if strings.Contains(text, "Content-Type:") || strings.Contains(text, "Subject:") {
		t.Fatalf("raw source returned as the body: %q", text)
	}
	if !strings.Contains(text, "Réunion prévue à 9h") {
		t.Fatalf("accents lost: %q", text)
	}
	if len(atts) != 0 {
		t.Fatalf("unexpected attachments: %+v", atts)
	}
}

// A part in an unusual charset used to end the walk, so everything after it —
// including the attachments — disappeared from the message.
func TestAPartInAnOddCharsetDoesNotHideTheRest(t *testing.T) {
	raw := "MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=b\r\n\r\n" +
		"--b\r\nContent-Type: text/plain; charset=x-inconnu-9000\r\n\r\nbonjour\r\n" +
		"--b\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nsuite du message\r\n" +
		"--b\r\nContent-Type: application/pdf\r\n" +
		"Content-Disposition: attachment; filename=\"facture.pdf\"\r\n\r\n%PDF-1.4\r\n" +
		"--b--\r\n"
	text, atts := parseBody([]byte(raw))
	if !strings.Contains(text, "suite du message") {
		t.Errorf("the part after the odd one was dropped: %q", text)
	}
	if len(atts) != 1 || atts[0].Filename != "facture.pdf" {
		t.Errorf("the attachment after the odd part vanished: %+v", atts)
	}
}
