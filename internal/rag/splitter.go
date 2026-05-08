package rag

import "strings"

const (
	ChunkSize    = 1000
	ChunkOverlap = 150
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
