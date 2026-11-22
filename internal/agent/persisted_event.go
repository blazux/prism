package agent

import "prism/internal/ollama"

// ObservePersistedMessage makes an external result available on the next turn.
// The caller already saved it atomically; never write it again or interrupt a
// live assistant/tool-result batch to insert it.
func (a *Agent) ObservePersistedMessage(id int64, role, content string) {
	if id <= 0 || role != "assistant" || content == "" {
		return
	}
	a.histMu.Lock()
	defer a.histMu.Unlock()
	for _, message := range a.pendingPersisted {
		if message.DBID == id {
			return
		}
	}
	a.pendingPersisted = append(a.pendingPersisted, ollama.Message{DBID: id, Role: role, Content: content})
}

func (a *Agent) mergePersistedMessages() {
	a.histMu.Lock()
	defer a.histMu.Unlock()
	for _, pending := range a.pendingPersisted {
		found := false
		for _, message := range a.history {
			if message.DBID == pending.DBID {
				found = true
				break
			}
		}
		if !found {
			a.history = append(a.history, pending)
			a.historyGen++
		}
	}
	a.pendingPersisted = nil
}
