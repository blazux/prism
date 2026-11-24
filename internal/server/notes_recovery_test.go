package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"prism/internal/memory"
)

func noteRecoveryCall(t *testing.T, s *Server, user *memory.User, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(method, path, strings.NewReader(body)), user)
	switch r.URL.Path {
	case "/api/notes/history":
		s.handleNoteHistory(w, r)
	case "/api/notes/trash":
		s.handleNoteTrash(w, r)
	case "/api/notes/restore":
		s.handleNoteRestore(w, r)
	case "/api/builtin/note":
		s.handleBuiltinTool(w, r)
	default:
		s.handleNotes(w, r)
	}
	return w
}

func TestNotesRecoveryDatabaseAndAgentParity(t *testing.T) {
	ms := securityStore(t)
	ctx := t.Context()
	s := New(Config{MultiUser: true, WorkspaceDir: t.TempDir(), PluginDir: t.TempDir()})
	s.memStore = ms
	owner := scopeFixtureUser(t, ms, "note-recovery-owner", memory.RoleMember)
	other := scopeFixtureUser(t, ms, "note-recovery-other", memory.RoleMember)
	scope := fmt.Sprintf("u%d", owner.ID)
	id, err := ms.AddNote(ctx, scope, "Avant", "Été : rôle privé", "réseau")
	if err != nil {
		t.Fatal(err)
	}
	// The ordinary agent update shares the same storage recovery path as UI.
	body := fmt.Sprintf(`{"action":"update","id":"%d","title":"Après","body":"Câble réparé"}`, id)
	w := noteRecoveryCall(t, s, owner, "POST", "/api/builtin/note", body)
	if w.Code != 200 || strings.Contains(w.Body.String(), `"error"`) {
		t.Fatalf("agent update: %d %s", w.Code, w.Body.String())
	}
	n, err := ms.GetNote(ctx, scope, id)
	if err != nil || n.Body != "Câble réparé" || len(n.Aliases) != 1 || n.Aliases[0] != "Avant" {
		t.Fatalf("updated note: %+v %v", n, err)
	}
	path := fmt.Sprintf("/api/notes/history?id=%d", id)
	w = noteRecoveryCall(t, s, owner, "GET", path, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var history struct {
		Versions []struct {
			ID   string `json:"id"`
			Body string `json:"body"`
		}
	}
	if json.Unmarshal(w.Body.Bytes(), &history) != nil || len(history.Versions) != 1 || history.Versions[0].Body != "" {
		t.Fatalf("history must be metadata: %s", w.Body.String())
	}
	version := history.Versions[0].ID
	w = noteRecoveryCall(t, s, owner, "GET", path+"&version="+version, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Été") {
		t.Fatalf("version preview: %d %s", w.Code, w.Body.String())
	}
	if w := noteRecoveryCall(t, s, other, "GET", path+"&version="+version, ""); w.Code != 404 {
		t.Fatalf("cross-user history: %d %s", w.Code, w.Body.String())
	}
	if w := noteRecoveryCall(t, s, owner, "POST", "/api/notes/restore", fmt.Sprintf(`{"id":"%d","version":"%s"}`, id, version)); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	n, _ = ms.GetNote(ctx, scope, id)
	if n.Body != "Été : rôle privé" || n.Title != "Avant" {
		t.Fatalf("wrong restored state: %+v", n)
	}
	if versions, err := ms.NoteHistory(ctx, scope, id, 0); err != nil || len(versions) != 2 {
		t.Fatalf("restore did not save replaced state: %+v %v", versions, err)
	}
	// Ordinary agent deletion now moves to trash without copying the body into its result.
	w = noteRecoveryCall(t, s, owner, "POST", "/api/builtin/note", fmt.Sprintf(`{"action":"delete","id":"%d"}`, id))
	if w.Code != 200 || strings.Contains(w.Body.String(), `"error"`) || strings.Contains(w.Body.String(), "rôle privé") {
		t.Fatalf("agent delete: %d %s", w.Code, w.Body.String())
	}
	if _, err := ms.GetNote(ctx, scope, id); err == nil {
		t.Fatal("trashed note still active")
	}
	w = noteRecoveryCall(t, s, owner, "GET", "/api/notes/trash", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Avant") || strings.Contains(w.Body.String(), "rôle privé") {
		t.Fatalf("trash summaries: %s", w.Body.String())
	}
	if w := noteRecoveryCall(t, s, other, "POST", "/api/notes/restore", fmt.Sprintf(`{"id":"%d"}`, id)); w.Code != 404 {
		t.Fatalf("cross-user restore: %d", w.Code)
	}
	if w := noteRecoveryCall(t, s, owner, "POST", "/api/notes/restore", fmt.Sprintf(`{"id":"%d"}`, id)); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if n, err := ms.GetNote(ctx, scope, id); err != nil || n.Title != "Avant" {
		t.Fatalf("trash restore: %+v %v", n, err)
	}
}

func TestNotesRecoveryGroupHistoryAndTrashRespectAuthorRights(t *testing.T) {
	ms := securityStore(t)
	ctx := t.Context()
	s := New(Config{MultiUser: true})
	s.memStore = ms
	author := scopeFixtureUser(t, ms, "note-author", memory.RoleMember)
	member := scopeFixtureUser(t, ms, "note-reader", memory.RoleMember)
	admin := scopeFixtureUser(t, ms, "note-group-admin", memory.RoleMember)
	group, err := ms.CreateGroup(ctx, "Notes recovery")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []*memory.User{author, member, admin} {
		role := memory.GroupRoleMember
		if u.ID == admin.ID {
			role = memory.GroupRoleAdmin
		}
		if err := ms.AddGroupMember(ctx, group.ID, u.ID, role); err != nil {
			t.Fatal(err)
		}
	}
	gscope := fmt.Sprintf("g%d", group.ID)
	id, err := ms.ShareNote(ctx, gscope, author.ID, 42, "Published", "OLDER REDACTED BODY", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.UpdateNote(ctx, gscope, id, "Published", "Current public content", ""); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/notes/history?scope=group&id=%d", id)
	for _, u := range []*memory.User{author, admin} {
		if w := noteRecoveryCall(t, s, u, "GET", path, ""); w.Code != 200 {
			t.Fatalf("authorized history: %d %s", w.Code, w.Body.String())
		}
	}
	if w := noteRecoveryCall(t, s, member, "GET", path, ""); w.Code != 403 || strings.Contains(w.Body.String(), "REDACTED") {
		t.Fatalf("reader saw old group content: %d %s", w.Code, w.Body.String())
	}
	if err := ms.DeleteNote(ctx, gscope, id); err != nil {
		t.Fatal(err)
	}
	if w := noteRecoveryCall(t, s, member, "GET", "/api/notes/trash", ""); w.Code != 200 || strings.Contains(w.Body.String(), "Published") {
		t.Fatalf("reader saw group trash: %d %s", w.Code, w.Body.String())
	}
	restore := fmt.Sprintf(`{"id":"%d","scope":"group"}`, id)
	if w := noteRecoveryCall(t, s, member, "POST", "/api/notes/restore", restore); w.Code != 403 {
		t.Fatalf("reader restore: %d", w.Code)
	}
	if w := noteRecoveryCall(t, s, admin, "POST", "/api/notes/restore", restore); w.Code != 200 {
		t.Fatalf("admin restore: %d %s", w.Code, w.Body.String())
	}
	// Re-publication of the same deleted source revives its own copy, preserving history/id.
	if err := ms.DeleteNote(ctx, gscope, id); err != nil {
		t.Fatal(err)
	}
	id2, err := ms.ShareNote(ctx, gscope, author.ID, 42, "Published again", "New publication", "")
	if err != nil || id2 != id {
		t.Fatalf("re-share created duplicate: %d %v", id2, err)
	}
	if w := noteRecoveryCall(t, s, &memory.User{ID: -group.ID}, "GET", "/api/notes/history?id="+strconv.FormatInt(id, 10), ""); w.Code != 403 {
		t.Fatalf("shared agent entered personal recovery: %d", w.Code)
	}
}

func TestNotesRecoveryHistoryPagesAreBounded(t *testing.T) {
	ms := securityStore(t)
	ctx := t.Context()
	s := New(Config{})
	s.memStore = ms
	id, err := ms.AddNote(ctx, "global", "Page", "initial", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 53; i++ {
		if err := ms.UpdateNote(ctx, "global", id, "Page", fmt.Sprintf("Long version %d ", i)+strings.Repeat("é", 500), ""); err != nil {
			t.Fatal(err)
		}
	}
	path := fmt.Sprintf("/api/notes/history?id=%d", id)
	w := noteRecoveryCall(t, s, serviceUser, "GET", path, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var page struct {
		Versions   []struct{ ID, Body string }
		NextBefore string
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Versions) != 50 || page.NextBefore == "" || strings.Contains(w.Body.String(), "Long version") {
		t.Fatalf("unbounded/full body page: %s", w.Body.String())
	}
	w = noteRecoveryCall(t, s, serviceUser, "GET", path+"&before="+page.NextBefore, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var older struct {
		Versions   []struct{ ID string }
		NextBefore string
	}
	json.Unmarshal(w.Body.Bytes(), &older)
	if len(older.Versions) != 3 || older.NextBefore != "" {
		t.Fatalf("wrong older history: %s", w.Body.String())
	}
}
