package server

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Requests stay on the initiating browser connection: a second tab/session
// must never receive an edit intended for this editor.
func (c *Client) editorTool(ctx context.Context, args map[string]any) (string, error) {
	if c.isDisconnected() {
		return "", fmt.Errorf("the initiating editor is offline; ask the user to reopen it before editing unsaved content")
	}
	action, _ := args["action"].(string)
	if action != "read" && action != "update" {
		return "", fmt.Errorf("editor action must be read or update")
	}
	if action == "update" {
		if rev, _ := args["revision"].(string); rev == "" {
			return "", fmt.Errorf("read the active editor first and pass its revision")
		}
	}
	ch := make(chan json.RawMessage, 1)
	c.mu.Lock()
	c.editorSequence++
	id := fmt.Sprintf("editor-%d", c.editorSequence)
	if c.editorPending == nil {
		c.editorPending = make(map[string]chan json.RawMessage)
	}
	c.editorPending[id] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.editorPending, id); c.mu.Unlock() }()
	deadline := time.Now().Add(30 * time.Second)
	c.sendJSONReliable(map[string]any{"type": "editor_request", "id": id, "args": args, "expires_at": deadline.UnixMilli()})
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case raw := <-ch:
		var response struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			return "", fmt.Errorf("invalid editor response")
		}
		if response.Error != "" {
			return "", fmt.Errorf("%s", response.Error)
		}
		return string(raw), nil
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timer.C:
		return "", fmt.Errorf("editor did not respond; read it again before retrying, an update may already have applied")
	}
}

func (c *Client) editorResponse(id string, data json.RawMessage) {
	c.mu.Lock()
	ch := c.editorPending[id]
	c.mu.Unlock()
	if ch != nil {
		select {
		case ch <- data:
		default:
		}
	}
}
