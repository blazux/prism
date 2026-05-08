package agent

import "testing"

func TestStripThinkingBlocks_NoTag(t *testing.T) {
	input := "Hello, world! This is a normal response."
	if got := stripThinkingBlocks(input); got != input {
		t.Errorf("no tag: want %q, got %q", input, got)
	}
}

func TestStripThinkingBlocks_SimpleThinking(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "thinking tag removed",
			input: "Before. <thinking>internal reasoning</thinking> After.",
			want:  "Before.  After.",
		},
		{
			name:  "thought tag removed",
			input: "Result: <thought>let me compute this</thought> 42",
			want:  "Result:  42",
		},
		{
			name:  "only the tag, nothing else",
			input: "<thinking>just noise</thinking>",
			want:  "",
		},
		{
			name:  "multiple thinking blocks",
			input: "<thinking>first</thinking> middle <thinking>second</thinking> end",
			want:  "middle  end",
		},
		{
			name:  "case-insensitive open tag",
			input: "text <THINKING>hidden</thinking> visible",
			want:  "text  visible",
		},
		{
			name:  "case-insensitive close tag",
			input: "text <thinking>hidden</THINKING> visible",
			want:  "text  visible",
		},
		{
			name:  "both tags",
			input: "<thought>t1</thought> between <thinking>t2</thinking> after",
			want:  "between  after",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stripThinkingBlocks(tc.input)
			if got != tc.want {
				t.Errorf("want %q, got %q", tc.want, got)
			}
		})
	}
}

// TestStripThinkingBlocks_UnclosedTag verifies that when an opening <thinking>
// tag is never closed, only the tag itself is removed and the rest of the
// response is preserved. Previously, everything from the tag onward was silently
// dropped, causing the user to see a truncated response.
func TestStripThinkingBlocks_UnclosedTag(t *testing.T) {
	input := "First paragraph.\n\n<thinking>starting to reason...\n\nThis is the rest of the response that the user should see."

	got := stripThinkingBlocks(input)

	// The opening tag must be gone.
	if containsString(got, "<thinking>") {
		t.Errorf("opening tag still present in output: %q", got)
	}

	// The visible content after the tag must be preserved.
	preserved := "This is the rest of the response that the user should see."
	if !containsString(got, preserved) {
		t.Errorf("content after unclosed tag was lost: %q not in %q", preserved, got)
	}

	// Content before the tag must also be preserved.
	if !containsString(got, "First paragraph.") {
		t.Errorf("content before unclosed tag was lost: %q", got)
	}
}

func TestStripThinkingBlocks_PreservesContentAroundBlock(t *testing.T) {
	// The content before and after a properly closed block must be intact.
	before := "Important answer: the result is 42."
	after := "Please let me know if you need more details."
	input := before + " <thinking>internal scratch work</thinking> " + after

	got := stripThinkingBlocks(input)

	if !containsString(got, before) {
		t.Errorf("content before block lost: %q not in %q", before, got)
	}
	if !containsString(got, after) {
		t.Errorf("content after block lost: %q not in %q", after, got)
	}
}

func containsString(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
