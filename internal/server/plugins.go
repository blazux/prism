package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type pluginEntry struct {
	id, title, content string
	cols, height       int
	locked             bool
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
		if metaBytes, err := os.ReadFile(filepath.Join(dir, id+".meta.json")); err == nil {
			var m struct {
				Title  string `json:"title"`
				Cols   int    `json:"cols"`
				Height int    `json:"height"`
				Locked bool   `json:"locked"`
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
			}
		}
		plugins = append(plugins, pluginEntry{id: id, title: title, content: string(content), cols: cols, height: height, locked: locked})
	}
	return plugins
}

// ─── Helpers ──────────────────────────────────────────────────────────────────
