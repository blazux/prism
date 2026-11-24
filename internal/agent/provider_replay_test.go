package agent

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"prism/internal/ollama"
)

func providerReplayFixture() ollama.Message {
	return ollama.Message{Role: "assistant", Content: "Searching. Found it.",
		ToolCalls: []ollama.ToolCall{{Function: ollama.ToolCallFunction{
			Name: "email_account", Arguments: json.RawMessage(`{"password":"fixture-password","nested":{"api_key":"fixture-api-key"},"address":"fixture@example.test"}`),
		}}},
		ProviderBlocks: []json.RawMessage{
			json.RawMessage(`{"type":"text","text":"Searching. "}`),
			json.RawMessage(`{"type":"server_tool_use","id":"srvtoolu_search","name":"tool_search_tool_bm25","input":{"query":"mail settings","limit":5}}`),
			json.RawMessage(`{"type":"tool_search_tool_result","tool_use_id":"srvtoolu_search","content":{"type":"tool_search_tool_search_result","tool_references":[{"type":"tool_reference","tool_name":"email_account"}]}}`),
			json.RawMessage(`{"type":"text","text":"Found it."}`),
			json.RawMessage(`{"type":"tool_use","id":"toolu_account","name":"email_account","input":{"password":"fixture-password","nested":{"api_key":"fixture-api-key"},"address":"fixture@example.test"}}`),
		},
	}
}

func TestProviderReplayPreservesSearchAndUsesCanonicalRedactedArguments(t *testing.T) {
	message := providerReplayFixture()
	originalCalls, _ := json.Marshal(message.ToolCalls)
	originalBlocks, _ := json.Marshal(message.ProviderBlocks)
	metadata := providerReplayMetadata(message)
	if len(metadata) == 0 || strings.Contains(string(metadata), "fixture-password") || strings.Contains(string(metadata), "fixture-api-key") || strings.Contains(string(metadata), "address") {
		t.Fatal("checkpoint missing or duplicated client tool arguments")
	}
	blocks := restoreProviderReplay(metadata, message.ToolCalls)
	if len(blocks) != len(message.ProviderBlocks) {
		t.Fatal("provider replay lost the original block sequence")
	}
	for i, raw := range blocks {
		var want, got any
		_ = json.Unmarshal(message.ProviderBlocks[i], &want)
		_ = json.Unmarshal(raw, &got)
		if i == len(blocks)-1 {
			want.(map[string]any)["input"] = map[string]any{"password": "[REDACTED]", "nested": map[string]any{"api_key": "[REDACTED]"}, "address": "fixture@example.test"}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("block %d changed replay data", i)
		}
	}
	callsAfter, _ := json.Marshal(message.ToolCalls)
	blocksAfter, _ := json.Marshal(message.ProviderBlocks)
	if string(callsAfter) != string(originalCalls) || string(blocksAfter) != string(originalBlocks) {
		t.Fatal("building a checkpoint altered live tool execution arguments or provider blocks")
	}
}

func TestProviderReplayKeepsServerOnlyPausesAndLaterResults(t *testing.T) {
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"type":"server_tool_use","id":"srvtoolu_paused","name":"tool_search_tool_regex","input":{"pattern":"mail.*","limit":5}}`),
		json.RawMessage(`{"type":"tool_search_tool_result","tool_use_id":"srvtoolu_paused","content":{"type":"tool_search_tool_search_result","tool_references":[]}}`),
	} {
		message := ollama.Message{Role: "assistant", ProviderBlocks: []json.RawMessage{raw}}
		blocks := restoreProviderReplay(providerReplayMetadata(message), nil)
		if len(blocks) != 1 {
			t.Fatal("server-only response was discarded because it had no client call or text")
		}
		var want, got any
		_ = json.Unmarshal(raw, &want)
		_ = json.Unmarshal(blocks[0], &got)
		if !reflect.DeepEqual(got, want) {
			t.Fatal("server-only replay changed a search ID, query or result")
		}
	}
}

func TestProviderReplayRejectsOpaquePrivateAndMismatchedState(t *testing.T) {
	for _, raw := range []string{
		`{"type":"thinking","thinking":"fixture-private-thought","signature":"fixture-signature"}`,
		`{"type":"text","text":"visible","signature":"fixture-signature"}`,
		`{"type":"text","text":"<THINKING>fixture-private-thought</THINKING>visible"}`,
		`{"type":"text","text":"<think>unfinished private thought"}`,
		`{"type":"text","text":"\u0000"}`,
		`{"type":"server_tool_use","id":"srvtoolu_search","name":"web_search","input":{"query":"mail"}}`,
		`{"type":"server_tool_use","id":"srvtoolu_search","name":"tool_search_tool_bm25","input":{"query":"mail","api_key":"fixture-private-key"}}`,
		`{"type":"tool_search_tool_result","tool_use_id":"toolu_client","content":{"type":"tool_search_tool_search_result","tool_references":[]}}`,
		`{"type":"tool_search_tool_result","tool_use_id":"srvtoolu_search","content":{"type":"tool_search_tool_search_result","tool_references":[{"type":"tool_reference","tool_name":"email","description":"opaque"}]}}`,
		`{"type":"tool_search_tool_result","tool_use_id":"srvtoolu_search","content":{"type":"tool_search_tool_search_result"}}`,
	} {
		message := ollama.Message{Role: "assistant", ProviderBlocks: []json.RawMessage{json.RawMessage(raw)}}
		if got := providerReplayMetadata(message); got != nil {
			t.Fatalf("unsupported response persisted: %s", replayBlockType(json.RawMessage(raw)))
		}
	}
	message := providerReplayFixture()
	message.ToolCalls[0].Function.Name = "different_tool"
	if providerReplayMetadata(message) != nil {
		t.Fatal("mismatched client names were checkpointed")
	}
	message = providerReplayFixture()
	message.ToolCalls[0].Function.Arguments = json.RawMessage(`{"different":"argument"}`)
	if providerReplayMetadata(message) != nil {
		t.Fatal("provider inputs were mapped to different canonical arguments")
	}
	message = providerReplayFixture()
	message.ProviderBlocks = append(message.ProviderBlocks, message.ProviderBlocks[1])
	if providerReplayMetadata(message) != nil {
		t.Fatal("duplicate provider tool IDs were checkpointed")
	}
}

func TestProviderReplayValidationAndBounds(t *testing.T) {
	message := providerReplayFixture()
	metadata := providerReplayMetadata(message)
	for _, corrupt := range []json.RawMessage{
		json.RawMessage(`null`),
		json.RawMessage(`{"provider":"other","version":1,"blocks":[]}`),
		json.RawMessage(strings.Replace(string(metadata), `"version":1`, `"version":2`, 1)),
		json.RawMessage(strings.Replace(string(metadata), `"call_index":0`, `"call_index":1`, 1)),
		json.RawMessage(strings.Replace(string(metadata), `"call_index":0`, `"input":{"api_key":"unsafe"}`, 1)),
		append(metadata, []byte(` {}`)...),
		json.RawMessage(strings.Repeat(" ", maxProviderReplayBytes+1)),
	} {
		if restoreProviderReplay(corrupt, message.ToolCalls) != nil {
			t.Fatal("invalid checkpoint restored")
		}
	}
	if restoreProviderReplay(metadata, nil) != nil {
		t.Fatal("client calls restored without their canonical arguments")
	}
	message.ProviderBlocks = append([]json.RawMessage{json.RawMessage(`{"type":"text","text":"` + strings.Repeat("x", maxProviderReplayBytes) + `"}`)}, message.ProviderBlocks...)
	if providerReplayMetadata(message) != nil {
		t.Fatal("unbounded checkpoint stored")
	}
	message = ollama.Message{Role: "assistant", ProviderBlocks: make([]json.RawMessage, maxProviderReplayBlocks+1)}
	if providerReplayMetadata(message) != nil {
		t.Fatal("unbounded provider block list stored")
	}
	if providerReplayMetadata(ollama.Message{Role: "assistant", Content: "ordinary answer", ProviderBlocks: []json.RawMessage{json.RawMessage(`{"type":"text","text":"ordinary answer"}`)}}) != nil {
		t.Fatal("ordinary text-only reply acquired unnecessary metadata")
	}
}

func TestProviderReplayPersistsOutsideBrowserHistory(t *testing.T) {
	store := resourceDatabase(t)
	ctx := t.Context()
	const session = "provider-replay-fixture"
	message := providerReplayFixture()
	metadata := providerReplayMetadata(message)
	calls, _ := json.Marshal(redactedToolCalls(message.ToolCalls))
	if _, err := store.AppendMessage(ctx, session, "user", "Configure the fixture.", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendMessageWithProviderMetadata(ctx, session, message.Role, message.Content, calls, metadata); err != nil {
		t.Fatal(err)
	}
	rows, err := store.LoadHistory(ctx, session)
	if err != nil || len(rows) != 2 || len(rows[0].ProviderMetadata) != 0 || len(rows[1].ProviderMetadata) == 0 {
		t.Fatalf("metadata was not kept separately from legacy history: rows=%d error=%v", len(rows), err)
	}
	var canonical []ollama.ToolCall
	if err := json.Unmarshal(rows[1].ToolCalls, &canonical); err != nil {
		t.Fatal(err)
	}
	if blocks := restoreProviderReplay(rows[1].ProviderMetadata, canonical); len(blocks) != len(message.ProviderBlocks) {
		t.Fatal("checkpoint did not survive its JSONB round trip")
	}
	visible, _ := json.Marshal(rows)
	if strings.Contains(string(visible), "srvtoolu_search") || strings.Contains(string(visible), "provider_metadata") || strings.Contains(string(visible), "fixture-password") {
		t.Fatal("private provider metadata or unredacted inputs reached browser history JSON")
	}
	page, err := store.LoadHistoryPage(ctx, session, 0, 10)
	if err != nil || len(page.Entries) != 2 || len(page.Entries[1].ProviderMetadata) != 0 {
		t.Fatal("browser pagination loaded private provider metadata")
	}
	tail, err := store.LoadActiveHistoryTail(ctx, session, 1)
	if err != nil || len(tail) != 1 || len(tail[0].ProviderMetadata) == 0 {
		t.Fatal("bounded active-history query omitted provider metadata")
	}
}

func TestProviderReplayReloadsAgentWithRedactedCalls(t *testing.T) {
	store := resourceDatabase(t)
	ctx := t.Context()
	for _, tc := range []struct {
		name    string
		message ollama.Message
	}{
		{"completed-search", providerReplayFixture()},
		{"paused-search", ollama.Message{Role: "assistant", ProviderBlocks: []json.RawMessage{json.RawMessage(`{"type":"server_tool_use","id":"srvtoolu_pending","name":"tool_search_tool_bm25","input":{"query":"notes"}}`)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const prompt = "Fixture request."
			first := New(nil, &ToolExecutor{}, "anthropic::claude-sonnet-5-5", store, "")
			first.SetSession("provider-agent-"+tc.name, "")
			if first.saveMessageToDB(ctx, ollama.Message{Role: "user", Content: prompt}) == 0 || first.saveMessageToDB(ctx, tc.message) == 0 {
				t.Fatal("failed to save the assistant checkpoint")
			}
			if len(tc.message.ToolCalls) > 0 && first.saveMessageToDB(ctx, ollama.Message{Role: "tool", Content: "Fixture account configured."}) == 0 {
				t.Fatal("failed to save the completed tool result")
			}
			fresh := New(nil, &ToolExecutor{}, "anthropic::claude-sonnet-5-5", store, "")
			fresh.SetSession(first.sessionID, "")
			fresh.loadHistoryFromDB(ctx)
			if len(fresh.history) < 2 || len(fresh.history[1].ProviderBlocks) != len(tc.message.ProviderBlocks) {
				t.Fatal("a newly constructed agent lost the saved provider state")
			}
			for _, block := range fresh.history[1].ProviderBlocks {
				if strings.Contains(string(block), "fixture-password") || strings.Contains(string(block), "fixture-api-key") {
					t.Fatal("restored provider blocks bypassed canonical argument redaction")
				}
			}
			if len(tc.message.ToolCalls) > 0 && !strings.Contains(string(fresh.history[1].ToolCalls[0].Function.Arguments), "[REDACTED]") {
				t.Fatal("reloaded canonical tool arguments were not redacted")
			}
			visible, _ := json.Marshal(fresh.history)
			if strings.Contains(string(visible), "srvtoolu_") {
				t.Fatal("provider blocks leaked into model-neutral message JSON")
			}
		})
	}
}
