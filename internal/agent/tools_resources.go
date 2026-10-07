package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"prism/internal/docker"
	"prism/internal/memory"
	"prism/internal/resources"
	"prism/internal/workspace"
)

const resourceCatalogKey = "workspace_resource_catalog_v1"

type resourceOrigin struct {
	Session   string    `json:"session"`
	CreatedAt time.Time `json:"createdAt"`
}

func (e *ToolExecutor) resourceVersions(ctx context.Context, refs []string) (map[string]string, error) {
	catalog, err := e.resourceCatalog(ctx)
	if err != nil {
		return nil, err
	}
	versions := map[string]string{}
	for _, id := range refs {
		if origin, ok := catalog[id]; ok {
			versions[id] = origin.CreatedAt.Format(time.RFC3339Nano)
		}
	}
	return versions, nil
}

func (e *ToolExecutor) resourceSession() string {
	if e.sessionID == "" {
		return "default"
	}
	return e.sessionID
}

func (e *ToolExecutor) resourceCatalog(ctx context.Context) (map[string]resourceOrigin, error) {
	out := map[string]resourceOrigin{}
	if e.memStore == nil {
		return out, nil
	}
	value, ok, err := e.memStore.GetConfig(ctx, resourceCatalogKey)
	if err != nil {
		return nil, err
	}
	if ok && json.Unmarshal([]byte(value), &out) != nil {
		return nil, fmt.Errorf("resource catalog is unreadable; cleanup refused")
	}
	if out == nil {
		out = map[string]resourceOrigin{}
	}
	return out, nil
}

// Creation tracking does not make a resource exclusively owned. Existing files
// and legacy jobs are deliberately not adopted when edited or re-registered.
func (e *ToolExecutor) recordResource(ctx context.Context, id string, existed bool) error {
	if existed || e.memStore == nil {
		return nil
	}
	catalog, err := e.resourceCatalog(ctx)
	if err != nil {
		return err
	}
	catalog[id] = resourceOrigin{Session: e.resourceSession(), CreatedAt: time.Now().UTC()}
	b, err := json.Marshal(catalog)
	if err != nil {
		return err
	}
	return e.memStore.SetConfig(ctx, resourceCatalogKey, string(b))
}

func (e *ToolExecutor) ownResourceSession(session string) bool {
	if !e.multiUser {
		return true
	}
	if e.actingUserID > 0 {
		return strings.HasPrefix(session, fmt.Sprintf("u%d-", e.actingUserID))
	}
	return session == e.resourceSession()
}

func validResourceID(id string) bool {
	kind, name, ok := strings.Cut(id, ":")
	if !ok || name == "" || strings.ContainsAny(name, "\r\n\x00") {
		return false
	}
	switch kind {
	case "file":
		return !filepath.IsAbs(name) && filepath.Clean(name) == name && name != "." && name != ".." && !strings.HasPrefix(name, "../") && !strings.HasPrefix(name, ".") && !strings.HasPrefix(name, "plugins/") && !strings.HasPrefix(name, "agent_tools/")
	case "tool", "cron", "service":
		return !strings.ContainsAny(name, "/\\")
	}
	return false
}

var resourceFileRef = regexp.MustCompile(`(?:/workspace/)?(?:data|agent_tools)/[a-zA-Z0-9_./-]+`)
var resourceCronSession = regexp.MustCompile(`(?:^| )PRISM_SESSION='([a-z0-9-]+)'(?: |$)`)

func resourceReferences(source string, scriptTools map[string]string, toolIDs []string) []string {
	refs := map[string]bool{}
	for _, match := range resourceFileRef.FindAllString(source, -1) {
		path := strings.TrimPrefix(match, "/workspace/")
		if id, ok := scriptTools[path]; ok {
			refs[id] = true
		} else if validResourceID("file:" + path) {
			refs["file:"+path] = true
		}
	}
	// Tool names are identifiers, not substrings: don't confuse e.g. weather
	// with weather_history. This is a positive guard, not proof of no usage.
	for _, id := range toolIDs {
		name := strings.TrimPrefix(id, "tool:")
		if strings.Contains(source, "'"+name+"'") || strings.Contains(source, `"`+name+`"`) {
			refs[id] = true
		}
	}
	out := make([]string, 0, len(refs))
	for ref := range refs {
		out = append(out, ref)
	}
	sort.Strings(out)
	return out
}

func uniqueResources(ids []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// resourceSnapshot inventories every board, every custom tool and the complete
// managed crontab in this environment. Cross-user consumers protect resources
// internally; their names/content are never returned to another user.
func (e *ToolExecutor) resourceSnapshot(ctx context.Context) ([]resources.Node, error) {
	catalog, err := e.resourceCatalog(ctx)
	if err != nil {
		return nil, err
	}
	nodes := []resources.Node{}
	sources := map[string]string{}
	scriptTools := map[string]string{}
	toolIDs := []string{}
	if e.customMgr != nil {
		e.customMgr.Reload()
		for _, t := range e.customMgr.All() {
			id := "tool:" + t.Name
			toolIDs = append(toolIDs, id)
			scriptTools[filepath.ToSlash(filepath.Join(filepath.Base(e.customMgr.Dir()), t.Filename))] = id
			origin, tracked := catalog[id]
			nodes = append(nodes, resources.Node{ID: id, Kind: "tool", Name: t.Name, Session: origin.Session, Tracked: tracked, Protected: t.Protected || (e.multiUser && (!tracked || !e.ownResourceSession(origin.Session)))})
		}
		for i := range nodes {
			t := e.customMgr.Get(nodes[i].Name)
			if t == nil {
				return nil, fmt.Errorf("custom tools changed during inventory; retry")
			}
			b, err := e.readManagedFile(filepath.Join(e.customMgr.Dir(), t.Filename))
			if err != nil {
				return nil, fmt.Errorf("cannot inspect custom tool %q: %w", t.Name, err)
			}
			nodes[i].Dependencies = resourceReferences(string(b), scriptTools, toolIDs)
			sources[nodes[i].ID] = string(b)
		}
	}
	root := filepath.Dir(e.pluginDir)
	boards, err := e.readManagedDir(root)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("cannot inspect dashboards: %w", err)
	}
	for _, board := range boards {
		if !board.IsDir() {
			continue
		}
		dir := filepath.Join(root, board.Name())
		boardSession := board.Name()
		if filepath.Clean(dir) == filepath.Clean(e.pluginDir) {
			boardSession = e.resourceSession()
		}
		files, err := e.readManagedDir(dir)
		if err != nil {
			return nil, err
		}
		for _, file := range files {
			if !strings.HasSuffix(file.Name(), ".html") {
				continue
			}
			id := strings.TrimSuffix(file.Name(), ".html")
			b, err := e.readManagedFile(filepath.Join(dir, file.Name()))
			if err != nil {
				return nil, err
			}
			var m pluginMeta
			mb, err := e.readManagedFile(filepath.Join(dir, id+".meta.json"))
			if err != nil && !os.IsNotExist(err) {
				return nil, err
			}
			if err == nil && json.Unmarshal(mb, &m) != nil {
				return nil, fmt.Errorf("unreadable widget metadata: cleanup refused")
			}
			sources["widget:"+boardSession+"/"+id] = string(b)
			deps := append(resourceReferences(string(b), scriptTools, toolIDs), m.Resources...)
			dedicated := []string{}
			for _, ref := range m.Resources {
				if origin, ok := catalog[ref]; ok && m.ResourceVersions[ref] == origin.CreatedAt.Format(time.RFC3339Nano) {
					dedicated = append(dedicated, ref)
				}
			}
			nodes = append(nodes, resources.Node{ID: "widget:" + boardSession + "/" + id, Kind: "widget", Name: id, Session: boardSession, Protected: m.Locked || !e.ownResourceSession(boardSession), Dependencies: uniqueResources(deps), Dedicated: dedicated})
		}
	}
	if e.multiUser && e.memStore != nil {
		payloads, err := e.memStore.ResourceSharedPayloads(ctx)
		if err != nil {
			return nil, fmt.Errorf("cannot inspect shared gallery: %w", err)
		}
		for i, payload := range payloads {
			var p memory.SharedPayload
			if json.Unmarshal(payload, &p) != nil {
				return nil, fmt.Errorf("unreadable shared gallery: cleanup refused")
			}
			deps := []string{}
			for _, w := range p.Widgets {
				sources[fmt.Sprintf("gallery:%d", i)] += w.Content + "\n"
				deps = append(deps, resourceReferences(w.Content, scriptTools, toolIDs)...)
				deps = append(deps, w.Resources...)
			}
			nodes = append(nodes, resources.Node{ID: fmt.Sprintf("gallery:%d", i), Kind: "gallery", Protected: true, Dependencies: uniqueResources(deps)})
		}
	}
	if e.docker != nil {
		raw, err := e.docker.Exec(ctx, docker.ReadCrontabCommand, 10*time.Second)
		if err != nil {
			return nil, docker.CronReadError(err, raw)
		}
		for _, j := range ParseCronJobs(raw) {
			id := "cron:" + j.Name
			origin, tracked := catalog[id]
			sources[id] = displayCommand(j.Command)
			bound := ""
			if m := resourceCronSession.FindStringSubmatch(j.Command); len(m) > 1 {
				bound = m[1]
			}
			nodes = append(nodes, resources.Node{ID: id, Kind: "cron", Name: j.Name, Session: origin.Session, BoundSession: bound, Tracked: tracked, Protected: !e.ownsCronJob(j) || (e.multiUser && j.Owner == "" && !e.globalAdmin), Dependencies: resourceReferences(displayCommand(j.Command), scriptTools, toolIDs)})
		}
	}
	if e.docker != nil {
		services, err := e.docker.ListServices(ctx)
		if err != nil {
			return nil, fmt.Errorf("cannot inspect services: %w", err)
		}
		for _, svc := range services {
			id := "service:" + svc.Name
			origin, tracked := catalog[id]
			nodes = append(nodes, resources.Node{ID: id, Kind: "service", Name: svc.Name, Session: origin.Session, Tracked: tracked, Protected: e.multiUser && (!tracked || !e.ownResourceSession(origin.Session))})
			for i, n := range nodes {
				source := sources[n.ID]
				if strings.Contains(source, "://"+svc.Name+".localhost") || strings.Contains(source, "://prism-svc-"+svc.Name+":") || (svc.Port > 0 && strings.Contains(source, e.docker.ServiceURL(svc.Port))) {
					nodes[i].Dependencies = uniqueResources(append(nodes[i].Dependencies, id))
				}
			}
		}
	}
	// Known files include legacy data and files recorded by the file tools.
	files := map[string]bool{}
	for id := range catalog {
		if strings.HasPrefix(id, "file:") {
			files[strings.TrimPrefix(id, "file:")] = true
		}
	}
	inspected := 0
	var visit func(string) error
	visit = func(dir string) error {
		entries, err := workspace.ReadDir(e.workspaceDir, dir)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			inspected++
			if inspected > 20000 || strings.Count(dir, "/") > 128 {
				return fmt.Errorf("data inventory exceeds safe scan bounds; narrow the data tree before cleanup")
			}
			path := filepath.ToSlash(filepath.Join(dir, entry.Name()))
			if entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			if entry.IsDir() {
				if err := visit(path); err != nil {
					return err
				}
			} else {
				files[path] = true
			}
			if len(files) > 10000 {
				return fmt.Errorf("resource inventory exceeds 10000 files; narrow the data directory before cleanup")
			}
		}
		return nil
	}
	if err := visit("data"); err != nil {
		return nil, err
	}
	for path := range files {
		id := "file:" + path
		if !validResourceID(id) {
			continue
		}
		f, err := workspace.Open(e.workspaceDir, path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		stat, err := f.Stat()
		f.Close()
		if err != nil {
			return nil, err
		}
		if !stat.Mode().IsRegular() {
			continue
		}
		origin, tracked := catalog[id]
		nodes = append(nodes, resources.Node{ID: id, Kind: "file", Name: path, Session: origin.Session, Tracked: tracked, Protected: e.multiUser && (!tracked || !e.ownResourceSession(origin.Session))})
	}
	known := map[string]bool{}
	for _, n := range nodes {
		known[n.ID] = true
	}
	missing := map[string]bool{}
	for _, n := range nodes {
		for _, dep := range n.Dependencies {
			if validResourceID(dep) && !known[dep] {
				missing[dep] = true
			}
		}
	}
	for id := range missing {
		kind, name, _ := strings.Cut(id, ":")
		origin, tracked := catalog[id]
		nodes = append(nodes, resources.Node{ID: id, Kind: kind, Name: name, Session: origin.Session, Tracked: tracked, Missing: true, Protected: true, Reason: "Already missing; check the widgets referencing it."})
	}
	// A second widget reading a cron/tool's data keeps that producer too,
	// even if it does not itself call the tool or name the job.
	nodes = protectResourceProducers(nodes)
	consumers := map[string]map[string]bool{}
	for _, n := range nodes {
		for _, dep := range n.Dependencies {
			if dep == n.ID {
				continue
			}
			if consumers[dep] == nil {
				consumers[dep] = map[string]bool{}
			}
			consumers[dep][n.ID] = true
		}
	}
	for i := range nodes {
		nodes[i].UsedBy = []string{}
		for consumer := range consumers[nodes[i].ID] {
			nodes[i].UsedBy = append(nodes[i].UsedBy, consumer)
		}
		sort.Strings(nodes[i].UsedBy)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	return nodes, nil
}

func (e *ToolExecutor) visibleResources(nodes []resources.Node) []resources.Node {
	out := []resources.Node{}
	for _, n := range nodes {
		if n.Kind == "gallery" || (n.Kind == "widget" && !e.ownResourceSession(n.Session)) || (e.multiUser && n.Session != "" && !e.ownResourceSession(n.Session)) {
			continue
		}
		for i, consumer := range n.UsedBy {
			if strings.HasPrefix(consumer, "gallery:") || (strings.HasPrefix(consumer, "widget:") && !e.ownResourceSession(strings.SplitN(strings.TrimPrefix(consumer, "widget:"), "/", 2)[0])) {
				n.UsedBy[i] = "shared consumer"
			}
		}
		if n.Missing {
			n.Reason = "Missing resource: repair its references or remove the widget."
		} else if n.Protected {
			n.Reason = "Protected or not owned by this account."
		} else if len(n.UsedBy) > 0 {
			n.Reason = "In use."
		} else if !n.Tracked {
			n.Reason = "Legacy resource: usage may be unknown. Review before cleanup."
		} else {
			n.Reason = "No detected consumer. Review before cleanup."
		}
		out = append(out, n)
	}
	return out
}

// BindWidgetResources declares these resources dedicated to a widget. Other
// observed consumers still protect them; files remain manual cleanup only.
func (e *ToolExecutor) BindWidgetResources(ctx context.Context, id string, refs []string) error {
	unlock := resources.Lock(e.workspaceDir)
	defer unlock()
	if id == "" || strings.ContainsAny(id, "/\\") || id == "." || id == ".." {
		return fmt.Errorf("invalid widget id")
	}
	if err := e.validateResourceBindings(ctx, refs); err != nil {
		return err
	}
	path := filepath.Join(e.pluginDir, id+".meta.json")
	b, err := e.readManagedFile(path)
	if err != nil {
		return err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	if locked, _ := m["locked"].(bool); locked {
		return fmt.Errorf("widget is locked")
	}
	m["resources"] = uniqueResources(refs)
	versions, err := e.resourceVersions(ctx, refs)
	if err != nil {
		return err
	}
	m["resourceVersions"] = versions
	b, err = json.Marshal(m)
	if err != nil {
		return err
	}
	return e.writeManagedFile(path, b)
}

func (e *ToolExecutor) validateResourceBindings(ctx context.Context, refs []string) error {
	nodes, err := e.resourceSnapshot(ctx)
	if err != nil {
		return err
	}
	known := map[string]resources.Node{}
	for _, n := range nodes {
		known[n.ID] = n
	}
	for _, ref := range refs {
		n, ok := known[ref]
		if !validResourceID(ref) || !ok || n.Protected {
			return fmt.Errorf("resource %q is missing, protected or not owned by this account", ref)
		}
	}
	return nil
}

func (e *ToolExecutor) resourcePlan(ctx context.Context, widgetID string, wholeBoard bool, selected []string) (resources.Plan, error) {
	nodes, err := e.resourceSnapshot(ctx)
	if err != nil {
		return resources.Plan{}, err
	}
	roots := []string{}
	for _, n := range nodes {
		if n.Kind == "widget" && n.Session == e.resourceSession() && (wholeBoard || n.Name == widgetID) {
			roots = append(roots, n.ID)
		}
	}
	if widgetID != "" && len(roots) == 0 {
		return resources.Plan{}, fmt.Errorf("widget %q not found. %s", widgetID, e.existingWidgetsHint())
	}
	if wholeBoard {
		// Deleting a dashboard is explicit consent to remove its widgets,
		// including locked widgets. Individual deletion respects the lock.
		for i := range nodes {
			if nodes[i].Kind == "widget" && nodes[i].Session == e.resourceSession() {
				nodes[i].Protected = false
			}
			if nodes[i].Kind == "cron" && nodes[i].Tracked && nodes[i].BoundSession == e.resourceSession() && !nodes[i].Protected {
				roots = append(roots, nodes[i].ID)
			}
		}
	}
	known := map[string]bool{}
	for _, n := range nodes {
		known[n.ID] = true
	}
	for _, id := range selected {
		if !validResourceID(id) || !known[id] {
			return resources.Plan{}, fmt.Errorf("unknown resource %q; refresh the inventory", id)
		}
	}
	p := resources.PlanRemoval(nodes, roots, selected)
	if wholeBoard {
		shown := map[string]bool{}
		for _, n := range p.Remove {
			shown[n.ID] = true
		}
		for _, n := range p.Keep {
			shown[n.ID] = true
		}
		for _, n := range nodes {
			if !shown[n.ID] && n.Kind != "widget" && (n.Session == e.resourceSession() || n.BoundSession == e.resourceSession()) {
				n.Reason = "Kept: creation or execution context alone does not establish exclusive usage. Review before cleanup."
				p.Keep = append(p.Keep, n)
			}
		}
	}
	// Redact other users' consumer names without changing the actual plan.
	if e.multiUser {
		owners := map[string]resources.Node{}
		for _, n := range nodes {
			owners[n.ID] = n
		}
		redact := func(n resources.Node) resources.Node {
			if n.Session != "" && !e.ownResourceSession(n.Session) {
				n.Session = ""
			}
			if n.BoundSession != "" && !e.ownResourceSession(n.BoundSession) {
				n.BoundSession = ""
			}
			n.UsedBy = append([]string(nil), n.UsedBy...)
			for i, id := range n.UsedBy {
				other := owners[id]
				if other.Kind == "gallery" || (other.Session != "" && !e.ownResourceSession(other.Session)) {
					n.UsedBy[i] = "shared consumer"
				}
			}
			return n
		}
		for i, n := range p.Keep {
			p.Keep[i] = redact(n)
		}
	}
	return p, nil
}

type ResourceResult struct {
	Plan    resources.Plan `json:"plan"`
	Removed []string       `json:"removed"`
	Errors  []string       `json:"errors"`
}

// CleanupResources is the common implementation for agent, UI and dashboard
// deletion. Every apply recomputes its plan. Failures stop the operation before
// dependent files/widgets disappear; successful removals are safe to retry.
func (e *ToolExecutor) CleanupResources(ctx context.Context, widget string, board bool, selected []string, dryRun bool) (ResourceResult, error) {
	unlock := resources.Lock(e.workspaceDir)
	defer unlock()
	return e.cleanupResourcesLocked(ctx, widget, board, selected, dryRun)
}

// DeleteDashboard keeps resource deletion and the session-store/file commit
// under one environment mutation lock. The server quiesces the agent first.
func (e *ToolExecutor) DeleteDashboard(ctx context.Context, selected []string, commit func() error) (ResourceResult, error) {
	unlock := resources.Lock(e.workspaceDir)
	defer unlock()
	r, err := e.cleanupResourcesLocked(ctx, "", true, selected, false)
	if err != nil {
		return r, err
	}
	return r, commit()
}

func (e *ToolExecutor) cleanupResourcesLocked(ctx context.Context, widget string, board bool, selected []string, dryRun bool) (ResourceResult, error) {
	p, err := e.resourcePlan(ctx, widget, board, selected)
	result := ResourceResult{Plan: p, Removed: []string{}, Errors: []string{}}
	if err != nil || dryRun {
		return result, err
	}
	// Check the entire plan before mutating: a denied file/tool operation
	// must not leave an earlier cron removal behind.
	for _, n := range p.Remove {
		if e.toolGuard != nil {
			tool, args := "widget", map[string]interface{}{"action": "remove", "id": n.Name}
			switch n.Kind {
			case "cron":
				tool, args = "cron", map[string]interface{}{"action": "remove", "name": n.Name}
			case "tool":
				t := e.customMgr.Get(n.Name)
				if t == nil {
					return result, fmt.Errorf("custom tool changed; refresh")
				}
				tool, args = "delete_file", map[string]interface{}{"path": "agent_tools/" + t.Filename}
			case "file":
				tool, args = "delete_file", map[string]interface{}{"path": n.Name}
			case "service":
				tool, args = "docker_manage", map[string]interface{}{"action": "stop", "name": n.Name}
			}
			if err := e.toolGuard(tool, args); err != nil {
				return result, err
			}
		}
	}
	changed := false
	defer func() {
		if !changed {
			return
		}
		if e.customMgr != nil {
			e.customMgr.Reload()
		}
		if e.onToolsReload != nil {
			e.onToolsReload()
		}
		if e.onFileChange != nil {
			e.onFileChange()
		}
	}()
	for _, n := range p.Remove {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		var err error
		switch n.Kind {
		case "cron":
			var msg string
			msg, err = e.cronRemoveUnlocked(ctx, n.Name)
			if err == nil && !strings.HasPrefix(msg, "Removed job ") {
				err = fmt.Errorf("%s", msg)
			}
		case "tool":
			t := e.customMgr.Get(n.Name)
			if t == nil || t.Protected {
				err = fmt.Errorf("custom tool changed or is protected")
			} else {
				err = e.removeManaged(filepath.Join(e.customMgr.Dir(), t.Filename))
			}
		case "service":
			err = e.docker.StopService(ctx, n.Name)
		case "file":
			err = workspace.Remove(e.workspaceDir, n.Name)
		case "widget":
			err = e.removeWidgetFiles(n.Name)
		}
		if err != nil && !os.IsNotExist(err) {
			result.Errors = append(result.Errors, n.ID+": "+err.Error())
			return result, fmt.Errorf("cleanup stopped at %s: %w", n.ID, err)
		}
		changed = true
		result.Removed = append(result.Removed, n.ID)
	}
	return result, nil
}

func resourceMutation(name string, args map[string]interface{}) bool {
	switch canonicalToolName(name) {
	case "write_file", "edit_file", "delete_file", "download_file", "exec_command", "register_tool", "docker_run", "docker_compose", "add_widget", "update_widget", "remove_widget":
		return true
	case "widget", "cron", "resources", "docker_manage":
		action, _ := args["action"].(string)
		return action != "list" && action != "ps" && action != "logs" && action != "list_shared"
	}
	return false
}

func (e *ToolExecutor) ResourceInventory(ctx context.Context) ([]resources.Node, error) {
	unlock := resources.Lock(e.workspaceDir)
	defer unlock()
	nodes, err := e.resourceSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	return e.visibleResources(nodes), nil
}

func (e *ToolExecutor) resourceTool(ctx context.Context, args map[string]interface{}) (string, error) {
	action, _ := args["action"].(string)
	widget, _ := args["widget"].(string)
	ids := []string{}
	if raw, present := args["ids"]; present {
		a, ok := raw.([]interface{})
		if !ok {
			return "", fmt.Errorf("ids must be an array of resource IDs")
		}
		for _, v := range a {
			s, ok := v.(string)
			if !ok {
				return "", fmt.Errorf("ids must contain strings")
			}
			ids = append(ids, s)
		}
	}
	var value any
	var err error
	switch action {
	case "list":
		var nodes []resources.Node
		nodes, err = e.ResourceInventory(ctx)
		if err == nil {
			// Settings and existing programmatic callers receive the full array.
			// Model calls get a scoped, bounded JSON page rather than the generic
			// text truncation, which loses IDs and encourages repeated inventories.
			if e.rawResults && len(args) == 1 {
				value = nodes
			} else {
				value, err = e.resourceListPage(nodes, args)
			}
		}
	case "link":
		err = e.BindWidgetResources(ctx, widget, ids)
		value = map[string]any{"widget": widget, "resources": ids}
	case "cleanup":
		dryRun := true
		if raw, present := args["dry_run"]; present {
			b, ok := raw.(bool)
			if !ok {
				return "", fmt.Errorf("dry_run must be a boolean")
			}
			dryRun = b
		}
		value, err = e.CleanupResources(ctx, widget, false, ids, dryRun)
	default:
		return "", fmt.Errorf("resources action must be list, link or cleanup")
	}
	if err != nil {
		b, _ := json.Marshal(value)
		return string(b), err
	}
	b, err := json.Marshal(value)
	return string(b), err
}

type resourceListItem struct {
	resources.Node
	ConsumerCount   int `json:"consumerCount"`
	DependencyCount int `json:"dependencyCount"`
	DedicatedCount  int `json:"dedicatedCount"`
}

type resourceListPage struct {
	Scope      string             `json:"scope"`
	Resources  []resourceListItem `json:"resources"`
	Total      int                `json:"total"`
	NextOffset *int               `json:"next_offset,omitempty"`
}

func (e *ToolExecutor) resourceListPage(nodes []resources.Node, args map[string]interface{}) (resourceListPage, error) {
	all := false
	if raw, present := args["all"]; present {
		var ok bool
		all, ok = raw.(bool)
		if !ok {
			return resourceListPage{}, fmt.Errorf("all must be a boolean")
		}
	}
	offset := 0
	if raw, present := args["offset"]; present {
		n, ok := raw.(float64)
		if !ok || n < 0 || n > 1e9 || math.Trunc(n) != n {
			return resourceListPage{}, fmt.Errorf("offset must be a non-negative whole number")
		}
		offset = int(n)
	}
	widget, _ := args["widget"].(string)
	scope := "workspace"
	if all && widget == "" {
		scope = "environment"
	} else {
		selected := map[string]bool{}
		byID := map[string]resources.Node{}
		var queue []string
		for _, node := range nodes {
			byID[node.ID] = node
			if widget != "" {
				if node.Kind == "widget" && node.Session == e.resourceSession() && node.Name == widget {
					queue = append(queue, node.ID)
				}
			} else if node.Session == e.resourceSession() || node.BoundSession == e.resourceSession() {
				queue = append(queue, node.ID)
			}
		}
		if widget != "" {
			scope = "widget"
			if len(queue) == 0 {
				return resourceListPage{}, fmt.Errorf("widget %q not found on this dashboard", widget)
			}
		}
		for len(queue) > 0 {
			id := queue[0]
			queue = queue[1:]
			if selected[id] {
				continue
			}
			selected[id] = true
			queue = append(queue, byID[id].Dependencies...)
		}
		filtered := []resources.Node{}
		for _, node := range nodes {
			if selected[node.ID] {
				filtered = append(filtered, node)
			}
		}
		nodes = filtered
	}
	page := resourceListPage{Scope: scope, Resources: []resourceListItem{}, Total: len(nodes)}
	for offset < len(nodes) {
		node := nodes[offset]
		item := resourceListItem{Node: node, ConsumerCount: len(node.UsedBy), DependencyCount: len(node.Dependencies), DedicatedCount: len(node.Dedicated)}
		// Counts remain authoritative; lists show representative references.
		if len(item.UsedBy) > 8 {
			item.UsedBy = item.UsedBy[:8]
		}
		if len(item.Dependencies) > 8 {
			item.Dependencies = item.Dependencies[:8]
		}
		if len(item.Dedicated) > 8 {
			item.Dedicated = item.Dedicated[:8]
		}
		b, _ := json.Marshal(item)
		if len(b) > 8000 {
			item.Name = ""
			item.UsedBy = []string{}
			item.Dependencies = nil
			item.Dedicated = nil
		}
		page.Resources = append(page.Resources, item)
		b, _ = json.Marshal(page)
		if len(page.Resources) > 1 && (len(b) > 16000 || len(page.Resources) > 40) {
			page.Resources = page.Resources[:len(page.Resources)-1]
			break
		}
		offset++
	}
	if offset < len(nodes) {
		page.NextOffset = &offset
	}
	return page, nil
}

func (e *ToolExecutor) trackWrittenResource(path, content string) error {
	id := "file:" + path
	if strings.HasPrefix(path, "agent_tools/") {
		name, _ := extractToolName(content)
		if name == "" {
			return nil
		}
		id = "tool:" + name
	}
	if !validResourceID(id) {
		return nil
	}
	f, err := workspace.Open(e.workspaceDir, path)
	if err == nil {
		f.Close()
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return e.recordResource(ctx, id, false)
}

func protectResourceProducers(nodes []resources.Node) []resources.Node {
	byID := map[string]resources.Node{}
	producers := map[string][]string{}
	for _, n := range nodes {
		byID[n.ID] = n
		if n.Kind == "cron" || n.Kind == "tool" {
			for _, dep := range n.Dependencies {
				producers[dep] = append(producers[dep], n.ID)
			}
		}
	}
	for i, n := range nodes {
		if n.Kind != "widget" && n.Kind != "gallery" {
			continue
		}
		seen := map[string]bool{}
		queue := append([]string(nil), n.Dependencies...)
		for len(queue) > 0 {
			ref := queue[0]
			queue = queue[1:]
			if seen[ref] {
				continue
			}
			seen[ref] = true
			if backend, ok := byID[ref]; ok && (backend.Kind == "tool" || backend.Kind == "cron") {
				queue = append(queue, backend.Dependencies...)
			}
			if strings.HasPrefix(ref, "file:") || strings.HasPrefix(ref, "tool:") {
				queue = append(queue, producers[ref]...)
			}
		}
		ids := []string{}
		for id := range seen {
			ids = append(ids, id)
		}
		nodes[i].Dependencies = uniqueResources(ids)
	}
	return nodes
}
