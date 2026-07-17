package server

import "testing"

func TestMentionsAgent(t *testing.T) {
	cases := []struct {
		content, agent string
		want           bool
	}{
		{"hey @agent what's up", "Assistant", true},
		{"@Assistant help", "Assistant", true},
		{"@assistant help", "Assistant", true}, // case-insensitive
		{"just chatting with the team", "Assistant", false},
		{"email me at bob@agentcorp.com", "Assistant", true}, // contains @agent — acceptable (rare)
		{"hi @Nova", "Nova", true},
		{"hi Nova", "Nova", false},
	}
	for _, c := range cases {
		if got := mentionsAgent(c.content, c.agent); got != c.want {
			t.Errorf("mentionsAgent(%q, %q) = %v, want %v", c.content, c.agent, got, c.want)
		}
	}
}

func TestStripMention(t *testing.T) {
	cases := []struct{ content, agent, want string }{
		{"@agent what is RAG?", "Assistant", "what is RAG?"},
		{"@Assistant summarize this", "Assistant", "summarize this"},
		{"hey @Nova can you help", "Nova", "hey  can you help"},
		{"no mention here", "Assistant", "no mention here"},
	}
	for _, c := range cases {
		if got := stripMention(c.content, c.agent); got != c.want {
			t.Errorf("stripMention(%q, %q) = %q, want %q", c.content, c.agent, got, c.want)
		}
	}
}
