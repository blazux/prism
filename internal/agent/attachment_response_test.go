package agent

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"prism/internal/workspace"
	"testing"
)

func TestResponseAttachmentSnapshot(t *testing.T) {
	root := t.TempDir()
	name := "report & résumé.txt"
	if err := os.WriteFile(filepath.Join(root, name), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	e := &ToolExecutor{workspaceDir: root, sessionID: "u7-default"}
	result, images, err := e.addAttachment(name)
	if err != nil || len(images) != 0 {
		t.Fatalf("%v %v", err, images)
	}
	var payload struct{ Attachment struct{ Name, URL string } }
	if err = json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(payload.Attachment.URL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/api/chat/attachment" || u.Query().Get("session") != "u7-default" || payload.Attachment.Name != name {
		t.Fatal(result)
	}
	os.WriteFile(filepath.Join(root, name), []byte("changed"), 0600)
	data, err := workspace.ReadFile(root, AttachmentFilePath("u7-default", u.Query().Get("id"), name))
	if err != nil || string(data) != "original" {
		t.Fatalf("snapshot: %q %v", data, err)
	}
	if _, _, err = e.addAttachment("missing.pdf"); err == nil {
		t.Fatal("missing file reported success")
	}
	if _, _, err = e.addAttachment("../outside"); err == nil {
		t.Fatal("escape accepted")
	}
}
