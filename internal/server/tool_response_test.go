package server

import (
	"errors"
	"testing"
)

func TestToolResponseBody(t *testing.T) {
	cases := []struct {
		name    string
		out     string
		execErr error
		want    string
	}{
		{"json array passes through", `[{"id":1},{"id":2}]`, nil, `[{"id":1},{"id":2}]`},
		{"json array trimmed", "  [1,2,3]\n", nil, "[1,2,3]"},
		{"json object passes through", `{"a":1}`, nil, `{"a":1}`},
		{"plain text is wrapped", "hello world", nil, `{"output":"hello world"}`},
		{"empty output is wrapped", "", nil, `{"output":""}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := string(toolResponseBody(c.out, c.execErr)); got != c.want {
				t.Errorf("toolResponseBody(%q) = %s, want %s", c.out, got, c.want)
			}
		})
	}

	// An exec error is surfaced even when the raw stdout looks like JSON — never
	// silently pass a partial array from a crashed tool off as success.
	got := string(toolResponseBody(`[{"id":1}]`, errors.New("boom")))
	if got != `{"error":"boom","output":"[{\"id\":1}]"}` {
		t.Errorf("exec error case = %s", got)
	}
}
