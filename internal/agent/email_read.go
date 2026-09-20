package agent

import (
	"fmt"
	"prism/internal/email"
)

type emailReadPage struct {
	email.Message
	BodyFormat string `json:"body_format"`
	Offset     int    `json:"offset"`
	TotalChars int    `json:"total_chars"`
	Truncated  bool   `json:"truncated"`
	NextOffset *int   `json:"next_offset,omitempty"`
}

// Keep each JSON page below the executor's cap, including JSON escaping and
// multibyte text. Continuation offsets count Unicode characters, never bytes.
func emailReadResult(msg email.Message, offset int) (string, error) {
	text := []rune(msg.ReadableBody())
	if offset < 0 || offset > len(text) {
		return "", fmt.Errorf("read offset must be between 0 and %d; use next_offset from the previous result", len(text))
	}
	end := min(offset+8000, len(text))
	for {
		msg.Body = string(text[offset:end])
		page := emailReadPage{Message: msg, BodyFormat: "text", Offset: offset, TotalChars: len(text), Truncated: end < len(text)}
		if page.Truncated {
			next := end
			page.NextOffset = &next
		}
		result := jsonResult(page)
		if len(result) <= maxToolResultBytes-1000 {
			return result, nil
		}
		if end-offset <= 1 {
			return "", fmt.Errorf("message metadata exceeds the tool result limit")
		}
		end = offset + (end-offset)/2
	}
}
