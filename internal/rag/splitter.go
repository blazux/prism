package rag

import "strings"

const (
	// ChunkSize is in characters. The embedder (Qwen3-Embedding-8B) accepts 8192
	// tokens ≈ 30k characters, so 1800 stays far from the limit while giving a
	// chunk enough context to stand on its own. At the previous 1000 the average
	// chunk of a 183-page manual was 625 characters — barely a paragraph.
	ChunkSize    = 1800
	ChunkOverlap = 250

	// MinChunkChars drops fragments too small to carry meaning: page numbers,
	// running headers, a lone section title. A 183-page manual produced 15 such
	// chunks, one of them 2 characters long — pure noise in the vector index.
	MinChunkChars = 80
)

// separators tried in order, from coarse to fine
var separators = []string{"\n\n", "\n", ". ", ", ", "\t", " "}

// SplitText splits text into overlapping chunks using a recursive strategy:
// it tries separators from coarsest to finest, recursing when a piece is still
// too large. Falls back to hard character-level split as a last resort.
func SplitText(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	return splitRecursive(text, separators)
}

func splitRecursive(text string, seps []string) []string {
	if len([]rune(text)) <= ChunkSize {
		if t := strings.TrimSpace(text); t != "" {
			return []string{t}
		}
		return nil
	}

	for i, sep := range seps {
		if !strings.Contains(text, sep) {
			continue
		}

		parts := strings.Split(text, sep)
		if len(parts) == 1 {
			continue
		}

		var result []string
		var current strings.Builder

		flush := func() {
			if chunk := strings.TrimSpace(current.String()); chunk != "" {
				result = append(result, chunk)
			}
			current.Reset()
		}

		addOverlap := func() {
			if ChunkOverlap > 0 && len(result) > 0 {
				last := []rune(result[len(result)-1])
				if len(last) > ChunkOverlap {
					current.WriteString(string(last[len(last)-ChunkOverlap:]))
				} else {
					current.WriteString(string(last))
				}
			}
		}

		for _, part := range parts {
			addedLen := len([]rune(current.String()))
			if addedLen > 0 {
				addedLen += len([]rune(sep))
			}
			addedLen += len([]rune(part))

			if current.Len() > 0 && addedLen > ChunkSize {
				flush()
				addOverlap()
			}

			if len([]rune(part)) > ChunkSize {
				// Single part too large — recurse with next separators
				flush()
				result = append(result, splitRecursive(part, seps[i+1:])...)
				continue
			}

			if current.Len() > 0 {
				current.WriteString(sep)
			}
			current.WriteString(part)
		}
		flush()
		return result
	}

	// No separator worked — hard split on characters
	return hardSplit(text)
}

func hardSplit(text string) []string {
	runes := []rune(text)
	var chunks []string
	for i := 0; i < len(runes); i += ChunkSize - ChunkOverlap {
		end := i + ChunkSize
		if end > len(runes) {
			end = len(runes)
		}
		if chunk := strings.TrimSpace(string(runes[i:end])); chunk != "" {
			chunks = append(chunks, chunk)
		}
		if end == len(runes) {
			break
		}
	}
	return chunks
}

// SplitPages chunks a whole document instead of each page in isolation, and
// reports the page each chunk starts on.
//
// Splitting page by page (what we did before) cut every paragraph that ran
// across a page break, and turned title pages into 2-character chunks. Here the
// pages are joined, split as one text, and each chunk is mapped back to a page
// by locating it in the joined text — so `page_number` stays exact for citation.
//
// Chunks shorter than MinChunkChars are dropped, unless the whole document is
// that short (a one-line note is still worth indexing).
func SplitPages(pages []string) (chunks []string, pageNums []int) {
	if len(pages) == 0 {
		return nil, nil
	}
	const sep = "\n\n"
	starts := make([]int, len(pages)) // rune offset where each page begins
	var b strings.Builder
	for i, p := range pages {
		if i > 0 {
			b.WriteString(sep)
		}
		starts[i] = len([]rune(b.String()))
		b.WriteString(p)
	}
	full := b.String()
	fullRunes := []rune(full)

	pageOf := func(offset int) int {
		page := 1
		for i, start := range starts {
			if offset >= start {
				page = i + 1
			} else {
				break
			}
		}
		return page
	}

	raw := SplitText(full)
	cursor := 0 // rune offset from which to look for the next chunk
	for _, c := range raw {
		page := pageOf(cursor)
		// Locate the chunk to attribute it to the right page. Chunks overlap, so
		// the search restarts at the previous match, never past it.
		if idx := runeIndex(fullRunes[cursor:], []rune(c)); idx >= 0 {
			cursor += idx
			page = pageOf(cursor)
		}
		if len([]rune(c)) < MinChunkChars && len(raw) > 1 {
			continue
		}
		chunks = append(chunks, c)
		pageNums = append(pageNums, page)
	}
	return chunks, pageNums
}

// runeIndex returns the rune offset of needle in haystack, or -1.
func runeIndex(haystack, needle []rune) int {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return -1
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i] == needle[0] && string(haystack[i:i+len(needle)]) == string(needle) {
			return i
		}
	}
	return -1
}

// SplitDocument chunks a paginationless document (DOCX, TXT, CSV, XLSX…) with
// the same hygiene as SplitPages: fragments below MinChunkChars are dropped,
// unless the whole document is that short. Page numbers are all zero — these
// formats have none.
func SplitDocument(text string) (chunks []string, pageNums []int) {
	raw := SplitText(text)
	for _, c := range raw {
		if len([]rune(c)) < MinChunkChars && len(raw) > 1 {
			continue
		}
		chunks = append(chunks, c)
		pageNums = append(pageNums, 0)
	}
	return chunks, pageNums
}
