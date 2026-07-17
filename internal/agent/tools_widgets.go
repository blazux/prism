package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type pluginMeta struct {
	Title  string  `json:"title"`
	Cols   int     `json:"cols"`
	Height int     `json:"height"`
	Locked bool    `json:"locked,omitempty"`
	// Open is the window lifecycle flag. nil/absent means "open" (the default
	// for freshly created widgets). false means the user minimized the window
	// but kept the widget — it lives in the dock and can be reopened.
	Open *bool `json:"open,omitempty"`
	// Free-window geometry (pixels) persisted across reloads/devices.
	X float64 `json:"x,omitempty"`
	Y float64 `json:"y,omitempty"`
	W float64 `json:"w,omitempty"`
	H float64 `json:"h,omitempty"`
}

func (e *ToolExecutor) listUIPlugins() (string, error) {
	entries, err := os.ReadDir(e.pluginDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "No widgets on the dashboard.", nil
		}
		return "", err
	}
	type row struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Cols   int    `json:"cols"`
		Height int    `json:"height"`
		Locked bool   `json:"locked,omitempty"`
		Open   bool   `json:"open"`
	}
	var widgets []row
	for _, entry := range entries {
		fname := entry.Name()
		if !strings.HasSuffix(fname, ".meta.json") {
			continue
		}
		id := strings.TrimSuffix(fname, ".meta.json")
		b, err := os.ReadFile(filepath.Join(e.pluginDir, fname))
		if err != nil {
			continue
		}
		var m pluginMeta
		if err := json.Unmarshal(b, &m); err != nil {
			continue
		}
		open := m.Open == nil || *m.Open
		widgets = append(widgets, row{ID: id, Title: m.Title, Cols: m.Cols, Height: m.Height, Locked: m.Locked, Open: open})
	}
	if len(widgets) == 0 {
		return "No widgets on the dashboard.", nil
	}
	out, _ := json.MarshalIndent(widgets, "", "  ")
	return string(out), nil
}

// previewWidget renders the saved widget in the headless browser and returns a
// report (console errors) plus the screenshot as base64 images, so the vision
// model can verify its own rendering before telling the user the widget works.
func (e *ToolExecutor) previewWidget(ctx context.Context, id string) (string, []string) {
	sessionID := e.sessionID
	if sessionID == "" {
		sessionID = "default"
	}
	// Same in-network URL the system prompt documents for widget previews. Pass
	// the Prism service token so the preview browser authenticates (multi-user
	// Prism now gates /plugins behind login) instead of screenshotting /login.
	widgetURL := fmt.Sprintf("http://prism-server:8080/plugins/%s/%s.html", sessionID, id)
	res, err := e.browserActAuth(ctx, widgetURL, []interface{}{
		map[string]interface{}{"type": "wait_ms", "ms": 1500},
		map[string]interface{}{"type": "screenshot"},
	}, e.prismToken)
	if err != nil || strings.HasPrefix(res, "browser_act failed") {
		return "(auto-preview unavailable — verify the widget manually with browser_act)", nil
	}
	consoleErrs := extractConsoleIssues(res)

	// Vision bridge: a text-only chat model can't see the screenshot. Have the
	// vision captioner (e.g. AcidBurn) describe it as text instead, and never
	// attach the image (it would only confuse a blind model into flailing).
	if e.chatBlind {
		if e.ragCaptioner != nil {
			for _, p := range extractScreenshotPaths(res, e.workspaceDir) {
				if d, err := e.ragCaptioner.DescribeWidget(ctx, p); err == nil && strings.TrimSpace(d) != "" {
					report := "Auto-preview (you have no vision — a vision model inspected the rendered widget for you):\n" + strings.TrimSpace(d) +
						"\n\nIf that description shows broken layout, missing images/icons, blank areas or errors, fix the widget before telling the user it is ready."
					if consoleErrs != "" {
						report += "\nConsole issues:\n" + consoleErrs
					}
					return report, nil
				}
			}
		}
		report := "Auto-preview: a screenshot was captured but you have no vision to inspect it. Verify the widget via the console issues below and by reviewing your HTML/JS — do not attempt to open the screenshot file."
		if consoleErrs != "" {
			report += "\nConsole issues:\n" + consoleErrs
		}
		return report, nil
	}

	images := extractScreenshotImages(res, e.workspaceDir)
	report := "Auto-preview: screenshot of the rendered widget attached. Inspect it — broken layout, missing icons/images or console errors mean the widget is NOT done; fix it before telling the user it is ready."
	if consoleErrs != "" {
		report += "\nConsole issues:\n" + consoleErrs
	}
	return report, images
}

// extractConsoleIssues pulls error/warning console messages out of a
// browser_act JSON result.
func extractConsoleIssues(result string) string {
	var entries []struct {
		Action   string `json:"action"`
		Messages []struct {
			Level string `json:"level"`
			Text  string `json:"text"`
			Count int    `json:"count"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(result), &entries); err != nil {
		return ""
	}
	var sb strings.Builder
	n := 0
	for _, entry := range entries {
		if entry.Action != "console" {
			continue
		}
		for _, m := range entry.Messages {
			if m.Level != "error" && m.Level != "warning" {
				continue
			}
			if n >= 5 {
				sb.WriteString("- …\n")
				return sb.String()
			}
			count := ""
			if m.Count > 1 {
				count = fmt.Sprintf(" (×%d)", m.Count)
			}
			fmt.Fprintf(&sb, "- [%s]%s %s\n", m.Level, count, m.Text)
			n++
		}
	}
	return sb.String()
}

func (e *ToolExecutor) addUIPlugin(ctx context.Context, id, title, content string, cols, height int) (string, []string, error) {
	// Headless conversations (group room, Webex, Telegram, cron) have no
	// dashboard: a widget written here would silently go nowhere. Refuse with a
	// clear explanation so the model answers in text instead of claiming success.
	if e.headless {
		return "", nil, fmt.Errorf("widgets are unavailable here: this conversation has no dashboard (group room / messaging channel). Present the information as a text or markdown reply instead")
	}
	if id == "" || title == "" || content == "" {
		return "", nil, fmt.Errorf("id, title and content are required")
	}

	id = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, id)

	pluginPath := filepath.Join(e.pluginDir, id+".html")
	if err := os.WriteFile(pluginPath, []byte(content), 0644); err != nil {
		return "", nil, fmt.Errorf("write plugin: %w", err)
	}

	meta, _ := json.Marshal(pluginMeta{Title: title, Cols: cols, Height: height})
	os.WriteFile(filepath.Join(e.pluginDir, id+".meta.json"), meta, 0644)

	if e.onPluginAdd != nil {
		e.onPluginAdd(id, title, content, cols, height)
	}
	msg := fmt.Sprintf("Widget '%s' added to dashboard (cols=%d, height=%dpx)", title, cols, height)
	report, images := e.previewWidget(ctx, id)
	return msg + "\n" + report, images, nil
}

func (e *ToolExecutor) updateUIPlugin(ctx context.Context, id, title, content string, cols, height int) (string, []string, error) {
	if e.headless {
		return "", nil, fmt.Errorf("widgets are unavailable here: this conversation has no dashboard (group room / messaging channel). Present the information as a text or markdown reply instead")
	}
	if id == "" {
		return "", nil, fmt.Errorf("id is required")
	}
	metaPath := filepath.Join(e.pluginDir, id+".meta.json")
	htmlPath := filepath.Join(e.pluginDir, id+".html")

	b, err := os.ReadFile(metaPath)
	if err != nil {
		return "", nil, fmt.Errorf("widget '%s' not found", id)
	}
	var m pluginMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return "", nil, fmt.Errorf("corrupt widget meta: %w", err)
	}
	if m.Locked {
		return "", nil, fmt.Errorf("widget '%s' is locked by the user and cannot be updated", id)
	}

	if title != "" {
		m.Title = title
	}
	if cols >= 1 && cols <= 3 {
		m.Cols = cols
	}
	if height > 0 {
		m.Height = height
	}

	meta, _ := json.Marshal(m)
	if err := os.WriteFile(metaPath, meta, 0644); err != nil {
		return "", nil, fmt.Errorf("write meta: %w", err)
	}

	if content != "" {
		if err := os.WriteFile(htmlPath, []byte(content), 0644); err != nil {
			return "", nil, fmt.Errorf("write content: %w", err)
		}
	} else {
		existing, _ := os.ReadFile(htmlPath)
		content = string(existing)
	}

	if e.onPluginAdd != nil {
		e.onPluginAdd(id, m.Title, content, m.Cols, m.Height)
	}
	msg := fmt.Sprintf("Widget '%s' updated", id)
	report, images := e.previewWidget(ctx, id)
	return msg + "\n" + report, images, nil
}

func (e *ToolExecutor) removeUIPlugin(id string) (string, error) {
	metaPath := filepath.Join(e.pluginDir, id+".meta.json")
	htmlPath := filepath.Join(e.pluginDir, id+".html")

	_, metaErr := os.Stat(metaPath)
	_, htmlErr := os.Stat(htmlPath)
	if os.IsNotExist(metaErr) && os.IsNotExist(htmlErr) {
		return "", fmt.Errorf("widget '%s' does not exist", id)
	}

	if b, err := os.ReadFile(metaPath); err == nil {
		var m pluginMeta
		if json.Unmarshal(b, &m) == nil && m.Locked {
			return "", fmt.Errorf("widget '%s' is locked by the user and cannot be removed", id)
		}
	}
	os.Remove(htmlPath)
	os.Remove(metaPath)

	if e.onPluginRem != nil {
		e.onPluginRem(id)
	}
	return fmt.Sprintf("Plugin '%s' removed", id), nil
}
