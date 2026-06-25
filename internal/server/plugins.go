package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type pluginEntry struct {
	id, title, content string
	cols, height       int
	locked             bool
	open               bool
	x, y, w, h         float64
}

// updatePluginMeta reads a widget's .meta.json (tolerating a missing/empty
// file), lets the caller mutate it as a generic map, and writes it back. Using
// a map instead of a typed struct means callers that only touch one field
// (lock, window state) never wipe fields they don't know about.
func updatePluginMeta(path string, mutate func(m map[string]any)) error {
	m := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &m)
	}
	mutate(m)
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0644)
}

func (s *Server) loadPlugins(dir string) []pluginEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var plugins []pluginEntry
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".html" {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".html")
		content, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		title := id
		cols := 1
		height := 280
		locked := false
		open := true
		var x, y, w, h float64
		if metaBytes, err := os.ReadFile(filepath.Join(dir, id+".meta.json")); err == nil {
			var m struct {
				Title  string  `json:"title"`
				Cols   int     `json:"cols"`
				Height int     `json:"height"`
				Locked bool    `json:"locked"`
				Open   *bool   `json:"open"`
				X      float64 `json:"x"`
				Y      float64 `json:"y"`
				W      float64 `json:"w"`
				H      float64 `json:"h"`
			}
			if json.Unmarshal(metaBytes, &m) == nil {
				if m.Title != "" {
					title = m.Title
				}
				if m.Cols > 0 {
					cols = m.Cols
				}
				if m.Height > 0 {
					height = m.Height
				}
				locked = m.Locked
				if m.Open != nil {
					open = *m.Open
				}
				x, y, w, h = m.X, m.Y, m.W, m.H
			}
		}
		plugins = append(plugins, pluginEntry{id: id, title: title, content: string(content), cols: cols, height: height, locked: locked, open: open, x: x, y: y, w: w, h: h})
	}
	return plugins
}

// widgetThemeHead is injected into every /plugins/*.html response so the
// headless preview (and any URL-loaded widget) looks the same as the themed
// dashboard srcdoc: the default theme tokens + the shared widget stylesheet.
// On the live dashboard the active theme is applied client-side on top of this.
const widgetThemeHead = `<style id="prism-theme-vars">:root{` +
	`--bg:#080809;--bg1:#0e0e10;--bg2:#141416;--bg3:#1a1a1e;--bg4:#212126;` +
	`--border:#232328;--border2:#2e2e35;--text:#e8e8f0;--text2:#9090a0;--text3:#55555f;` +
	`--accent:#6b8afd;--accent-dim:#2a3566;--green:#4dba87;--red:#e06c75;--yellow:#e5c07b;--orange:#d19a66;` +
	`--radius:8px;--mono:'Fira Code',ui-monospace,monospace}</style>` +
	`<link rel="stylesheet" href="/widget-base.css">`

// injectWidgetTheme inserts widgetThemeHead into a widget document, tolerating
// fragments without <head>/<html> (it falls back to prepending).
func injectWidgetTheme(html string) string {
	lower := strings.ToLower(html)
	if i := strings.Index(lower, "<head>"); i >= 0 {
		at := i + len("<head>")
		return html[:at] + widgetThemeHead + html[at:]
	}
	if i := strings.Index(lower, "<html"); i >= 0 {
		if j := strings.Index(lower[i:], ">"); j >= 0 {
			at := i + j + 1
			return html[:at] + "<head>" + widgetThemeHead + "</head>" + html[at:]
		}
	}
	return widgetThemeHead + html
}

// servePluginHTML serves /plugins/<session>/<id>.html with the widget theme
// injected; everything else (meta.json, assets) falls through to next.
func (s *Server) servePluginHTML(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".html") {
			full := filepath.Join(s.cfg.PluginDir, filepath.Clean("/"+r.URL.Path))
			if b, err := os.ReadFile(full); err == nil {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Write([]byte(injectWidgetTheme(string(b))))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ─── Helpers ──────────────────────────────────────────────────────────────────
