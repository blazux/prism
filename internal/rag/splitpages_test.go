package rag

import "strings"

import "testing"

// A paragraph running across a page break used to be cut in two, because each
// page was split in isolation. Now the document is split as one text.
func TestSplitPagesJoinsAcrossPageBreak(t *testing.T) {
	// One sentence, split by the PDF across pages 1 and 2.
	pages := []string{
		strings.Repeat("a", 200) + " the optical link budget is computed from",
		"the receiver sensitivity and the fibre attenuation. " + strings.Repeat("b", 200),
	}
	chunks, pageNums := SplitPages(pages)
	if len(chunks) != len(pageNums) {
		t.Fatalf("chunks/pages length mismatch: %d vs %d", len(chunks), len(pageNums))
	}
	joined := strings.Join(chunks, " ")
	if !strings.Contains(joined, "computed from") || !strings.Contains(joined, "the receiver sensitivity") {
		t.Fatalf("sentence lost across the page break: %q", joined)
	}
	// Both fragments fit in one chunk of 1800 chars: the sentence must be whole.
	if len(chunks) != 1 {
		t.Errorf("expected the two short pages to merge into 1 chunk, got %d", len(chunks))
	}
	if pageNums[0] != 1 {
		t.Errorf("chunk starts on page %d, want 1", pageNums[0])
	}
}

// Title pages and running headers produced 2-character chunks — noise that
// competes with real content in the vector index.
func TestSplitPagesDropsTinyFragments(t *testing.T) {
	pages := []string{
		"7", // a page number alone
		strings.Repeat("real content about the session usage meter. ", 60),
		"ii", // roman numeral
	}
	chunks, pageNums := SplitPages(pages)
	for i, c := range chunks {
		if len([]rune(c)) < MinChunkChars {
			t.Errorf("chunk %d kept despite being %d chars: %q", i, len([]rune(c)), c)
		}
	}
	if len(chunks) == 0 {
		t.Fatal("the real content was dropped too")
	}
	_ = pageNums
}

// A document that is entirely short must still be indexed.
func TestSplitPagesKeepsShortDocument(t *testing.T) {
	chunks, pageNums := SplitPages([]string{"Backup runs at 03:00."})
	if len(chunks) != 1 || pageNums[0] != 1 {
		t.Fatalf("short doc dropped: chunks=%v pages=%v", chunks, pageNums)
	}
}

// The page number is what lets the agent cite its source: it must follow the
// content, not the chunk index.
func TestSplitPagesAttributesTheRightPage(t *testing.T) {
	long := func(marker string) string {
		return marker + " " + strings.Repeat("padding words to fill the page. ", 70)
	}
	pages := []string{long("ALPHA"), long("BRAVO"), long("CHARLIE")}
	chunks, pageNums := SplitPages(pages)
	for i, c := range chunks {
		switch {
		case strings.Contains(c, "ALPHA") && pageNums[i] != 1:
			t.Errorf("chunk %d holds ALPHA but reports page %d", i, pageNums[i])
		case strings.Contains(c, "CHARLIE") && pageNums[i] < 2:
			t.Errorf("chunk %d holds CHARLIE but reports page %d", i, pageNums[i])
		}
	}
}
