package agent

import "testing"

func TestClampIterations(t *testing.T) {
	cases := map[int]int{0: 0, -5: 0, 3: MinMaxIterations, 75: 75, 9999: MaxMaxIterations}
	for in, want := range cases {
		if got := ClampIterations(in); got != want {
			t.Errorf("ClampIterations(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestEffectiveLimits(t *testing.T) {
	off, on := false, true
	a := &Agent{}
	if n, th := a.effectiveLimits(); n != DefaultMaxIterations || !th {
		t.Errorf("defaults: got (%d,%v)", n, th)
	}
	a.limits = Limits{MaxIterations: 120, Thinking: &off}
	if n, th := a.effectiveLimits(); n != 120 || th {
		t.Errorf("config: got (%d,%v)", n, th)
	}
	// Override wins field by field; unset fields fall through to config.
	a.limitsOverride = Limits{Thinking: &on}
	if n, th := a.effectiveLimits(); n != 120 || !th {
		t.Errorf("partial override: got (%d,%v)", n, th)
	}
	a.limitsOverride = Limits{MaxIterations: 30}
	if n, th := a.effectiveLimits(); n != 30 || th {
		t.Errorf("iter override: got (%d,%v)", n, th)
	}
}

func TestNormalizeReasoningEffort(t *testing.T) {
	cases := map[string]string{"": "", "low": "low", " XHigh ": "xhigh", "medium": "medium", "high": "high", "ultra": "", "none": ""}
	for in, want := range cases {
		if got := NormalizeReasoningEffort(in); got != want {
			t.Errorf("NormalizeReasoningEffort(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReasoningEffort_Resolution(t *testing.T) {
	a := &Agent{}
	if got := a.reasoningEffort(); got != "" {
		t.Errorf("default: got %q, want \"\" (server default)", got)
	}
	a.limits = Limits{ReasoningEffort: "low"}
	if got := a.reasoningEffort(); got != "low" {
		t.Errorf("config: got %q, want low", got)
	}
	// A group override wins; an empty override falls through to config.
	a.limitsOverride = Limits{ReasoningEffort: "xhigh"}
	if got := a.reasoningEffort(); got != "xhigh" {
		t.Errorf("override: got %q, want xhigh", got)
	}
	a.limitsOverride = Limits{MaxIterations: 30}
	if got := a.reasoningEffort(); got != "low" {
		t.Errorf("partial override: got %q, want low", got)
	}
}
