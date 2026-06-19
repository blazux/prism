package memory

import "testing"

func TestStripThinking(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"no tags", "Plain summary.", "Plain summary."},
		{"closed think", "<think>reasoning here</think>The user wants X.", "The user wants X."},
		{"tag mid-text", "Before <think>noise</think> after", "Before  after"},
		{"unclosed tag drops trailing", "Valid summary.<think>leaked reasoning that never ends", "Valid summary."},
		{"multiple blocks", "<think>a</think>One.<think>b</think>Two.", "One.Two."},
		{"thinking variant", "<thinking>x</thinking>Done.", "Done."},
		{"thought variant", "<thought>y</thought>Ok.", "Ok."},
		{"trims whitespace", "  <think>z</think>  Result.  ", "Result."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := stripThinking(c.in); got != c.want {
				t.Errorf("stripThinking(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
