package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"prism/internal/memory"
)

// Group widget/dashboard sharing, agent side. Whatever a human can do from the
// gallery, the agent can do too: browse what the group shared, add one to the
// board, and publish a widget to the team. Backed by the same store/endpoints
// the modal uses. Only meaningful in a multi-user group (sharingGroups empty
// otherwise).

// SetSharingContext gives the executor the acting user and the groups they can
// share within (from CallerContext).
func (e *ToolExecutor) SetSharingContext(userID int64, groups []memory.Membership) {
	e.actingUserID = userID
	e.sharingGroups = groups
}

func (e *ToolExecutor) groupIDs() []int64 {
	ids := make([]int64, 0, len(e.sharingGroups))
	for _, g := range e.sharingGroups {
		ids = append(ids, g.GroupID)
	}
	return ids
}

func (e *ToolExecutor) inSharingGroup(id int64) bool {
	for _, g := range e.sharingGroups {
		if g.GroupID == id {
			return true
		}
	}
	return false
}

func (e *ToolExecutor) sharedList(ctx context.Context, kind string) (string, error) {
	if e.memStore == nil || len(e.sharingGroups) == 0 {
		return "Sharing is only available inside a multi-user group — nothing to list here.", nil
	}
	items, err := e.memStore.SharedItemsForGroups(ctx, e.groupIDs(), kind)
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		return "No widgets or dashboards shared in your group yet.", nil
	}
	b, _ := json.MarshalIndent(items, "", "  ")
	return "Shared with your group — add one with `widget action=add_shared id=<id>`:\n" + string(b), nil
}

func (e *ToolExecutor) sharedAdd(ctx context.Context, idStr string) (string, error) {
	if e.headless {
		return "", fmt.Errorf("widgets are unavailable here: this conversation has no dashboard")
	}
	if e.memStore == nil {
		return "", fmt.Errorf("sharing is unavailable")
	}
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		return "", fmt.Errorf("id must be the numeric id from list_shared")
	}
	item, ok, err := e.memStore.SharedItemByID(ctx, id)
	if err != nil {
		return "", err
	}
	if !ok || !e.inSharingGroup(item.GroupID) {
		return "", fmt.Errorf("shared item %d is not available in your group", id)
	}
	var p memory.SharedPayload
	if json.Unmarshal(item.Payload, &p) != nil || len(p.Widgets) == 0 {
		return "", fmt.Errorf("that shared item has no widgets")
	}
	added := 0
	for i, wdg := range p.Widgets {
		wid := fmt.Sprintf("%s-shared%d-%d", slugify(wdg.Title), id, i)
		cols, height := wdg.Cols, wdg.Height
		if cols < 1 {
			cols = 1
		}
		if height <= 0 {
			height = 280
		}
		if e.writeSharedWidget(wid, wdg.Title, wdg.Content, cols, height) == nil {
			added++
		}
	}
	return fmt.Sprintf("Added %d widget(s) from %q to this dashboard.", added, item.Title), nil
}

func (e *ToolExecutor) writeSharedWidget(id, title, content string, cols, height int) error {
	if err := os.MkdirAll(e.pluginDir, 0755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(e.pluginDir, id+".html"), []byte(content), 0644); err != nil {
		return err
	}
	meta, _ := json.Marshal(pluginMeta{Title: title, Cols: cols, Height: height})
	os.WriteFile(filepath.Join(e.pluginDir, id+".meta.json"), meta, 0644)
	if e.onPluginAdd != nil {
		e.onPluginAdd(id, title, content, cols, height)
	}
	return nil
}

func (e *ToolExecutor) sharePublish(ctx context.Context, widgetID, kind, groupName string) (string, error) {
	if e.memStore == nil || len(e.sharingGroups) == 0 {
		return "", fmt.Errorf("sharing is only available inside a multi-user group")
	}
	grp, err := e.resolveShareGroup(groupName)
	if err != nil {
		return "", err
	}
	var widgets []memory.SharedWidget
	title := ""
	if kind == "dashboard" {
		widgets = e.readBoardWidgets("")
		title = "shared dashboard"
	} else {
		kind = "widget"
		if widgetID == "" {
			return "", fmt.Errorf("id (the widget to share) is required — get it from `widget action=list`")
		}
		widgets = e.readBoardWidgets(widgetID)
		if len(widgets) > 0 {
			title = widgets[0].Title
		}
	}
	if len(widgets) == 0 {
		return "", fmt.Errorf("nothing to share (widget not found on this board, or the board is empty)")
	}
	payload, _ := json.Marshal(memory.SharedPayload{Widgets: widgets})
	if _, err := e.memStore.ShareItem(ctx, memory.SharedItem{
		GroupID: grp.GroupID, Kind: kind, Title: title,
		OwnerID: e.actingUserID, OwnerName: "the agent", Payload: payload,
	}); err != nil {
		return "", err
	}
	where := grp.GroupName
	if where == "" {
		where = "your group"
	}
	return fmt.Sprintf("Shared %d widget(s) as %q with %s.", len(widgets), title, where), nil
}

func (e *ToolExecutor) resolveShareGroup(name string) (memory.Membership, error) {
	if name != "" {
		for _, g := range e.sharingGroups {
			if strings.EqualFold(g.GroupName, name) {
				return g, nil
			}
		}
		return memory.Membership{}, fmt.Errorf("you are not in a group named %q", name)
	}
	if len(e.sharingGroups) == 1 {
		return e.sharingGroups[0], nil
	}
	names := make([]string, len(e.sharingGroups))
	for i, g := range e.sharingGroups {
		names[i] = g.GroupName
	}
	return memory.Membership{}, fmt.Errorf("you are in several groups (%s) — say which with group=<name>", strings.Join(names, ", "))
}

// readBoardWidgets reads this board's widgets from the plugin dir: just the one
// with id==only, or all of them when only is empty.
func (e *ToolExecutor) readBoardWidgets(only string) []memory.SharedWidget {
	entries, err := os.ReadDir(e.pluginDir)
	if err != nil {
		return nil
	}
	var out []memory.SharedWidget
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".html") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".html")
		if only != "" && id != only {
			continue
		}
		content, err := os.ReadFile(filepath.Join(e.pluginDir, entry.Name()))
		if err != nil {
			continue
		}
		title, cols, height := id, 1, 280
		if b, err := os.ReadFile(filepath.Join(e.pluginDir, id+".meta.json")); err == nil {
			var m pluginMeta
			if json.Unmarshal(b, &m) == nil {
				if m.Title != "" {
					title = m.Title
				}
				if m.Cols > 0 {
					cols = m.Cols
				}
				if m.Height > 0 {
					height = m.Height
				}
			}
		}
		out = append(out, memory.SharedWidget{Title: title, Content: string(content), Cols: cols, Height: height})
	}
	return out
}
