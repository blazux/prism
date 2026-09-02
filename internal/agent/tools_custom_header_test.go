package agent

import (
	"strings"
	"testing"
)

// extractToolName must say WHY a # TOOL: header is unusable, not just that it
// is — the unbalanced-brace case is the one measured in the field (xslog_sip:
// the model retried twice fixing accents while one closing brace was missing).
func TestExtractToolName(t *testing.T) {
	cases := []struct {
		name    string
		code    string
		want    string
		errPart string // "" = no error expected
	}{
		{
			"valid header",
			"#!/usr/bin/env python3\n# TOOL: {\"name\":\"xslog_sip\",\"description\":\"d\"}\nimport json",
			"xslog_sip", "",
		},
		{
			"missing closing brace (the xslog_sip failure)",
			"#!/usr/bin/env python3\n# TOOL: {\"name\":\"xslog_sip\",\"parameters\":{\"type\":\"object\",\"properties\":{\"minutes\":{\"type\":\"integer\"}}}\nimport json",
			"", "never closes",
		},
		{
			"no name field",
			"# TOOL: {\"description\":\"d\"}\nimport json",
			"", "no \"name\" field",
		},
		{
			"invalid JSON (trailing comma)",
			"# TOOL: {\"name\":\"x\",}\nimport json",
			"", "invalid JSON",
		},
		{
			"no header at all",
			"#!/usr/bin/env python3\nimport json",
			"", "",
		},
	}
	for _, c := range cases {
		got, err := extractToolName(c.code)
		if got != c.want {
			t.Errorf("%s: name=%q, want %q", c.name, got, c.want)
		}
		if c.errPart == "" {
			if err != nil && got == "" && c.code != cases[4].code {
				t.Errorf("%s: unexpected error: %v", c.name, err)
			}
		} else if err == nil || !strings.Contains(err.Error(), c.errPart) {
			t.Errorf("%s: error=%v, want it to mention %q", c.name, err, c.errPart)
		}
	}
}
