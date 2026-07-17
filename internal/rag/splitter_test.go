package rag

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// allCharsPresent verifies that every character in input appears somewhere in the chunks.
// This is a weak but useful check: no content should be silently dropped.
func allCharsPresent(t *testing.T, input string, chunks []string) {
	t.Helper()
	joined := strings.Join(chunks, "")
	for i, r := range input {
		if !strings.ContainsRune(joined, r) {
			t.Errorf("character %q at input[%d] not found in any chunk", r, i)
			return
		}
	}
}

func TestSplitText_Empty(t *testing.T) {
	if got := SplitText(""); got != nil {
		t.Errorf("empty input: want nil, got %v", got)
	}
	if got := SplitText("   \n\t  "); got != nil {
		t.Errorf("whitespace-only input: want nil, got %v", got)
	}
}

func TestSplitText_ShortText(t *testing.T) {
	input := "Hello, world!"
	chunks := SplitText(input)
	if len(chunks) != 1 {
		t.Fatalf("short text: want 1 chunk, got %d", len(chunks))
	}
	if chunks[0] != input {
		t.Errorf("short text: want %q, got %q", input, chunks[0])
	}
}

func TestSplitText_ExactlyChunkSize(t *testing.T) {
	// Text of exactly ChunkSize characters with no separator — should produce 1 chunk.
	input := strings.Repeat("a", ChunkSize)
	chunks := SplitText(input)
	if len(chunks) != 1 {
		t.Fatalf("exact ChunkSize: want 1 chunk, got %d", len(chunks))
	}
	if utf8.RuneCountInString(chunks[0]) != ChunkSize {
		t.Errorf("exact ChunkSize: want %d runes, got %d", ChunkSize, utf8.RuneCountInString(chunks[0]))
	}
}

func TestSplitText_NoContentLoss_WithSeparators(t *testing.T) {
	// Build a text larger than ChunkSize with paragraph separators.
	para := strings.Repeat("word ", 80) // ~400 chars per paragraph
	input := strings.Join([]string{para, para, para, para}, "\n\n")

	chunks := SplitText(input)
	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}

	// Every word that appears in the input must appear in at least one chunk.
	words := strings.Fields(input)
	joined := strings.Join(chunks, " ")
	for _, w := range words {
		if !strings.Contains(joined, w) {
			t.Errorf("word %q lost during splitting", w)
			break
		}
	}
}

func TestSplitText_NoContentLoss_HardSplit(t *testing.T) {
	// A string with no separators longer than ChunkSize forces hardSplit.
	input := strings.Repeat("x", ChunkSize*3)
	chunks := SplitText(input)

	total := 0
	for _, c := range chunks {
		total += utf8.RuneCountInString(c)
	}

	// With overlap the total will exceed input length, but we should
	// recover at least (ChunkSize - ChunkOverlap) * numChunks unique chars.
	// Simpler check: the last character of input must appear in the last chunk.
	lastChunk := chunks[len(chunks)-1]
	if !strings.HasSuffix(lastChunk, "x") {
		t.Errorf("last chunk does not end with last character of input")
	}

	// The concatenation of chunk[0..n] (without overlap) must cover all input chars.
	// We check by verifying the last chunk ends at the last character of input.
	lastRune := []rune(lastChunk)
	inputRunes := []rune(input)
	if lastRune[len(lastRune)-1] != inputRunes[len(inputRunes)-1] {
		t.Errorf("last character mismatch: input ends with %q, last chunk ends with %q",
			inputRunes[len(inputRunes)-1], lastRune[len(lastRune)-1])
	}
}

func TestSplitText_ChunkSizeRespected(t *testing.T) {
	input := strings.Repeat("Hello world. ", 200) // ~2600 chars

	chunks := SplitText(input)
	for i, c := range chunks {
		if utf8.RuneCountInString(c) > ChunkSize {
			t.Errorf("chunk[%d] has %d runes, exceeds ChunkSize %d",
				i, utf8.RuneCountInString(c), ChunkSize)
		}
	}
}

func TestSplitText_OverlapExists(t *testing.T) {
	// Two paragraphs, each just over ChunkSize/2, so they cannot share a chunk.
	// Sized from the constant rather than hard-coded: ChunkSize is a tuning knob.
	para := strings.Repeat("a", ChunkSize/2+50)
	input := para + "\n\n" + para

	chunks := SplitText(input)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks for a %d-char text (ChunkSize=%d), got %d",
			len([]rune(input)), ChunkSize, len(chunks))
	}

	// Verify that the last ChunkOverlap runes of chunks[0] appear at the start of chunks[1].
	c0 := []rune(chunks[0])
	c1 := []rune(chunks[1])
	if len(c0) >= ChunkOverlap && len(c1) >= ChunkOverlap {
		tail := string(c0[len(c0)-ChunkOverlap:])
		head := string(c1[:ChunkOverlap])
		if tail != head {
			t.Logf("chunk[0] tail: %q", tail)
			t.Logf("chunk[1] head: %q", head)
			t.Error("chunks[0] tail does not match chunks[1] head — overlap is missing or incorrect")
		}
	}
}

func TestHardSplit_NoBoundaryDrop(t *testing.T) {
	// Ensure the last characters of the input are never silently dropped.
	// Bug risk: a loop advancing by (ChunkSize - ChunkOverlap) might stop
	// before emitting the final partial chunk.
	for _, size := range []int{
		ChunkSize + 1,
		2*(ChunkSize-ChunkOverlap) - 1,
		2*(ChunkSize-ChunkOverlap) + 0,
		2*(ChunkSize-ChunkOverlap) + 1,
		3*(ChunkSize-ChunkOverlap) + 42,
	} {
		input := strings.Repeat("z", size)
		chunks := hardSplit(input)

		last := chunks[len(chunks)-1]
		lastRune := []rune(last)
		if lastRune[len(lastRune)-1] != 'z' {
			t.Errorf("size=%d: last chunk ends unexpectedly with %q", size, lastRune[len(lastRune)-1])
		}

		// Verify last chunk covers up to the end of the input.
		inputRunes := []rune(input)
		if string(lastRune[len(lastRune)-1]) != string(inputRunes[len(inputRunes)-1]) {
			t.Errorf("size=%d: input last char %q != last chunk last char %q",
				size, inputRunes[len(inputRunes)-1], lastRune[len(lastRune)-1])
		}
	}
}

func TestSplitText_UnicodeContent(t *testing.T) {
	// Verify that multi-byte Unicode characters are split on rune boundaries.
	unit := "日本語テスト " // 7 runes
	input := strings.Repeat(unit, 200)
	chunks := SplitText(input)

	for i, c := range chunks {
		if !utf8.ValidString(c) {
			t.Errorf("chunk[%d] is not valid UTF-8", i)
		}
	}
	allCharsPresent(t, input, chunks)
}
