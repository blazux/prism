package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	"prism/internal/ollama"
)

func TestBuildSystemHoistsThePromptOutOfTheHistory(t *testing.T) {
	msgs := []ollama.Message{
		{Role: "system", Content: "You are PRISM."},
		{Role: "user", Content: "hi"},
		{Role: "system", Content: "   "}, // an empty block is rejected by the API
	}

	got := buildSystem(msgs)
	if len(got) != 1 || got[0].Text != "You are PRISM." {
		t.Errorf("expected PRISM's prompt as the only system block, got %+v", got)
	}
}

func TestBuildMessagesDropsSystemTurns(t *testing.T) {
	msgs := []ollama.Message{
		{Role: "system", Content: "prompt"},
		{Role: "user", Content: "hi"},
	}
	got := buildMessages(msgs)
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

	got := buildMessages(msgs)
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
	got := buildMessages(msgs)
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

	got := buildMessages(msgs)
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
	got := buildMessages(msgs)
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
	res := buildMessages(withEmptyResult)
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
	got := buildMessages(msgs)
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
	got := buildMessages(msgs)
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
	got := buildMessages(msgs)
	last := got[len(got)-1].Content[0].Text
	if last != "partial answer" {
		t.Errorf("expected trailing whitespace trimmed, got %q", last)
	}
}

func TestBuildMessagesCarriesImages(t *testing.T) {
	msgs := []ollama.Message{{Role: "user", Content: "what is this", Images: []string{"BASE64DATA"}}}
	got := buildMessages(msgs)
	if len(got[0].Content) != 2 || got[0].Content[1].Type != "image" {
		t.Fatalf("expected a text block and an image block, got %+v", got[0].Content)
	}
	if got[0].Content[1].Source.Data != "BASE64DATA" {
		t.Errorf("image data did not survive the conversion: %+v", got[0].Content[1].Source)
	}
}

func TestToolBuilderReassemblesStreamedCalls(t *testing.T) {
	b := newToolBuilder()
	b.start(0, "read_file")
	b.addArgs(0, `{"path":`)
	b.addArgs(0, `"/tmp/x"}`)

	calls := b.result()
	if len(calls) != 1 {
		t.Fatalf("expected one call, got %d", len(calls))
	}
	if calls[0].Function.Name != "read_file" {
		t.Errorf("expected the registered name, got %q", calls[0].Function.Name)
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

	calls := b.result()
	for _, c := range calls {
		if !json.Valid(c.Function.Arguments) {
			t.Errorf("tool %q got unusable arguments %s", c.Function.Name, c.Function.Arguments)
		}
	}
}

func TestValidateKeyRejectsASubscriptionTokenWithAnActionableMessage(t *testing.T) {
	// sk-ant-api… and sk-ant-oat… differ by three characters, and the wrong one
	// earns an opaque 401 from Anthropic. Say what is actually wrong instead.
	oauthShaped := []string{
		"sk-ant-oat01-xxxx", // `claude setup-token`
		"cc-xxxx",           // Claude Code access token
		"eyJhbGciOi",        // OAuth JWT
	}
	for _, token := range oauthShaped {
		err := ValidateKey(token)
		if err == nil {
			t.Errorf("ValidateKey(%q) accepted a subscription token", token)
			continue
		}
		for _, want := range []string{"OAuth token", "sk-ant-api", "console.anthropic.com", "extra"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("ValidateKey(%q) message is missing %q: %v", token, want, err)
			}
		}
	}

	if err := ValidateKey("sk-ant-api03-xxxx"); err != nil {
		t.Errorf("a console key must be accepted, got %v", err)
	}
	if err := ValidateKey(""); err == nil {
		t.Error("an unset key must be reported, not sent as an empty header")
	}
}
