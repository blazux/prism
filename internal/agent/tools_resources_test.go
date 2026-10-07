package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"prism/internal/customtools"
	"prism/internal/docker"
	"prism/internal/resources"
)

func TestResourceListDefaultsToCurrentDashboardAndTransitiveDependencies(t *testing.T) {
	e := resourceFixture(t)
	nodes := []resources.Node{
		{ID: "cron:refresh", Kind: "cron", Dependencies: []string{"tool:fetch"}},
		{ID: "file:data/shared.json", Kind: "file", UsedBy: []string{"tool:fetch", "widget:a/one", "widget:b/other"}},
		{ID: "tool:fetch", Kind: "tool", Dependencies: []string{"file:data/shared.json"}},
		{ID: "tool:unused", Kind: "tool", Session: "a"},
		{ID: "widget:a/one", Kind: "widget", Name: "one", Session: "a", Dependencies: []string{"cron:refresh"}},
		{ID: "widget:b/other", Kind: "widget", Name: "other", Session: "b"},
	}
	page, err := e.resourceListPage(nodes, map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Scope != "workspace" || page.Total != 5 {
		t.Fatal("unrelated dashboard included or dependency missed", page)
	}
	page, err = e.resourceListPage(nodes, map[string]interface{}{"widget": "one"})
	if err != nil || page.Scope != "widget" || page.Total != 4 {
		t.Fatal("widget filter lost transitive dependencies", page, err)
	}
	for _, n := range page.Resources {
		if n.ID == "file:data/shared.json" && n.ConsumerCount != 3 {
			t.Fatal("shared consumers were filtered out", n)
		}
	}
	if _, err := e.resourceListPage(nodes, map[string]interface{}{"widget": "other"}); err == nil {
		t.Fatal("other dashboard accepted as current widget")
	}
}

func TestResourceListGlobalPaginationKeepsJSONAndAllIDs(t *testing.T) {
	e := resourceFixture(t)
	nodes := make([]resources.Node, 1000)
	for i := range nodes {
		nodes[i] = resources.Node{ID: fmt.Sprintf("file:data/item-%04d.json", i), Kind: "file", Name: fmt.Sprintf("data/item-%04d.json", i), UsedBy: []string{}, Reason: "Legacy resource: review before cleanup."}
	}
	seen := map[string]bool{}
	offset := 0
	for {
		page, err := e.resourceListPage(nodes, map[string]interface{}{"all": true, "offset": float64(offset)})
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(page)
		if page.Total != 1000 || page.Scope != "environment" || !json.Valid([]byte(e.capResult(string(b)))) || len(b) >= maxToolResultBytes {
			t.Fatal("page was truncated or reported wrong total", page.Total, len(b))
		}
		for _, n := range page.Resources {
			if seen[n.ID] {
				t.Fatal("pagination duplicated resource", n.ID)
			}
			seen[n.ID] = true
		}
		if page.NextOffset == nil {
			break
		}
		if *page.NextOffset <= offset {
			t.Fatal("pagination made no progress")
		}
		offset = *page.NextOffset
	}
	if len(seen) != len(nodes) {
		t.Fatal("pagination lost resources", len(seen))
	}
	for _, args := range []map[string]interface{}{{"all": "true"}, {"offset": -1.0}, {"offset": 1.5}, {"offset": "1"}} {
		if _, err := e.resourceListPage(nodes, args); err == nil {
			t.Fatal("invalid pagination accepted", args)
		}
	}
}

func TestResourceListBoundsDenseReferencesWithoutMutatingInventory(t *testing.T) {
	e := resourceFixture(t)
	refs := make([]string, 500)
	for i := range refs {
		refs[i] = "widget:a/" + strings.Repeat("x", 220) + fmt.Sprint(i)
	}
	nodes := []resources.Node{{ID: "tool:shared", Kind: "tool", Name: "shared", UsedBy: refs, Dependencies: refs, Dedicated: refs}}
	page, err := e.resourceListPage(nodes, map[string]interface{}{"all": true})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(page)
	if len(b) >= maxToolResultBytes || page.Resources[0].ConsumerCount != 500 || page.Resources[0].DependencyCount != 500 || len(nodes[0].UsedBy) != 500 {
		t.Fatal("dense references lost counts or mutated snapshot", len(b), page)
	}
}

func TestResourceListKeepsFullProgrammaticInventory(t *testing.T) {
	e := resourceFixture(t)
	if _, err := e.writeFile("data/legacy.json", "{}"); err != nil {
		t.Fatal(err)
	}
	result, err := e.resourceTool(context.Background(), map[string]interface{}{"action": "list"})
	if err != nil {
		t.Fatal(err)
	}
	var page resourceListPage
	if err := json.Unmarshal([]byte(result), &page); err != nil || page.Total != 0 {
		t.Fatal("unassigned data included by default", result, err)
	}
	e.SetRawResults(true)
	result, err = e.resourceTool(context.Background(), map[string]interface{}{"action": "list"})
	if err != nil {
		t.Fatal(err)
	}
	var rows []resources.Node
	if err := json.Unmarshal([]byte(result), &rows); err != nil || len(rows) != 1 || rows[0].ID != "file:data/legacy.json" {
		t.Fatal("Settings/full programmatic inventory changed", result, err)
	}
}

func resourceFixture(t *testing.T) *ToolExecutor {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "plugins", "a")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "data"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "agent_tools"), 0700); err != nil {
		t.Fatal(err)
	}
	e := NewToolExecutor(nil, root, dir, "", "fixture-private-token")
	e.SetSessionID("a")
	e.SetCustomTools(customtools.LoadManager(filepath.Join(root, "agent_tools")), nil)
	return e
}

func resourceCron(t *testing.T, e *ToolExecutor, raw *string, failInstall *bool) {
	t.Helper()
	e.docker = docker.WithExecution(func(ctx context.Context, command string, input []byte, env map[string]string) (string, error) {
		switch {
		case command == docker.ReadCrontabCommand:
			return *raw, nil
		case strings.HasPrefix(command, "crontab /workspace/"):
			if *failInstall {
				return "", errors.New("fixture install failure")
			}
			b, err := os.ReadFile(filepath.Join(e.workspaceDir, filepath.Base(strings.TrimPrefix(command, "crontab "))))
			if err != nil {
				return "", err
			}
			*raw = string(b)
			return "", nil
		case strings.Contains(command, "'ps'"):
			return "", nil
		default:
			return "", errors.New("unexpected workspace command")
		}
	}).WithWorkspaceServices(e.workspaceDir)
}

func TestResourcesPreserveCrossDashboardDataAndLegacyBackends(t *testing.T) {
	e := resourceFixture(t)
	if _, err := e.writeFile("data/shared.json", "{}"); err != nil {
		t.Fatal(err)
	}
	writeWidget(t, e.pluginDir, "one", `<script>fetch('/data/shared.json')</script>`)
	other := filepath.Join(filepath.Dir(e.pluginDir), "b")
	os.MkdirAll(other, 0700)
	writeWidget(t, other, "two", `<script>fetch('/data/shared.json')</script>`)
	r, err := e.CleanupResources(context.Background(), "one", false, []string{"file:data/shared.json"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Removed) != 1 || r.Removed[0] != "widget:a/one" {
		t.Fatal(r)
	}
	if _, err := os.Stat(filepath.Join(e.workspaceDir, "data/shared.json")); err != nil {
		t.Fatal("shared data deleted", err)
	}
}

func TestResourcesCronFailureKeepsWidgetAndData(t *testing.T) {
	e := resourceFixture(t)
	raw := "# agent-job: refresh\n# agent-owner: a\n*/5 * * * * echo data/x.json\n"
	fail := true
	resourceCron(t, e, &raw, &fail)
	if _, err := e.writeFile("data/x.json", "{}"); err != nil {
		t.Fatal(err)
	}
	writeWidget(t, e.pluginDir, "one", `<script>fetch('/data/x.json')</script>`)
	r, err := e.CleanupResources(context.Background(), "one", false, []string{"cron:refresh", "file:data/x.json"}, false)
	if err == nil || len(r.Removed) != 0 {
		t.Fatal("failed install reported successful cleanup", r, err)
	}
	for _, p := range []string{filepath.Join(e.pluginDir, "one.html"), filepath.Join(e.workspaceDir, "data/x.json")} {
		if _, err := os.Stat(p); err != nil {
			t.Fatal("failed cleanup removed dependency", err)
		}
	}
	fail = false
	r, err = e.CleanupResources(context.Background(), "one", false, []string{"cron:refresh", "file:data/x.json"}, false)
	if err != nil || len(r.Removed) != 3 {
		t.Fatal(r, err)
	}
	if strings.TrimSpace(raw) != "" {
		t.Fatal("last job remained in live crontab")
	}
	b, err := os.ReadFile(filepath.Join(e.workspaceDir, ".crontab"))
	if err != nil || strings.TrimSpace(string(b)) != "" {
		t.Fatal("job could resurrect on restart", err, string(b))
	}
}

func TestResourcesProducerRemainsForOtherDashboard(t *testing.T) {
	nodes := protectResourceProducers([]resources.Node{
		{ID: "widget:a/one", Kind: "widget", Dependencies: []string{"file:data/x.json"}},
		{ID: "widget:b/two", Kind: "widget", Dependencies: []string{"file:data/x.json"}},
		{ID: "cron:refresh", Kind: "cron", Tracked: true, Dependencies: []string{"tool:fetch"}},
		{ID: "tool:fetch", Kind: "tool", Tracked: true, Dependencies: []string{"file:data/x.json"}},
		{ID: "file:data/x.json", Kind: "file"},
	})
	p := resources.PlanRemoval(nodes, []string{"widget:a/one"}, []string{"cron:refresh", "tool:fetch", "file:data/x.json"})
	if len(p.Remove) != 1 || p.Remove[0].Kind != "widget" {
		t.Fatal("shared producer removed", p)
	}
}

func TestResourcesReportsExternalDeletionAndPreservesData(t *testing.T) {
	e := resourceFixture(t)
	writeWidget(t, e.pluginDir, "one", `<script>fetch('/data/missing.json')</script>`)
	nodes, err := e.ResourceInventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range nodes {
		if n.ID == "file:data/missing.json" {
			found = n.Missing && n.Protected && len(n.UsedBy) == 1
		}
	}
	if !found {
		t.Fatal("broken references missing from inventory", nodes)
	}
	r, err := e.CleanupResources(context.Background(), "one", false, nil, false)
	if err != nil || len(r.Removed) != 1 {
		t.Fatal("missing backend prevents obsolete widget removal", r, err)
	}
}

func TestResourcesDoNotExposeCronCredentials(t *testing.T) {
	e := resourceFixture(t)
	raw := "# agent-job: refresh\n# agent-owner: a\n@hourly PRISM_URL='http://prism-server:8080' PRISM_SESSION='a' PRISM_TOKEN='fixture-private-token' echo hello\n"
	fail := false
	resourceCron(t, e, &raw, &fail)
	nodes, err := e.ResourceInventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(nodes)
	if strings.Contains(string(b), "fixture-private-token") || strings.Contains(string(b), "echo hello") {
		t.Fatal("crontab credentials/commands leaked", string(b))
	}
}

func TestResourcesPermissionPreflightAndPathBoundary(t *testing.T) {
	e := resourceFixture(t)
	raw := "# agent-job: refresh\n# agent-owner: a\n@hourly echo data/x.json\n"
	fail := false
	resourceCron(t, e, &raw, &fail)
	e.writeFile("data/x.json", "{}")
	writeWidget(t, e.pluginDir, "one", `<script>fetch('/data/x.json')</script>`)
	e.SetToolGuard(func(name string, args map[string]interface{}) error {
		if name == "delete_file" {
			return errors.New("file deletion denied")
		}
		return nil
	})
	r, err := e.CleanupResources(context.Background(), "one", false, []string{"cron:refresh", "file:data/x.json"}, false)
	if err == nil || len(r.Removed) != 0 || !strings.Contains(raw, "# agent-job: refresh") {
		t.Fatal("denied cleanup was partially applied", r, err)
	}
	for _, id := range []string{"file:../private", "file:/etc/passwd", "file:.secret_key", "file:plugins/a/one.html", "tool:../bad"} {
		if validResourceID(id) {
			t.Fatal("unsafe ID accepted", id)
		}
	}
	if _, err := e.CleanupResources(context.Background(), "../b/two", false, nil, false); err == nil {
		t.Fatal("foreign widget path accepted")
	}
}

func TestResourcesLockedWidgetCannotBeRemovedByAgentOrMaintenance(t *testing.T) {
	e := resourceFixture(t)
	writeWidget(t, e.pluginDir, "one", "<p>one</p>")
	if err := os.WriteFile(filepath.Join(e.pluginDir, "one.meta.json"), []byte(`{"title":"one","locked":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.removeUIPlugin("one"); err == nil {
		t.Fatal("agent removed a locked widget")
	}
	r, err := e.CleanupResources(context.Background(), "one", false, nil, false)
	if err != nil || len(r.Removed) > 0 {
		t.Fatal(r, err)
	}
	r, err = e.CleanupResources(context.Background(), "", true, nil, false)
	if err != nil || len(r.Removed) != 1 {
		t.Fatal("explicit whole-dashboard deletion did not include its widgets", r, err)
	}
}
