package server

import (
	"encoding/json"
	"net/http/httptest"
	"prism/internal/notes"
	"strings"
	"testing"
)

func TestHostedNotesRejectLocalVault(t *testing.T) {
	ms := securityStore(t)
	s := &Server{memStore: ms}
	path := t.TempDir()
	body, _ := json.Marshal(map[string]string{"provider": "vault", "path": path})
	call := func(method, body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		s.handleNotesSource(w, httptest.NewRequest(method, "/api/notes/source", strings.NewReader(body)))
		return w
	}
	// Standalone retains local vault support.
	if w := call("POST", string(body)); w.Code != 200 {
		t.Fatalf("local: %d %s", w.Code, w.Body.String())
	}
	ms.LocalVaultDisabled = true
	if w := call("POST", string(body)); w.Code != 403 {
		t.Fatalf("hosted: %d %s", w.Code, w.Body.String())
	}
	w := call("GET", "")
	var config map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &config); err != nil {
		t.Fatal(err)
	}
	if config["vault_available"] != false || config["path"] != "" {
		t.Fatal("hosted API exposed local vault capability/path")
	}
	// A pre-existing config cannot bypass the policy through the agent/provider.
	p := notes.ProviderFor(t.Context(), ms, "default")
	if _, err := p.List(t.Context()); err == nil {
		t.Fatal("old vault was readable")
	}
	if _, err := p.Save(t.Context(), "", "probe", "body", ""); err == nil {
		t.Fatal("old vault was writable")
	}
	if err := p.Delete(t.Context(), "probe.md"); err == nil {
		t.Fatal("old vault allowed deletion")
	}
	if !ms.ConfigScope("u1").LocalVaultDisabled {
		t.Fatal("scoped store lost deployment policy")
	}
	if w := call("POST", `{"provider":"local"}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if p := notes.ProviderFor(t.Context(), ms, "default"); p.Kind() != "local" {
		t.Fatal("database notes unavailable")
	}
}
