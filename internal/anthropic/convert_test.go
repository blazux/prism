package anthropic

import (
	"encoding/json"
	"testing"

	"prism/internal/ollama"
)

func TestBuildSystemHoistsPromptAndLeadsWithClaudeCodeIdentity(t *testing.T) {
	msgs := []ollama.Message{
		{Role: "system", Content: "You are PRISM."},
		{Role: "user", Content: "hi"},
	}

	oauth := buildSystem(msgs, true)
	if len(oauth) != 2 {
		t.Fatalf("expected the identity block plus PRISM's prompt, got %d blocks", len(oauth))
	}
	if oauth[0].Text != claudeCodeSystemPrefix {
		t.Errorf("identity block must come first, got %q", oauth[0].Text)
	}
	if oauth[1].Text != "You are PRISM." {
		t.Errorf("PRISM's prompt should follow it, got %q", oauth[1].Text)
	}

	// On an API key there is no subscription to route, so no identity block.
	plain := buildSystem(msgs, false)
	if len(plain) != 1 || plain[0].Text != "You are PRISM." {
		t.Errorf("api-key path should carry PRISM's prompt alone, got %+v", plain)
	}
}

func TestBuildMessagesDropsSystemTurns(t *testing.T) {
	msgs := []ollama.Message{
		{Role: "system", Content: "prompt"},
		{Role: "user", Content: "hi"},
	}
	got := buildMessages(msgs, false)
	if len(got) != 1 || got[0].Role != "user" {
		t.Fatalf("system turns belong in the system field, got %+v", got)
	}
}

func TestBuildMessagesPairsToolCallsWithResults(t *testing.T) {
	msgs := []ollama.Message{
		{Role: "user", Content: "what time is it"},
		{Role: "assistant", Content: "checking", ToolCalls: []ollama.ToolCall{
			{Function: ollama.ToolCallFunction{Name: "clock", Arguments: json.RawMessage(`{"tz":"UTC"}`)}},
		}},
		{Role: "tool", Content: "12:00"},
	}

	got := buildMessages(msgs, false)
	if len(got) != 3 {
		t.Fatalf("expected user/assistant/user, got %d messages: %+v", len(got), got)
	}

	assistant := got[1]
	if assistant.Role != "assistant" || len(assistant.Content) != 2 {
		t.Fatalf("assistant turn should carry text + tool_use, got %+v", assistant)
	}
	use := assistant.Content[1]
	if use.Type != "tool_use" || use.Name != "clock" {
		t.Fatalf("expected a tool_use block for clock, got %+v", use)
	}

	result := got[2]
	if result.Role != "user" {
		t.Fatalf("tool results ride in a user turn, got role %q", result.Role)
	}
	if result.Content[0].Type != "tool_result" || result.Content[0].ToolUseID != use.ID {
		t.Fatalf("tool_result must reference the tool_use id %q, got %+v", use.ID, result.Content[0])
	}
}

func TestBuildMessagesDropsOrphanedToolResult(t *testing.T) {
	// The originating assistant call was summarised out of the replayed history.
	// Anthropic rejects a tool_result pointing at an unknown id, so it must go.
	msgs := []ollama.Message{
		{Role: "user", Content: "hi"},
		{Role: "tool", Content: "stale output"},
	}
	got := buildMessages(msgs, false)
	if len(got) != 1 || len(got[0].Content) != 1 || got[0].Content[0].Type != "text" {
		t.Fatalf("orphaned tool result should be dropped, got %+v", got)
	}
}

func TestBuildMessagesMergesConsecutiveSameRoleTurns(t *testing.T) {
	// Two tool results in a row have to arrive as two blocks of one user turn.
	msgs := []ollama.Message{
		{Role: "user", Content: "go"},
		{Role: "assistant", ToolCalls: []ollama.ToolCall{
			{Function: ollama.ToolCallFunction{Name: "a", Arguments: json.RawMessage(`{}`)}},
			{Function: ollama.ToolCallFunction{Name: "b", Arguments: json.RawMessage(`{}`)}},
		}},
		{Role: "tool", Content: "one"},
		{Role: "tool", Content: "two"},
	}

	got := buildMessages(msgs, false)
	if len(got) != 3 {
		t.Fatalf("expected the two results merged into one turn, got %d messages", len(got))
	}
	if n := len(got[2].Content); n != 2 {
		t.Fatalf("expected 2 tool_result blocks in one user turn, got %d", n)
	}
	if got[2].Content[0].ToolUseID == got[2].Content[1].ToolUseID {
		t.Error("each result must reference its own tool_use id")
	}
}

func TestBuildMessagesSkipsEmptyTurnsAndFillsEmptyToolResults(t *testing.T) {
	msgs := []ollama.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "   "}, // nothing to say, nothing to call
		{Role: "user", Content: "still there?"},
	}
	got := buildMessages(msgs, false)
	for _, m := range got {
		if len(m.Content) == 0 {
			t.Fatalf("an empty content list is rejected by the API: %+v", got)
		}
		for _, b := range m.Content {
			if b.Type == "text" && b.Text == "" {
				t.Fatalf("empty text blocks are rejected by the API: %+v", got)
			}
		}
	}

	withEmptyResult := []ollama.Message{
		{Role: "user", Content: "go"},
		{Role: "assistant", ToolCalls: []ollama.ToolCall{
			{Function: ollama.ToolCallFunction{Name: "a", Arguments: json.RawMessage(`{}`)}},
		}},
		{Role: "tool", Content: ""},
	}
	res := buildMessages(withEmptyResult, false)
	last := res[len(res)-1].Content[0]
	if last.Content == "" {
		t.Error("an empty tool result must be filled in, not sent empty")
	}
}

func TestBuildMessagesRepairsInvalidToolArguments(t *testing.T) {
	// A turn truncated mid-stream leaves unparseable arguments in the history.
	// Replaying them would 400 this request and every one after it.
	msgs := []ollama.Message{
		{Role: "user", Content: "go"},
		{Role: "assistant", ToolCalls: []ollama.ToolCall{
			{Function: ollama.ToolCallFunction{Name: "a", Arguments: json.RawMessage(`{"path":"/tmp`)}},
		}},
	}
	got := buildMessages(msgs, false)
	input := got[1].Content[0].Input
	if !json.Valid(input) {
		t.Fatalf("invalid arguments must be replaced, got %s", input)
	}
}

func TestBuildMessagesOpensOnAUserTurn(t *testing.T) {
	msgs := []ollama.Message{
		{Role: "assistant", Content: "resumed after compaction"},
		{Role: "user", Content: "go on"},
	}
	got := buildMessages(msgs, false)
	if got[0].Role != "user" {
		t.Fatalf("the conversation must open on a user turn, got %q", got[0].Role)
	}
}

func TestBuildMessagesTrimsTrailingAssistantWhitespace(t *testing.T) {
	// A trailing assistant turn is a prefill, and one ending in whitespace is
	// rejected outright.
	msgs := []ollama.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "partial answer\n\n"},
	}
	got := buildMessages(msgs, false)
	last := got[len(got)-1].Content[0].Text
	if last != "partial answer" {
		t.Errorf("expected trailing whitespace trimmed, got %q", last)
	}
}

func TestBuildMessagesCarriesImages(t *testing.T) {
	msgs := []ollama.Message{{Role: "user", Content: "what is this", Images: []string{"BASE64DATA"}}}
	got := buildMessages(msgs, false)
	if len(got[0].Content) != 2 || got[0].Content[1].Type != "image" {
		t.Fatalf("expected a text block and an image block, got %+v", got[0].Content)
	}
	if got[0].Content[1].Source.Data != "BASE64DATA" {
		t.Errorf("image data did not survive the conversion: %+v", got[0].Content[1].Source)
	}
}

func TestToolNamesRoundTripThroughTheOAuthWire(t *testing.T) {
	// A single-underscore mcp_ name is what trips Anthropic's third-party
	// classifier; everything must land on the double-underscore form.
	cases := []struct{ registered, wire string }{
		{"read_file", "mcp__read_file"},
		{"mcp_linear_get_issue", "mcp__linear_get_issue"},
		{"mcp__already_prefixed", "mcp__already_prefixed"},
	}
	for _, c := range cases {
		if got := toWireName(c.registered); got != c.wire {
			t.Errorf("toWireName(%q) = %q, want %q", c.registered, got, c.wire)
		}
		if got := fromWireName(c.wire); got == c.wire && c.wire != c.registered {
			t.Errorf("fromWireName(%q) left the wire prefix in place", c.wire)
		}
	}
}

func TestBuildToolsPrefixesOnlyOnTheOAuthPath(t *testing.T) {
	tools := []ollama.Tool{{Function: ollama.ToolFunction{Name: "read_file", Description: "d"}}}

	oauth := buildTools(tools, true)
	if oauth[0].Name != "mcp__read_file" {
		t.Errorf("subscription wire needs the mcp__ prefix, got %q", oauth[0].Name)
	}
	if oauth[0].InputSchema.Type != "object" {
		t.Errorf("a schema with no type is rejected, got %q", oauth[0].InputSchema.Type)
	}

	plain := buildTools(tools, false)
	if plain[0].Name != "read_file" {
		t.Errorf("api-key path must send the registered name, got %q", plain[0].Name)
	}
}

func TestToolBuilderStripsWirePrefixFromStreamedCalls(t *testing.T) {
	b := newToolBuilder()
	b.start(0, "mcp__read_file")
	b.addArgs(0, `{"path":`)
	b.addArgs(0, `"/tmp/x"}`)

	calls := b.result(true)
	if len(calls) != 1 {
		t.Fatalf("expected one call, got %d", len(calls))
	}
	if calls[0].Function.Name != "read_file" {
		t.Errorf("the dispatcher must see the registered name, got %q", calls[0].Function.Name)
	}
	if string(calls[0].Function.Arguments) != `{"path":"/tmp/x"}` {
		t.Errorf("fragments did not reassemble: %s", calls[0].Function.Arguments)
	}
}

func TestToolBuilderRepairsTruncatedAndMissingArguments(t *testing.T) {
	b := newToolBuilder()
	b.start(0, "no_args")         // a no-argument call streams no fragments at all
	b.start(1, "truncated")       //
	b.addArgs(1, `{"path":"/tmp`) // cut off mid-stream

	calls := b.result(false)
	for _, c := range calls {
		if !json.Valid(c.Function.Arguments) {
			t.Errorf("tool %q got unusable arguments %s", c.Function.Name, c.Function.Arguments)
		}
	}
}

func TestIsOAuthTokenSeparatesSubscriptionTokensFromAPIKeys(t *testing.T) {
	cases := map[string]bool{
		"sk-ant-api03-xxxx": false, // console API key → x-api-key, no CLI fingerprint
		"sk-ant-oat01-xxxx": true,  // setup-token
		"cc-xxxx":           true,  // Claude Code access token
		"eyJhbGciOi":        true,  // OAuth JWT
		"":                  false,
		"some-other-key":    false,
	}
	for token, want := range cases {
		if got := isOAuthToken(token); got != want {
			t.Errorf("isOAuthToken(%q) = %v, want %v", token, got, want)
		}
	}
}
