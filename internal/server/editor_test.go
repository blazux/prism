package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestEditorRPCStaysOnItsConnection(t *testing.T) {
	c := &Client{send: make(chan []byte, 1)}
	other := &Client{send: make(chan []byte, 1)}
	done := make(chan error, 1)
	go func() {
		result, err := c.editorTool(context.Background(), map[string]any{"action": "read"})
		if err == nil && !strings.Contains(result, "draft") {
			t.Errorf("unexpected response: %s", result)
		}
		done <- err
	}()
	var request struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	select {
	case data := <-c.send:
		if err := json.Unmarshal(data, &request); err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("no request")
	}
	if request.Type != "editor_request" {
		t.Fatal(request)
	}
	other.editorResponse(request.ID, json.RawMessage(`{"body":"wrong tab"}`))
	select {
	case <-done:
		t.Fatal("other connection resolved request")
	default:
	}
	c.editorResponse(request.ID, json.RawMessage(`{"body":"draft","revision":"v1"}`))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(c.editorPending) != 0 {
		t.Fatal("request not cleaned")
	}
}
func TestEditorRejectsMissingRevisionAndPropagatesConflicts(t *testing.T) {
	c := &Client{send: make(chan []byte, 1)}
	if _, err := c.editorTool(context.Background(), map[string]any{"action": "update", "body": "unsafe"}); err == nil {
		t.Fatal("missing revision accepted")
	}
	done := make(chan error, 1)
	go func() {
		_, err := c.editorTool(context.Background(), map[string]any{"action": "update", "revision": "old", "body": "new"})
		done <- err
	}()
	var request struct {
		ID string `json:"id"`
	}
	json.Unmarshal(<-c.send, &request)
	c.editorResponse(request.ID, json.RawMessage(`{"error":"editor changed"}`))
	if err := <-done; err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatal(err)
	}
}
func TestEditorCancelledRequestIsRemoved(t *testing.T) {
	c := &Client{send: make(chan []byte, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.editorTool(ctx, map[string]any{"action": "read"}); err == nil {
		t.Fatal("cancelled request succeeded")
	}
	if len(c.editorPending) != 0 {
		t.Fatal("pending request leaked")
	}
}

func TestEditorOpenMailUsesInitiatingBrowserAndReturnsRevision(t *testing.T) {
	c := &Client{send: make(chan []byte, 1)}
	if _, err := c.editorTool(context.Background(), map[string]any{"action": "open", "app": "notes"}); err == nil {
		t.Fatal("unsupported app accepted")
	}
	done := make(chan error, 1)
	go func() {
		result, err := c.editorTool(context.Background(), map[string]any{"action": "open", "app": "email", "folder": "Projects", "uid": 17})
		if err == nil && !strings.Contains(result, "revision") {
			t.Error("missing new draft revision")
		}
		done <- err
	}()
	var request struct {
		ID   string         `json:"id"`
		Args map[string]any `json:"args"`
	}
	select {
	case data := <-c.send:
		if err := json.Unmarshal(data, &request); err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("no browser request")
	}
	if request.Args["action"] != "open" || request.Args["folder"] != "Projects" {
		t.Fatalf("wrong request: %v", request.Args)
	}
	c.editorResponse(request.ID, json.RawMessage(`{"status":"opened","app":"email","revision":"new","body":"Quoted original"}`))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
