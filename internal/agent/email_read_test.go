package agent

import (
	"encoding/json"
	"prism/internal/email"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestEmailReadPagesPreserveUnicodeAndStayUnderExecutorCap(t *testing.T) {
	for _, body := range []string{strings.Repeat("été🙂", 4000), strings.Repeat("<&>\"\n", 5000)} {
		msg := email.Message{Folder: "Archive", UID: 42, Body: body}
		offset := 0
		var joined strings.Builder
		for pages := 0; ; pages++ {
			if pages > 100 {
				t.Fatal("continuation did not advance")
			}
			result, err := emailReadResult(msg, offset)
			if err != nil {
				t.Fatal(err)
			}
			if !utf8.ValidString(result) || capToolResult(result) != result {
				t.Fatal("invalid or truncated JSON")
			}
			var page emailReadPage
			if err = json.Unmarshal([]byte(result), &page); err != nil {
				t.Fatal(err)
			}
			if page.Folder != "Archive" || page.UID != 42 || page.TotalChars != len([]rune(body)) || page.BodyFormat != "text" {
				t.Fatal("lost context")
			}
			joined.WriteString(page.Body)
			if !page.Truncated {
				if page.NextOffset != nil {
					t.Fatal("unexpected continuation")
				}
				break
			}
			if page.NextOffset == nil || *page.NextOffset <= offset {
				t.Fatal("no continuation")
			}
			offset = *page.NextOffset
		}
		if joined.String() != body {
			t.Fatal("lost or duplicated text")
		}
	}
}
func TestEmailReadConvertsBeforePaging(t *testing.T) {
	msg := email.Message{BodyIsHTML: true, Body: "<html><head><style>" + strings.Repeat("x{}", 10000) + "</style></head><body><p>Here is the article.</p></body></html>"}
	result, err := emailReadResult(msg, 0)
	if err != nil {
		t.Fatal(err)
	}
	var page emailReadPage
	json.Unmarshal([]byte(result), &page)
	if page.Body != "Here is the article." || page.Truncated {
		t.Fatal(result)
	}
	for _, offset := range []int{-1, 999} {
		if _, err = emailReadResult(msg, offset); err == nil {
			t.Fatal("invalid offset accepted")
		}
	}
}
