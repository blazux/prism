package server

import (
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Bundled agent tools, seeded like the help docs: embedded in the binary, written
// into $WORKSPACE_DIR/agent_tools/ on boot so every deployment ships them, not
// just the box where someone hand-copied a script.
//
// Only the embedded files are written; anything else the user created in that
// directory is left untouched. A tool that needs OS packages ships a sibling
// `.apt-packages` line here — it is merged into /workspace/.apt-packages, which
// the workspace container re-applies at boot.
const toolsEmbedDir = "agent_tools"

func (s *Server) materializeAgentTools() {
	dir := filepath.Join(s.cfg.WorkspaceDir, "agent_tools")
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("[tools] mkdir: %v", err)
		return
	}

	entries, err := fs.ReadDir(s.cfg.ToolsFS, toolsEmbedDir)
	if err != nil {
		return // nothing embedded — fine
	}

	var aptPkgs []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		b, err := fs.ReadFile(s.cfg.ToolsFS, toolsEmbedDir+"/"+name)
		if err != nil {
			continue
		}
		if name == ".apt-packages" {
			aptPkgs = splitLines(string(b))
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0644); err != nil {
			log.Printf("[tools] write %s: %v", name, err)
		}
	}

	if len(aptPkgs) > 0 {
		s.ensureAptPackages(aptPkgs)
	}
}

// ensureAptPackages adds any missing package to /workspace/.apt-packages without
// dropping what is already there (the user may have added their own). The
// workspace container installs the list at its next start; on this box tshark is
// already present, so the merge is a no-op there.
func (s *Server) ensureAptPackages(pkgs []string) {
	path := filepath.Join(s.cfg.WorkspaceDir, ".apt-packages")
	existing := map[string]bool{}
	var lines []string
	if b, err := os.ReadFile(path); err == nil {
		for _, l := range splitLines(string(b)) {
			existing[l] = true
			lines = append(lines, l)
		}
	}
	changed := false
	for _, p := range pkgs {
		if !existing[p] {
			lines = append(lines, p)
			existing[p] = true
			changed = true
		}
	}
	if !changed {
		return
	}
	sort.Strings(lines)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		log.Printf("[tools] write .apt-packages: %v", err)
		return
	}
	log.Printf("[tools] .apt-packages updated; workspace installs them on next restart")
}

func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out
}
