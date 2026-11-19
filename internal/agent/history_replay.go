package agent

import "prism/internal/ollama"

const unknownToolOutcome = "Prism recovered no saved result for this tool call. Its outcome is unknown: it may have run or may still be running. Verify the actual state before repeating it."

// normalizeReplayedToolBatches gives providers a complete result block for each
// stored assistant tool batch. A process can stop after an action but before
// saving its result, so missing rows never mean that an action did not happen.
// These synthetic results are for model context only: another worker may still
// append the real result, and the archive must keep that distinction intact.
// Notes injected while a batch was executing retain their content and relative
// order, after its result block as required by strict tool-use protocols.
func normalizeReplayedToolBatches(in []ollama.Message) ([]ollama.Message, int) {
	if len(in) == 0 {
		return in, 0
	}
	out := make([]ollama.Message, 0, len(in))
	missing := 0
	for i := 0; i < len(in); {
		message := in[i]
		out = append(out, message)
		i++
		if message.Role != "assistant" || len(message.ToolCalls) == 0 {
			continue
		}
		end := i
		for end < len(in) && in[end].Role != "assistant" {
			end++
		}
		results := make([]ollama.Message, 0, len(message.ToolCalls))
		var notes []ollama.Message
		for _, following := range in[i:end] {
			if following.Role == "tool" && len(results) < len(message.ToolCalls) {
				results = append(results, following)
			} else {
				notes = append(notes, following)
			}
		}
		for len(results) < len(message.ToolCalls) {
			results = append(results, ollama.Message{Role: "tool", Content: unknownToolOutcome})
			missing++
		}
		out = append(out, results...)
		out = append(out, notes...)
		i = end
	}
	return out, missing
}
