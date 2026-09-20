package email

import (
	"strings"
	"testing"
)

func TestReadableHTMLNewsletterAfterLongStyles(t *testing.T) {
	htmlBody := "<html><head><style>" + strings.Repeat(".mail{color:red}", 900) + "</style></head><body><div hidden>Hidden preview</div><div style='display: none'>Hidden tracking</div><script>bad()</script><h1>Actual article</h1><p>Hello <strong>équipe</strong> &amp; friends.</p><p><a href='https://example.org/article?a=1&amp;b=2'>Read article</a></p><img alt='Useful diagram'><table><tr><td>One</td><td>Two</td></tr></table></body></html>"
	raw := "MIME-Version: 1.0\r\nContent-Type: text/html; charset=utf-8\r\n\r\n" + htmlBody
	body, _, isHTML := parseBodyContent([]byte(raw))
	msg := Message{Body: body, BodyIsHTML: isHTML}
	text := msg.ReadableBody()
	for _, want := range []string{"Actual article", "Hello équipe & friends.", "Read article (https://example.org/article?a=1&b=2)", "Useful diagram", "One Two"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %q", want, text)
		}
	}
	for _, bad := range []string{".mail", "Hidden", "bad()", "<style>"} {
		if strings.Contains(text, bad) {
			t.Fatalf("leaked %q", bad)
		}
	}
	if msg.Body != htmlBody {
		t.Fatal("changed HTML body used by UI")
	}
}
func TestReadablePlainTextIsNotInterpretedAsHTML(t *testing.T) {
	plain := "Hello <person@example.org>\nUse <b> as literal code.\n\n  indentation 🙂"
	body, _, isHTML := parseBodyContent([]byte("Content-Type: text/plain; charset=utf-8\r\n\r\n" + plain))
	if isHTML || (Message{Body: body, BodyIsHTML: isHTML}).ReadableBody() != plain {
		t.Fatal("plain text was altered")
	}
}
