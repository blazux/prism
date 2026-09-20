package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestActivityDoesNotPersistToolSecretsOrBodies(t *testing.T) {
	meta, ok := activityDetails("email", json.RawMessage(`{"action":"send","to":"private@example.org","body":"secret body","password":"secret password"}`))
	b, _ := json.Marshal(meta)
	if !ok || strings.Contains(string(b), "private") || strings.Contains(string(b), "secret") {
		t.Fatal(string(b))
	}
	_, ok = activityDetails("email", json.RawMessage(`{"action":"list"}`))
	if ok {
		t.Fatal("read poll in actions feed")
	}
}
