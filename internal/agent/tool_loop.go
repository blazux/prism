package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"

	"prism/internal/ollama"
)

const toolLoopWindow = 12

type toolLoopSignal uint8

const (
	toolLoopNone toolLoopSignal = iota
	toolLoopWarn
	toolLoopStop
)

// Repetition is only an observation, not proof that the task cannot progress.
// Give the model a chance to finish or change approach before stopping a cycle.
// Result images participate so a changing screenshot is a new observation.
type toolLoopDetector struct {
	recent  [][sha256.Size]byte
	warned  map[[sha256.Size]byte]bool
	pending map[[sha256.Size]byte]bool
}

func (d *toolLoopDetector) observe(call ollama.ToolCall, result string, images []string) toolLoopSignal {
	args := string(call.Function.Arguments)
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(call.Function.Arguments))
	decoder.UseNumber()
	if decoder.Decode(&decoded) == nil {
		canonical, _ := json.Marshal(decoded)
		args = string(canonical)
	}
	payload, _ := json.Marshal(struct {
		Name   string
		Args   string
		Result string
		Images []string
	}{call.Function.Name, args, result, images})
	fingerprint := sha256.Sum256(payload)
	if len(d.recent) == toolLoopWindow {
		d.recent = d.recent[1:]
	}
	d.recent = append(d.recent, fingerprint)
	matches := 0
	for _, previous := range d.recent {
		if previous == fingerprint {
			matches++
		}
	}
	// A different operation or changed result can be useful work between checks.
	// Do not carry an earlier warning across that change. The overall turn budget
	// still bounds investigations whose commands keep changing.
	if matches == 1 {
		clear(d.warned)
		clear(d.pending)
	}
	for previous := range d.warned {
		present := false
		for _, recent := range d.recent {
			present = present || recent == previous
		}
		if !present {
			delete(d.warned, previous)
		}
	}
	if matches < 3 {
		return toolLoopNone
	}
	if d.warned[fingerprint] {
		return toolLoopStop
	}
	if d.pending == nil {
		d.pending = make(map[[sha256.Size]byte]bool)
	}
	d.pending[fingerprint] = true
	return toolLoopWarn
}

func (d *toolLoopDetector) warning() string {
	if len(d.pending) == 0 {
		return ""
	}
	return "[Prism execution status] Some tool calls returned the same result for the same arguments three times recently. Reuse the results already available. Conclude if the request is satisfied, or change approach if information is still missing. If waiting is required, use a bounded wait or polling command rather than repeating model turns. Continued identical calls without a new observation will stop this turn."
}

// Called only once the warning is in the context of the next model request.
// A batch containing several identical calls cannot consume that opportunity.
func (d *toolLoopDetector) afterBatch() {
	if d.warned == nil {
		d.warned = make(map[[sha256.Size]byte]bool)
	}
	for fingerprint := range d.pending {
		d.warned[fingerprint] = true
	}
	clear(d.pending)
}

// Execution stops are persisted as diagnostics and replayed as context, so the
// next turn has the actual reason for the interruption even after reconnecting.
const agentStopPrefix = "[Prism execution status] "

func (a *Agent) recordAgentStop(ctx context.Context, reason string) {
	note := agentStopPrefix + reason + " The task is incomplete. This records an agent execution limit."
	message := ollama.Message{Role: "user", Content: note}
	message.DBID = a.saveMessageToDB(ctx, ollama.Message{Role: "error", Content: note})
	a.histMu.Lock()
	a.history = append(a.history, message)
	a.histMu.Unlock()
}
