package server

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The widget runtime is a contract with the agent: the system prompt documents
// these helpers, and every widget the agent ever wrote calls them. A frontend
// refactor that drops or renames one breaks widgets already pinned on users'
// dashboards — silently, since nothing in Go references them. This pins the
// surface (see docs/UPGRADING.md, rule 4).
func TestWidgetRuntimeContract(t *testing.T) {
	widget := readWeb(t, "prism-widget.js")
	for _, helper := range []string{
		"prismTool", "prismToolRaw", "prismChat", "prismNotify",
		"prismSuggest", "prismContext", "prismOpenFile", "prismOnData",
	} {
		if !strings.Contains(widget, "window."+helper+" = ") {
			t.Errorf("prism-widget.js no longer defines window.%s", helper)
		}
	}
	// prismTool reaches the universal dispatcher with the widget's own session.
	if !strings.Contains(widget, "'/api/builtin/' + encodeURIComponent(name) + '?session='") {
		t.Error("prismTool must POST /api/builtin/<name>?session=<PRISM_SESSION>")
	}

	// composeWidgetDoc injects, in this order: theme vars, widget-base.css,
	// PRISM_SESSION, prism-widget.js. Widgets rely on all four being present.
	theme := readWeb(t, "theme.js")
	for _, needle := range []string{
		`<link rel="stylesheet" href="/widget-base.css">`,
		`window.PRISM_SESSION=`,
		`<script src="/prism-widget.js">`,
	} {
		if !strings.Contains(theme, needle) {
			t.Errorf("theme.js composeWidgetDoc no longer injects %s", needle)
		}
	}

	// The dashboard must keep handling every message type the helpers emit.
	app := readWeb(t, "app.js")
	for _, msgType := range []string{"openFile", "context", "suggest", "sendChat", "notify"} {
		re := regexp.MustCompile(`d\.type === '` + msgType + `'`)
		if !re.MatchString(app) {
			t.Errorf("app.js message handler no longer handles type %q", msgType)
		}
	}
	// …and keep telling app frames about data changes (prismOnData).
	if !strings.Contains(app, "type: 'data-changed'") {
		t.Error("app.js no longer posts 'data-changed' to frames")
	}
	// The widget sandbox keeps same-origin: the system prompt promises widgets
	// that relative /api and /data fetches are cookie-authenticated.
	if !regexp.MustCompile(`WIDGET_SANDBOX = '[^']*allow-same-origin`).MatchString(app) {
		t.Error("widget iframe sandbox dropped allow-same-origin — relative fetches from widgets would lose their cookie (see docs/UPGRADING.md)")
	}
}

func readWeb(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "web", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
