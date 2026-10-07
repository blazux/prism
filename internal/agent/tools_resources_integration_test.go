package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"prism/internal/memory"
)

func resourceDatabase(t *testing.T) *memory.Store {
	t.Helper()
	dsn := os.Getenv("PRISM_TEST_SECRETS_URL")
	if dsn == "" {
		t.Skip("disposable PostgreSQL required")
	}
	ctx := context.Background()
	db, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("resource_lifecycle_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := db.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		db.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); db.Close(ctx) })
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := u.Query()
	query.Set("search_path", schema+",public")
	u.RawQuery = query.Encode()
	store, err := memory.NewStore(ctx, u.String(), bytes.Repeat([]byte{7}, 32), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	return store
}

func TestResourcesPersistentProvenanceAndAutomaticDedicatedCleanup(t *testing.T) {
	ctx := context.Background()
	ms := resourceDatabase(t)
	e := resourceFixture(t)
	e.SetMemoryStore(ms)
	raw := ""
	fail := false
	resourceCron(t, e, &raw, &fail)
	code := `# TOOL: {"name":"weather_fetch","description":"weather","parameters":{"type":"object","properties":{}}}
import json
open('/workspace/data/weather.json','w').write('{}')
`
	if _, err := e.registerTool(code); err != nil {
		t.Fatal(err)
	}
	if _, err := e.writeFile("data/weather.json", "{}"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.cronAdd(ctx, "weather", "@hourly", "python3 /workspace/agent_tools/weather_fetch.py", "Weather refresh"); err != nil {
		t.Fatal(err)
	}
	writeWidget(t, e.pluginDir, "weather", `<script>fetch('/data/weather.json')</script>`)
	if err := e.BindWidgetResources(ctx, "weather", []string{"tool:weather_fetch", "cron:weather", "file:data/weather.json"}); err != nil {
		t.Fatal(err)
	}
	// A new executor with the same database sees persisted provenance and links.
	other := NewToolExecutor(e.docker, e.workspaceDir, e.pluginDir, "", "")
	other.SetSessionID("a")
	other.SetMemoryStore(ms)
	other.SetCustomTools(e.customMgr, nil)
	r, err := other.CleanupResources(ctx, "weather", false, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Plan.Remove) != 3 {
		t.Fatal("dedicated producer/tool/widget not planned", r)
	}
	if len(r.Removed) != 0 {
		t.Fatal("preview mutated resources")
	}
	r, err = other.CleanupResources(ctx, "weather", false, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Removed) != 3 || strings.TrimSpace(raw) != "" {
		t.Fatal(r, raw)
	}
	if _, err := os.Stat(filepath.Join(e.workspaceDir, "data/weather.json")); err != nil {
		t.Fatal("data deleted automatically", err)
	}
	if e.customMgr.Get("weather_fetch") != nil {
		t.Fatal("deleted tool remains callable")
	}
}

func TestResourcesCreationDoesNotAdoptLegacyOrClobberParallelRecords(t *testing.T) {
	ctx := context.Background()
	ms := resourceDatabase(t)
	e := resourceFixture(t)
	e.SetMemoryStore(ms)
	if err := os.WriteFile(filepath.Join(e.workspaceDir, "data/legacy.json"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.writeFile("data/legacy.json", "new"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := e.writeFile(fmt.Sprintf("data/new-%d.json", i), "{}"); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	catalog, err := e.resourceCatalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 12 {
		t.Fatal("concurrent creation lost provenance", catalog)
	}
	if _, ok := catalog["file:data/legacy.json"]; ok {
		t.Fatal("overwrite adopted legacy file")
	}
}

func TestResourcesSharedGalleryAndOtherUsersProtectBackend(t *testing.T) {
	ctx := context.Background()
	ms := resourceDatabase(t)
	e := resourceFixture(t)
	e.SetMemoryStore(ms)
	e.SetMultiUserMode(true)
	e.SetSharingContext(1, nil)
	e.SetSessionID("u1-a")
	oldDir := e.pluginDir
	e.pluginDir = filepath.Join(filepath.Dir(oldDir), "u1-a")
	os.Rename(oldDir, e.pluginDir)
	code := `# TOOL: {"name":"shared_fetch","description":"shared","parameters":{"type":"object","properties":{}}}
print('{}')
`
	if _, err := e.registerTool(code); err != nil {
		t.Fatal(err)
	}
	writeWidget(t, e.pluginDir, "one", `<script>prismTool('shared_fetch',{})</script>`)
	if err := e.BindWidgetResources(ctx, "one", []string{"tool:shared_fetch"}); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(filepath.Dir(e.pluginDir), "u2-confidential-name")
	os.MkdirAll(foreign, 0700)
	writeWidget(t, foreign, "two", `<script>prismTool('shared_fetch',{})</script>`)
	r, err := e.CleanupResources(ctx, "one", false, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Plan.Remove) != 1 {
		t.Fatal("cross-user backend planned for deletion", r)
	}
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), "confidential-name") {
		t.Fatal("other user's dashboard name leaked", string(b))
	}
	os.RemoveAll(foreign)
	user, err := ms.CreateUser(ctx, "fixture", "irrelevant", "Fixture", "member", "approved")
	if err != nil {
		t.Fatal(err)
	}
	group, err := ms.CreateGroup(ctx, "Fixture group")
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(memory.SharedPayload{Widgets: []memory.SharedWidget{{Title: "Published", Content: "<p>dynamic backend</p>", Resources: []string{"tool:shared_fetch"}}}})
	if _, err := ms.ShareItem(ctx, memory.SharedItem{GroupID: group.ID, Kind: "widget", Title: "Published", OwnerID: user.ID, OwnerName: "Fixture", Payload: payload}); err != nil {
		t.Fatal(err)
	}
	r, err = e.CleanupResources(ctx, "one", false, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Removed) != 1 || e.customMgr.Get("shared_fetch") == nil {
		t.Fatal("published backend deleted", r)
	}
}

func TestResourcesOldWidgetDoesNotDeleteRecreatedBackend(t *testing.T) {
	ctx := context.Background()
	e := resourceFixture(t)
	e.SetMemoryStore(resourceDatabase(t))
	code := `# TOOL: {"name":"fetch","description":"fixture","parameters":{"type":"object","properties":{}}}
print('{}')
`
	if _, err := e.registerTool(code); err != nil {
		t.Fatal(err)
	}
	writeWidget(t, e.pluginDir, "one", `<p>Widget</p>`)
	if err := e.BindWidgetResources(ctx, "one", []string{"tool:fetch"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.deleteFile("agent_tools/fetch.py"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.registerTool(code); err != nil {
		t.Fatal(err)
	}
	r, err := e.CleanupResources(ctx, "one", false, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Removed) != 1 || e.customMgr.Get("fetch") == nil {
		t.Fatal("old declaration deleted replacement", r)
	}
}

func TestResourcesBoardDeletionPreservesAutonomousCron(t *testing.T) {
	ctx := context.Background()
	e := resourceFixture(t)
	e.SetMemoryStore(resourceDatabase(t))
	raw := "# agent-job: bound\n# agent-owner: a\n@hourly PRISM_SESSION='a' echo hello\n\n# agent-job: independent\n# agent-owner: a\n@hourly echo hello\n"
	fail := false
	resourceCron(t, e, &raw, &fail)
	for _, id := range []string{"cron:bound", "cron:independent"} {
		if err := e.recordResource(ctx, id, false); err != nil {
			t.Fatal(err)
		}
	}
	r, err := e.CleanupResources(ctx, "", true, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Removed) != 1 || r.Removed[0] != "cron:bound" || strings.Contains(raw, "agent-job: bound") || !strings.Contains(raw, "agent-job: independent") {
		t.Fatal("standalone cron removed or bound cron retained", r, raw)
	}
}
