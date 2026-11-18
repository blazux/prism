package anthropic

import (
	"bytes"
	"encoding/json"
	"log"
	"strings"
	"testing"

	"prism/internal/ollama"
)

func TestMalformedToolArgumentsRejectActionsWithoutLoggingValues(t *testing.T) {
	var logged bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logged)
	defer log.SetOutput(previous)
	for _, args := range []string{`{"password":"private-secret`, `null`, `[]`, `"text"`} {
		builder := newToolBuilder()
		builder.start(0, "write_file")
		builder.addArgs(0, args)
		calls := builder.result()
		if len(calls) != 1 || calls[0].Function.ArgumentError == "" || string(calls[0].Function.Arguments) != `{}` {
			t.Fatalf("malformed arguments remained executable: %+v", calls)
		}
		if !json.Valid(calls[0].Function.Arguments) {
			t.Fatal("replay arguments are invalid JSON")
		}
		if strings.Contains(calls[0].Function.ArgumentError, "private-secret") {
			t.Fatal("argument value leaked into returned error")
		}
	}
	if strings.Contains(logged.String(), "private-secret") || strings.Contains(logged.String(), "password") {
		t.Fatal("raw malformed arguments leaked into logs")
	}
}

func TestNonstreamToolArgumentsUseSameRejectionAndSafeReplay(t *testing.T) {
	out := make(chan ollama.StreamEvent, 1)
	(&Client{}).readMessage(strings.NewReader(`{"content":[{"type":"tool_use","id":"toolu_1","name":"write_file","input":null}],"stop_reason":"tool_use"}`), out)
	event := <-out
	if event.Err != nil || len(event.ToolCalls) != 1 || event.ToolCalls[0].Function.ArgumentError == "" {
		t.Fatalf("nonstream arguments bypassed validation: %+v", event)
	}
	var block struct {
		Input json.RawMessage `json:"input"`
	}
	if len(event.ProviderBlocks) != 1 || json.Unmarshal(event.ProviderBlocks[0], &block) != nil || string(block.Input) != `{}` {
		t.Fatalf("unsafe provider input replay: %+v", event.ProviderBlocks)
	}
}

func TestValidToolArgumentsRemainExecutable(t *testing.T) {
	for _, args := range []string{"", `{}`, `{"path":"file.txt"}`} {
		builder := newToolBuilder()
		builder.start(0, "read_file")
		builder.addArgs(0, args)
		calls := builder.result()
		if len(calls) != 1 || calls[0].Function.ArgumentError != "" {
			t.Fatalf("valid arguments rejected: %+v", calls)
		}
	}
}
