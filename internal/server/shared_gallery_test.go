package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"prism/internal/memory"
)

func TestSharedGalleryUpgradeKeepsLegacySharesAndGroupAccess(t *testing.T) {
	dsn := os.Getenv("PRISM_TEST_SECRETS_URL")
	if dsn == "" {
		t.Skip("disposable PostgreSQL required")
	}
	ctx := context.Background()
	db, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("share_upgrade_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := db.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); db.Close(ctx) })
	// Create precisely the old table, before applying today's application schema.
	_, err = db.Exec(ctx, `CREATE TABLE `+quoted+`.shared_items (
	 id BIGSERIAL PRIMARY KEY, group_id BIGINT NOT NULL, kind TEXT NOT NULL,
	 title TEXT NOT NULL DEFAULT '', owner_id BIGINT NOT NULL DEFAULT 0,
	 owner_name TEXT NOT NULL DEFAULT '', payload JSONB NOT NULL DEFAULT '{}',
	 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`)
	if err != nil {
		t.Fatal(err)
	}
	legacy := `{"widgets":[{"title":"Legacy clock","content":"<div>original clock</div>","cols":2,"height":300}]}`
	_, err = db.Exec(ctx, `INSERT INTO `+quoted+`.shared_items (id,group_id,kind,title,owner_id,owner_name,payload,created_at)
	 VALUES (73,1,'widget','Legacy clock',7,'Old author',$1,'2026-01-02 03:04:05+00')`, legacy)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(dsn)
	query := u.Query()
	query.Set("search_path", schema+",public")
	u.RawQuery = query.Encode()
	key := bytes.Repeat([]byte{7}, 32)
	ms, err := memory.NewStore(ctx, u.String(), key, false)
	if err != nil {
		t.Fatal(err)
	}
	// Starting again must be safe too (idempotent ADD COLUMN IF NOT EXISTS).
	ms.Close()
	ms, err = memory.NewStore(ctx, u.String(), key, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ms.Close)
	group, err := ms.CreateGroup(ctx, "Fixture group")
	if err != nil || group.ID != 1 {
		t.Fatal(group, err)
	}
	member, err := ms.CreateUser(ctx, "member@example.test", "unused", "Member", memory.RoleMember, memory.StatusApproved)
	if err != nil {
		t.Fatal(err)
	}
	outsider, err := ms.CreateUser(ctx, "outsider@example.test", "unused", "Outsider", memory.RoleMember, memory.StatusApproved)
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.AddGroupMember(ctx, group.ID, member.ID, "member"); err != nil {
		t.Fatal(err)
	}
	items, err := ms.SharedItemsForGroups(ctx, []int64{group.ID}, "")
	if err != nil || len(items) != 1 || items[0].ID != 73 || items[0].Description != "" || items[0].HasPreview || items[0].Count != 1 {
		t.Fatal("legacy catalogue changed", items, err)
	}
	item, ok, err := ms.SharedItemByID(ctx, 73)
	if err != nil || !ok || item.Title != "Legacy clock" || item.OwnerName != "Old author" || item.CreatedAt.UTC().Format(time.RFC3339) != "2026-01-02T03:04:05Z" || !strings.Contains(string(item.Payload), "original clock") {
		t.Fatal("legacy publication changed", item, err)
	}
	root := t.TempDir()
	s := &Server{memStore: ms, cfg: Config{MultiUser: true, WorkspaceDir: root, PluginDir: filepath.Join(root, "plugins")}}
	call := func(method, path, body string, user *memory.User, list bool) *httptest.ResponseRecorder {
		t.Helper()
		r := withUser(httptest.NewRequest(method, path, strings.NewReader(body)), user)
		w := httptest.NewRecorder()
		if list {
			s.handleShared(w, r)
		} else {
			s.handleSharedItem(w, r)
		}
		return w
	}
	w := call("POST", "/api/shared/73/add?session=target", "", member, false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"added":1`) {
		t.Fatal("legacy share no longer addable", w.Code, w.Body.String())
	}
	path := filepath.Join(root, "plugins", fmt.Sprintf("u%d-target", member.ID), "legacy-clock-shared73-0.html")
	if content, err := os.ReadFile(path); err != nil || string(content) != "<div>original clock</div>" {
		t.Fatal("legacy widget content changed", string(content), err)
	}
	if w := call("GET", "/api/shared/73/preview", "", member, false); w.Code != 404 {
		t.Fatal("legacy missing preview", w.Code)
	}
	var jpegBytes bytes.Buffer
	jpeg.Encode(&jpegBytes, image.NewRGBA(image.Rect(0, 0, 40, 30)), nil)
	id, err := ms.ShareItem(ctx, memory.SharedItem{GroupID: group.ID, Kind: "widget", Title: "New clock", Description: " A compact clock. ", OwnerID: member.ID, Payload: item.Payload, Preview: jpegBytes.Bytes()})
	if err != nil {
		t.Fatal(err)
	}
	previewURL := fmt.Sprintf("/api/shared/%d/preview", id)
	w = call("GET", previewURL, "", member, false)
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" || !bytes.Equal(w.Body.Bytes(), jpegBytes.Bytes()) || !strings.Contains(w.Header().Get("Cache-Control"), "private") {
		t.Fatal("preview not served privately", w.Code)
	}
	if w := call("GET", previewURL, "", outsider, false); w.Code != 403 {
		t.Fatal("cross-group preview allowed", w.Code)
	}
	w = call("GET", "/api/shared", "", member, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "A compact clock.") || !strings.Contains(w.Body.String(), `"hasPreview":true`) || strings.Contains(w.Body.String(), "original clock") {
		t.Fatal("catalogue omitted metadata or exposed payload", w.Body.String())
	}
	w = call("GET", "/api/shared", "", outsider, true)
	if w.Code != 200 || strings.Contains(w.Body.String(), "New clock") {
		t.Fatal("cross-group catalogue exposed", w.Body.String())
	}
	// The preview lives in the publication, not the source workspace.
	os.RemoveAll(filepath.Join(root, "plugins"))
	if w := call("GET", previewURL, "", member, false); w.Code != 200 {
		t.Fatal("source removal lost snapshot", w.Code)
	}
	if w := call("DELETE", fmt.Sprintf("/api/shared/%d", id), "", member, false); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("GET", previewURL, "", member, false); w.Code != 404 {
		t.Fatal("unshared image remains accessible", w.Code)
	}
	// Existing clients can still publish without either of the new fields.
	source := filepath.Join(root, "plugins", fmt.Sprintf("u%d-source", member.ID))
	os.MkdirAll(source, 0755)
	os.WriteFile(filepath.Join(source, "clock.html"), []byte("<div>clock</div>"), 0644)
	os.WriteFile(filepath.Join(source, "clock.meta.json"), []byte(`{"title":"Clock"}`), 0644)
	for _, description := range []string{"", "Useful clock"} {
		b, _ := json.Marshal(map[string]any{"kind": "widget", "widgetId": "clock", "groupId": group.ID, "session": "source", "description": description})
		w := call("POST", "/api/shared", string(b), member, true)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Preview unavailable") {
			t.Fatal("no-browser publication failed", w.Code, w.Body.String())
		}
	}
	b, _ := json.Marshal(map[string]any{"description": strings.Repeat("x", 1001)})
	if w := call("POST", "/api/shared", string(b), member, true); w.Code != 400 {
		t.Fatal("oversized description accepted", w.Code)
	}
}
