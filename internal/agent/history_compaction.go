package agent

import "prism/internal/ollama"

// safeCompactionCut advances to a user boundary that preserves both a complete
// tool batch and the persisted history range. Replay may move an injected note
// after its tool results, so message IDs need not be in ascending order in memory.
func safeCompactionCut(history []ollama.Message, candidate int) int {
	suffixMin := make([]int64, len(history)+1)
	for i := len(history) - 1; i >= 0; i-- {
		suffixMin[i] = suffixMin[i+1]
		if id := history[i].DBID; id > 0 && (suffixMin[i] == 0 || id < suffixMin[i]) {
			suffixMin[i] = id
		}
	}
	var droppedMax int64
	pending := 0
	for i, message := range history {
		if i >= candidate && message.Role == "user" && pending == 0 && (suffixMin[i] == 0 || droppedMax < suffixMin[i]) {
			return i
		}
		if message.DBID > droppedMax {
			droppedMax = message.DBID
		}
		switch message.Role {
		case "assistant":
			pending = len(message.ToolCalls)
		case "tool":
			if pending > 0 {
				pending--
			}
		}
	}
	return len(history)
}
