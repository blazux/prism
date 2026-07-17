package rag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every format lands in the same index, so every format must get the same
// repair. DOCX and TXT rarely hyphenate, but they carry curly quotes and
// non-breaking spaces straight from the authoring tool — a query typed on a
// keyboard would never match them.
func TestParseFileNormalisesEveryTextFormat(t *testing.T) {
	dir := t.TempDir()
	raw := "it’s a “quoted” value here, soft­hyphen, refer‐\nence"
	want := `it's a "quoted" value here, softhyphen, reference`

	for _, ext := range []string{".txt", ".md", ".csv", ".json"} {
		t.Run(ext, func(t *testing.T) {
			p := filepath.Join(dir, "doc"+ext)
			if err := os.WriteFile(p, []byte(raw), 0644); err != nil {
				t.Fatal(err)
			}
			got, err := ParseFile(p)
			if err != nil {
				t.Fatalf("ParseFile: %v", err)
			}
			if got != want {
				t.Errorf("%s not normalised:\n got  %q\n want %q", ext, got, want)
			}
		})
	}
}

// Applying the repair twice must not change the text: ParseFile normalises, and
// a caller may normalise again without harm.
func TestNormalizeExtractedTextIsIdempotent(t *testing.T) {
	in := "refer‐\nence and real‐time and it’s"
	once := NormalizeExtractedText(in)
	twice := NormalizeExtractedText(once)
	if once != twice {
		t.Errorf("not idempotent:\n once  %q\n twice %q", once, twice)
	}
}

// The paginationless formats get the same hygiene as PDFs: no 2-character
// chunks polluting the vector index.
func TestSplitDocumentDropsTinyFragments(t *testing.T) {
	text := strings.Repeat("real content about the session usage meter. ", 60) + "\n\n7\n\nii"
	chunks, pageNums := SplitDocument(text)
	if len(chunks) != len(pageNums) {
		t.Fatalf("length mismatch: %d chunks, %d pages", len(chunks), len(pageNums))
	}
	for i, c := range chunks {
		if len([]rune(c)) < MinChunkChars {
			t.Errorf("chunk %d kept at %d chars: %q", i, len([]rune(c)), c)
		}
		if pageNums[i] != 0 {
			t.Errorf("chunk %d reports page %d; paginationless formats have none", i, pageNums[i])
		}
	}
	if len(chunks) == 0 {
		t.Fatal("real content dropped")
	}
}

// A short note (a CSV of three lines, a one-paragraph memo) must still index.
func TestSplitDocumentKeepsShortDocument(t *testing.T) {
	chunks, _ := SplitDocument("Backup runs at 03:00.")
	if len(chunks) != 1 {
		t.Fatalf("short document dropped: %v", chunks)
	}
}
