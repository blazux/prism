package server

import (
	"strings"
	"testing"
)

// rag.ParseFile reads any unknown extension whole and returns the bytes as a
// string. This predicate is the only thing standing between a pcap and 100 000
// characters of noise in the model's prompt — and between the file and the
// workspace, since a non-empty text means "don't bother saving it".
func TestLooksBinary(t *testing.T) {
	// A real libpcap header: magic, version, then a NUL-heavy thiszone field.
	pcap := "\xd4\xc3\xb2\xa1\x02\x00\x04\x00\x00\x00\x00\x00\x00\x00\x00\x00"

	binary := map[string]string{
		"pcap header":     pcap,
		"zip magic":       "PK\x03\x04\x14\x00\x00\x00",
		"elf executable":  "\x7fELF\x02\x01\x01\x00",
		"lone NUL":        "text with a \x00 inside",
		"invalid utf-8":   "caf\xe9 latin-1", // é in latin-1, not valid UTF-8
		"NUL past 8 KiB?": strings.Repeat("a", 100) + "\x00",
	}
	for name, s := range binary {
		if !looksBinary(s) {
			t.Errorf("looksBinary(%s) = false, want true", name)
		}
	}

	textual := map[string]string{
		"empty":          "",
		"ascii":          "INVITE sip:+33@example.com SIP/2.0\r\n",
		"utf-8 accents":  "café, naïve, 日本語",
		"emoji":          "ok 👍",
		"csv":            "a,b,c\n1,2,3\n",
		"long utf-8 doc": strings.Repeat("héllo wörld ", 2000), // > 8 KiB, valid
	}
	for name, s := range textual {
		if looksBinary(s) {
			t.Errorf("looksBinary(%s) = true, want false", name)
		}
	}
}

// A multi-byte rune straddling the 8 KiB cut must not be mistaken for a binary
// file: the head would end mid-rune and fail utf8.ValidString.
func TestLooksBinary_RuneAcrossHeadBoundary(t *testing.T) {
	// Land the second byte of "é" exactly at offset 8192.
	s := strings.Repeat("a", 8191) + "é" + strings.Repeat("b", 100)
	if looksBinary(s) {
		t.Error("a valid UTF-8 document split mid-rune at 8 KiB was called binary")
	}
}
