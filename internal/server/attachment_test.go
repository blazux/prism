package server

import (
	"strings"
	"testing"
)

// The preamble is what every channel hands the agent about a file. These pin the
// branch each file kind takes — a regression here misroutes the agent (tells it
// to read a pcap as text, or claims a page needs a server).
func TestAttachmentPreamble(t *testing.T) {
	cases := []struct {
		name string
		att  attachment
		want []string // substrings that must appear
	}{
		{
			"inlined text wins",
			attachment{Name: "note.txt", Text: "hello there", Path: "uploads/note.txt"},
			[]string{"note.txt", "hello there"},
		},
		{
			"pcap points at the tool",
			attachment{Name: "call.pcap", Text: "", Path: "uploads/call.pcap"},
			[]string{"network capture", "`pcap` tool", "mode=calls"},
		},
		{
			"html says render, not server",
			attachment{Name: "flow.html", Text: "", Path: "uploads/flow.html"},
			[]string{"never say you need a server", "browser_get", "file:///workspace/uploads/flow.html"},
		},
		{
			"unknown binary, generic open",
			attachment{Name: "blob.bin", Text: "", Path: "uploads/blob.bin"},
			[]string{"read the file at uploads/blob.bin"},
		},
		{
			"nothing extracted and not saved",
			attachment{Name: "x.dat", Text: "", Path: ""},
			[]string{"No text could be extracted"},
		},
	}
	for _, c := range cases {
		got := attachmentPreamble(c.att)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: preamble missing %q\n---\n%s", c.name, w, got)
			}
		}
	}
}

// prependAttachments must stack files ahead of the user's text, so the model sees
// the files then the question — and multiple files all appear.
func TestPrependAttachments(t *testing.T) {
	out := prependAttachments("what is in these?", []attachment{
		{Name: "a.pcap", Path: "uploads/a.pcap"},
		{Name: "b.txt", Text: "second file body", Path: "uploads/b.txt"},
	})
	for _, w := range []string{"a.pcap", "b.txt", "second file body", "what is in these?"} {
		if !strings.Contains(out, w) {
			t.Errorf("prepend dropped %q", w)
		}
	}
	if strings.Index(out, "what is in these?") < strings.Index(out, "a.pcap") {
		t.Error("user text should come after the attachments")
	}
}
