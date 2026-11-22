package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConnectedSupervisionPreservesHistoryWithoutAcceptingNewMessages(t *testing.T) {
	requests := 0
	vox := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "GET" || r.URL.Path != "/api/supervision/conversation" {
			t.Errorf("archive modified: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"items":[{"role":"user","content":"Earlier question"}],"total":1}`))
	}))
	defer vox.Close()
	s := &Server{memStore: securityStore(t), cfg: Config{VoxURL: vox.URL}}
	for _, path := range []string{"chat", "context"} {
		r := withUser(httptest.NewRequest("POST", "/api/vox-workspace/"+path, strings.NewReader(`{"message":"new question"}`)), serviceUser)
		w := httptest.NewRecorder()
		s.handleVoxWorkspaceAPI(w, r)
		if w.Code < 400 {
			t.Fatalf("legacy %s still writable", path)
		}
	}
	r := withUser(httptest.NewRequest("GET", "/api/vox-workspace/conversation", nil), serviceUser)
	w := httptest.NewRecorder()
	s.handleVoxWorkspaceAPI(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Earlier question") || requests != 1 {
		t.Fatalf("archive not preserved: %d %s", w.Code, w.Body)
	}
}
