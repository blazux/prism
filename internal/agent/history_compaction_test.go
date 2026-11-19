package agent

import (
	"encoding/json"
	"reflect"
	"testing"

	"prism/internal/ollama"
)

func TestCompactedReplayedBatchDoesNotReturnAsOrphanResults(t *testing.T) {
	for _, complete := range []bool{false, true} {
		name := "partial"
		if complete {
			name = "complete"
		}
		t.Run(name, func(t *testing.T) {
			store := resourceDatabase(t)
			ctx := t.Context()
			const session = "compaction-interleaved-note"
			batch, _ := json.Marshal(recoveryBatch().ToolCalls)
			appendRow := func(role, content string, calls json.RawMessage) int64 {
				t.Helper()
				id, err := store.AppendMessage(ctx, session, role, content, calls)
				if err != nil {
					t.Fatal(err)
				}
				return id
			}
			appendRow("user", "Prepare both files", nil)
			appendRow("assistant", "Preparing the files.", batch)
			noteID := appendRow("user", "A note received while the tools were running", nil)
			appendRow("tool", "Saved first result", nil)
			if complete {
				appendRow("tool", "Saved second result", nil)
			}
			nextID := appendRow("user", "The next request", nil)
			appendRow("assistant", "Answer to the next request", nil)
			archive, err := store.LoadHistory(ctx, session)
			if err != nil {
				t.Fatal(err)
			}
			newAgent := func() *Agent {
				a := New(&fakeSummarizeBackend{reply: "Earlier work and its concurrent note."}, nil, "fixture", store, "")
				a.SetSession(session, "")
				a.loadHistoryFromDB(ctx)
				return a
			}
			a := newAgent()
			// The provider-compatible replay puts this note after the results,
			// although its stored ID is earlier than theirs. Aim at that boundary.
			target := 0
			found := false
			for _, message := range a.history {
				found = found || message.DBID == noteID
				if found {
					target += messageContextChars(message)
				}
			}
			if !found {
				t.Fatal("interleaved note missing from replay")
			}
			if err := a.compactLiveContextTo(ctx, nil, target); err != nil {
				t.Fatal(err)
			}
			reloaded := newAgent()
			if !reflect.DeepEqual(a.history, reloaded.history) {
				t.Fatalf("reload changed the compacted context or restored orphan results:\nbefore=%+v\nafter=%+v", a.history, reloaded.history)
			}
			compaction := store.GetLiveCompaction(ctx, session)
			if compaction == nil || compaction.BeforeID != nextID {
				t.Fatalf("compaction boundary = %+v, want next request ID %d", compaction, nextID)
			}
			after, err := store.LoadHistory(ctx, session)
			if err != nil || !reflect.DeepEqual(archive, after) {
				t.Fatalf("compaction changed the archive: %v", err)
			}
		})
	}
}

func TestCompactionDoesNotSplitLiveToolBatchAtInjectedNote(t *testing.T) {
	batch := recoveryBatch()
	for _, nextTurn := range []bool{false, true} {
		history := []ollama.Message{
			{Role: "user", Content: "Prepare both files", DBID: 1}, batch,
			{Role: "user", Content: "Note received during execution", DBID: 3},
			{Role: "tool", Content: "Saved first result", DBID: 4},
			{Role: "tool", Content: "Saved second result", DBID: 5},
		}
		if nextTurn {
			history = append(history, ollama.Message{Role: "user", Content: "Next task", DBID: 6})
		}
		target := 0
		for _, message := range history[2:] {
			target += messageContextChars(message)
		}
		backend := &recoveryCaptureBackend{capturingBackend: capturingBackend{reply: "Earlier work."}}
		a := &Agent{model: "fixture", ollama: backend, history: history}
		if err := a.compactLiveContextTo(t.Context(), nil, target); err != nil {
			t.Fatal(err)
		}
		if !nextTurn {
			if backend.calls != 0 || !reflect.DeepEqual(a.history, history) {
				t.Fatal("compaction split a batch without a later safe boundary")
			}
		} else if backend.calls != 1 || len(a.history) != 2 || a.history[1].DBID != 6 {
			t.Fatalf("compaction did not advance beyond the interleaved note: %+v", a.history)
		}
	}
}
