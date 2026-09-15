package rag

import "testing"

// pdftotext hands back the typesetter's hyphenation: 145 of the 295 chunks of a
// 183-page manual contained a word broken across lines ("refer‐\nence"). The
// embedder then sees two non-words and no query can ever match the passage.
func TestNormalizeExtractedTextRejoinsHyphenatedWords(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"plain hyphenation", "quick refer‐\nence.", "quick reference."},
		{"indented continuation", "ele‐\n   ments", "elements"},
		{"windows line ending", "plat‐\r\nforms", "platforms"},
		{"capitalised word", "Time‐\nstamp", "Timestamp"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeExtractedText(tt.in); got != tt.want {
				t.Errorf("NormalizeExtractedText(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// The trap: 93 chunks of the same manual carry a *legitimate* hyphen inside a
// compound word. Rejoining those would produce "realtime" and break the search
// just as badly. Only a hyphen followed by a line break is hyphenation.
func TestNormalizeExtractedTextKeepsCompoundWords(t *testing.T) {
	tests := []struct{ in, want string }{
		{"real‐time monitoring", "real-time monitoring"},
		{"user‐defined limits", "user-defined limits"},
		{"a plain-ascii-hyphen stays", "a plain-ascii-hyphen stays"},
		// A hyphen ending a line before a *capital* is not hyphenation either
		// (e.g. a list item "Foo‐\nBar" is two entries).
		{"Foo‐\nBar", "Foo-\nBar"},
	}
	for _, tt := range tests {
		if got := NormalizeExtractedText(tt.in); got != tt.want {
			t.Errorf("NormalizeExtractedText(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestNormalizeExtractedTextTypography(t *testing.T) {
	in := "it’s “quoted”, non breaking, soft­hyphen, ﬁle"
	want := `it's "quoted", non breaking, softhyphen, file`
	if got := NormalizeExtractedText(in); got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

// A malformed document used to be indexed truncated and reported as a success,
// so the agent answered from half a file without knowing it.
func TestTruncatedOfficeXMLIsReportedNotSwallowed(t *testing.T) {
	good := []byte(`<?xml version="1.0"?><w:document xmlns:w="x"><w:p><w:t>bonjour</w:t></w:p></w:document>`)
	text, err := extractDocxText(good)
	if err != nil || text != "bonjour" {
		t.Fatalf("well-formed document: %q err=%v", text, err)
	}
	cut := []byte(`<?xml version="1.0"?><w:document xmlns:w="x"><w:p><w:t>bonjour`)
	if _, err := extractDocxText(cut); err == nil {
		t.Error("a truncated document was accepted as complete")
	}
	if _, err := extractPPTXText(cut); err == nil {
		t.Error("a truncated slide was accepted as complete")
	}
}
