package server

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The bundled help is what the agent uses to guide users, so it must keep up
// with the UI: every Settings tab and every admin-console pane has to be
// mentioned in at least one docs/help page. The corpus once drifted 76 commits
// behind the product because nothing failed when a feature shipped
// undocumented — this does.
func TestHelpDocsCoverSettingsTabsAndAdminPanes(t *testing.T) {
	docs := strings.ToLower(readAllHelpDocs(t))

	settingsLabels := map[string]string{
		"agent": "Agent", "appearance": "Appearance", "calendar": "Calendar", "channels": "Channels",
		"email": "Email", "knowledge": "Knowledge", "mcp": "MCP", "notes": "Notes", "profile": "Profile",
		"secrets": "Secrets", "tools": "Tools", "webhooks": "Webhooks",
	}
	settings, err := os.ReadFile(filepath.Join("..", "..", "web", "settings.html"))
	if err != nil {
		t.Fatalf("read settings.html: %v", err)
	}
	for _, id := range uniqueMatches(`data-tab="([a-z_-]+)"`, string(settings)) {
		label, known := settingsLabels[id]
		if !known {
			t.Errorf("Settings tab %q is new: add it to this test's label map and document it in docs/help/", id)
			continue
		}
		if !strings.Contains(docs, strings.ToLower("Settings → "+label)) {
			t.Errorf("Settings → %s (tab %q) is documented nowhere in docs/help/*.md", label, id)
		}
	}

	adminLabels := map[string]string{
		"users": "Users", "groups": "Groups", "tools": "Global tool policy", "usage": "Usage", "logs": "Logs",
		"platform": "Apps", "telephony": "Telephony", "agent": "Shared agent", "rag": "knowledge base",
		"mcp": "MCP servers", "secrets": "Group secrets", "access": "Group tool access",
	}
	admin, err := os.ReadFile("admin_console_page.go")
	if err != nil {
		t.Fatalf("read admin console page: %v", err)
	}
	for _, id := range uniqueMatches(`data-pane="([a-z_-]+)"`, string(admin)) {
		label, known := adminLabels[id]
		if !known {
			t.Errorf("admin pane %q is new: add it to this test's label map and document it in docs/help/", id)
			continue
		}
		if !strings.Contains(docs, strings.ToLower(label)) {
			t.Errorf("admin pane %q (%s) is documented nowhere in docs/help/*.md", id, label)
		}
	}
}

func TestRenderHelp_IndexTopicAndMiss(t *testing.T) {
	docs := []helpDoc{
		{name: "overview.md", body: "# Prism overview\n\nWhat it is."},
		{name: "connect-telegram.md", body: "# Connect Telegram\n\n1. BotFather"},
		{name: "connect-slack.md", body: "# Connect Slack\n\nAdmins only."},
	}
	idx, err := renderHelp(docs, "")
	if err != nil || !strings.Contains(idx, "connect-telegram — Connect Telegram") {
		t.Errorf("index: %v %q", err, idx)
	}
	if page, _ := renderHelp(docs, "connect-telegram.md"); !strings.HasPrefix(page, "# Connect Telegram") {
		t.Errorf("exact topic (with .md): %q", page)
	}
	if page, _ := renderHelp(docs, "telegram"); !strings.HasPrefix(page, "# Connect Telegram") {
		t.Errorf("unique partial match: %q", page)
	}
	if _, err := renderHelp(docs, "connect"); err == nil || !strings.Contains(err.Error(), "several") {
		t.Errorf("ambiguous partial match must ask to pick: %v", err)
	}
	if _, err := renderHelp(docs, "nope"); err == nil || !strings.Contains(err.Error(), "overview") {
		t.Errorf("miss must list topics: %v", err)
	}
}

func readAllHelpDocs(t *testing.T) string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join("..", "..", "docs", "help", "*.md"))
	if len(files) == 0 {
		t.Fatal("no docs/help/*.md found")
	}
	var sb strings.Builder
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		sb.Write(b)
		sb.WriteString("\n")
	}
	return sb.String()
}

func uniqueMatches(pattern, s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range regexp.MustCompile(pattern).FindAllStringSubmatch(s, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}
