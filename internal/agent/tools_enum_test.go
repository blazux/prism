package agent

import (
	"strings"
	"testing"
)

// Every closed-set string parameter must carry a machine-readable Enum: a
// mid-size local model reads the schema far more reliably than prose, and a
// wrong value otherwise costs a whole turn. The prose ("One of: …") stays for
// humans; this test keeps the two from drifting apart.
func TestClosedSetParamsHaveEnums(t *testing.T) {
	for _, td := range ToolDefinitions {
		for pname, p := range td.Function.Parameters.Properties {
			if p.Type != "string" || !strings.Contains(p.Description, "One of:") {
				continue
			}
			if len(p.Enum) == 0 {
				t.Errorf("%s.%s says %q but has no Enum", td.Function.Name, pname, p.Description)
				continue
			}
			for _, v := range p.Enum {
				if !strings.Contains(p.Description, v) {
					t.Errorf("%s.%s: enum value %q is not mentioned in its description %q", td.Function.Name, pname, v, p.Description)
				}
			}
		}
	}
}
