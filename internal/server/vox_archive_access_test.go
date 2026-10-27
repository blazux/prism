package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"prism/internal/agent"
)

func TestSharedCatalogueHidesDeniedOutboundTools(t *testing.T) {
	s := &Server{}
	cc := s.callerContextForGroup(t.Context(), 2)
	for _, name := range []string{"place_call", "list_calls", "cancel_call"} {
		if !cc.HiddenTools[name] || cc.Guard(name, nil) == nil {
			t.Fatalf("denied tool advertised: %s", name)
		}
	}
	if cc.HiddenTools["exec_command"] || cc.Guard("exec_command", nil) != nil {
		t.Fatal("archive recipe lost the existing execution primitive")
	}
	e := agent.NewToolExecutor(nil, t.TempDir(), t.TempDir(), "", "")
	e.SetVox("http://fixture.invalid", "", "")
	cc.apply(e)
	for _, tool := range e.AllDynamicTools() {
		if tool.Function.Name == "list_calls" {
			t.Fatal("list_calls still advertised dynamically")
		}
	}
}

func TestVoxArchiveAccessFactsAndSignedGroupRead(t *testing.T) {
	ms := securityStore(t)
	ctx := t.Context()
	group, _ := ms.CreateGroup(ctx, "Reception fixtures")
	other, _ := ms.CreateGroup(ctx, "Other fixtures")
	reached := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached++
		var scope map[string]any
		_ = json.Unmarshal([]byte(r.Header.Get("X-Prism-Scope")), &scope)
		if scope["standard"] != true || scope["owner"] != nil || scope["admin"] != nil {
			t.Errorf("unsafe group scope: %v", scope)
		}
		if r.URL.Path != "/api/history" || r.URL.Query().Get("kind") != "calls" || r.URL.Query().Get("session_id") != "" {
			t.Errorf("unsafe archive path: %s", r.URL)
		}
		fmt.Fprint(w, `{"total":0,"items":[],"next_offset":null}`)
	}))
	defer upstream.Close()
	s := &Server{memStore: ms, cfg: Config{MultiUser: true, VoxURL: upstream.URL, AuthToken: "fixture-deployment-key"}}
	facts := s.voxGroupArchiveContext(ctx, group.ID)
	if !strings.Contains(facts, "NOT ENABLED") || !strings.Contains(facts, "PrismVox → Access") {
		t.Fatal("missing current access facts")
	}
	request := func(gid int64) *httptest.ResponseRecorder {
		session := fmt.Sprintf("room-g%d", gid)
		uid, signedSession, ok := s.verifyCapToken(s.selfCallToken(session))
		if !ok {
			t.Fatal("fixture capability failed")
		}
		r := httptest.NewRequest("GET", "/api/vox/history?kind=conversation&session_id=victim", nil)
		scoped, ok := s.capabilityRequest(r, uid, signedSession)
		if !ok {
			t.Fatal("signed group capability not accepted")
		}
		w := httptest.NewRecorder()
		s.handleVoxHistory(w, scoped)
		return w
	}
	if w := request(group.ID); w.Code != 403 || !strings.Contains(w.Body.String(), "not enabled") {
		t.Fatalf("unconfigured group should get a clear refusal: %d %s", w.Code, w.Body.String())
	}
	if reached != 0 {
		t.Fatal("unconfigured group reached Vox")
	}
	raw, _ := json.Marshal([]int64{group.ID})
	_ = ms.SetConfig(ctx, voxWorkspaceGroupsKey, string(raw))
	facts = s.voxGroupArchiveContext(ctx, group.ID)
	if strings.Contains(facts, "NOT ENABLED") || !strings.Contains(facts, "exec_command") || !strings.Contains(facts, "$PRISM_URL/api/vox/history") || !strings.Contains(facts, "$PRISM_TOKEN") {
		t.Fatal("authorized group did not receive usable archive recipe")
	}
	if w := request(group.ID); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := request(other.ID); w.Code != 403 {
		t.Fatal("other group gained archive access")
	}
	if reached != 1 {
		t.Fatalf("unexpected upstream requests: %d", reached)
	}
	_ = ms.SetGroupToolAccess(ctx, group.ID, "exec_command", "admin_only")
	if !strings.Contains(s.voxGroupArchiveContext(ctx, group.ID), "exec_command is restricted") {
		t.Fatal("recipe ignores explicit tool restrictions")
	}
	cc := s.callerContextForGroup(ctx, group.ID)
	if !cc.HiddenTools["exec_command"] {
		t.Fatal("blocked execution tool advertised")
	}
	_ = ms.SetToolPolicy(ctx, "list_calls", "open")
	cc = s.callerContextForGroup(ctx, group.ID)
	if cc.HiddenTools["list_calls"] || cc.Guard("list_calls", nil) != nil {
		t.Fatal("explicitly permitted outbound queue was hidden")
	}
}

func TestHeadlessGroupPromptIncludesCurrentArchiveAccess(t *testing.T) {
	ms := securityStore(t)
	ctx := context.Background()
	group, _ := ms.CreateGroup(ctx, "Prompt fixtures")
	enabled := false
	calls := 0
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[]}`)
			return
		}
		var request struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		calls++
		prompt := ""
		for _, m := range request.Messages {
			if m.Role == "system" {
				prompt += m.Content
			}
		}
		if enabled {
			if !strings.Contains(prompt, "$PRISM_URL/api/vox/history") || strings.Contains(prompt, "NOT ENABLED") {
				t.Error("allowed archive facts missing or stale in the actual model prompt")
			}
		} else if !strings.Contains(prompt, "NOT ENABLED") {
			t.Error("disabled access not present in the actual model prompt")
		}
		for _, tool := range request.Tools {
			if tool.Function.Name == "list_calls" {
				t.Error("model advertised the denied queue tool")
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Fixture reply\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer model.Close()
	s := New(Config{WorkspaceDir: t.TempDir(), PluginDir: t.TempDir(), Model: "fixture", LLMBackend: "openai", OpenAIBaseURL: model.URL, VoxURL: "http://fixture.invalid", MultiUser: true})
	s.memStore = ms
	session := fmt.Sprintf("room-g%d", group.ID)
	for _, allowed := range []bool{false, true} {
		enabled = allowed
		if allowed {
			raw, _ := json.Marshal([]int64{group.ID})
			_ = ms.SetConfig(ctx, voxWorkspaceGroupsKey, string(raw))
		}
		cc := s.callerContextForGroup(ctx, group.ID)
		if _, err := s.runHeadlessChatTap(ctx, session, "Do you have call history access?", "fixture", cc, nil, agent.Limits{PromptProfile: "minimal"}); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("expected two model turns, got %d", calls)
	}
}
