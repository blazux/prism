package server

// Prism's own documentation, bundled into the binary, made available to the
// agent so it can answer usage/capability/setup questions and walk users through
// connecting accounts. Two delivery paths for robustness:
//   - materialized to $WORKSPACE_DIR/.prism_help/ so the agent can read_file them
//     even when RAG is unavailable (no embed model, cold start);
//   - ingested into the "prism-help" RAG collection for semantic search when RAG
//     becomes ready (idempotent via a content hash).

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"prism/internal/agent"
	"prism/internal/memory"
	"sort"
	"strings"

	"prism/internal/rag"
)

const (
	helpEmbedDir    = "docs/help"
	helpCollection  = agent.HelpCollection
	helpWorkspaceNS = ".prism_help"
	helpHashKey     = "help_docs_hash"
	helpSession     = agent.HelpCollectionScope
)

// helpFn serves the bundled docs to the agent's prism_help tool.
func (s *Server) helpFn() agent.HelpFn {
	return func(ctx context.Context, topic string) (string, error) {
		docs, _ := s.loadHelpDocs()
		return renderHelp(docs, topic)
	}
}

// renderHelp: topic "" → the index; an exact page name (with or without .md)
// → that page; a unique partial match on name or title ("telegram") → that
// page; anything else → a teaching error carrying the real topic list.
func renderHelp(docs []helpDoc, topic string) (string, error) {
	if len(docs) == 0 {
		return "", fmt.Errorf("no bundled documentation in this build")
	}
	names := make([]string, 0, len(docs))
	for _, d := range docs {
		names = append(names, strings.TrimSuffix(d.name, ".md"))
	}
	topic = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(topic)), ".md")
	if topic == "" {
		var sb strings.Builder
		sb.WriteString("# Prism documentation — topics\nCall prism_help with a topic name for the full page.\n\n")
		for _, d := range docs {
			fmt.Fprintf(&sb, "- %s — %s\n", strings.TrimSuffix(d.name, ".md"), helpTitle(d.body))
		}
		return sb.String(), nil
	}
	var partial []helpDoc
	for _, d := range docs {
		n := strings.TrimSuffix(d.name, ".md")
		if n == topic {
			return d.body, nil
		}
		if strings.Contains(n, topic) || strings.Contains(strings.ToLower(helpTitle(d.body)), topic) {
			partial = append(partial, d)
		}
	}
	switch len(partial) {
	case 1:
		return partial[0].body, nil
	case 0:
		return "", fmt.Errorf("no help page named %q. Topics: %s", topic, strings.Join(names, ", "))
	}
	var pn []string
	for _, d := range partial {
		pn = append(pn, strings.TrimSuffix(d.name, ".md"))
	}
	return "", fmt.Errorf("topic %q matches several pages: %s — pick one", topic, strings.Join(pn, ", "))
}

// helpTitle is a page's first "# " heading, else its first non-empty line.
func helpTitle(body string) string {
	first := ""
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "# ") {
			return strings.TrimSpace(t[2:])
		}
		if first == "" {
			first = t
		}
	}
	if r := []rune(first); len(r) > 80 {
		first = string(r[:80]) + "…"
	}
	return first
}

// integrationsStatusFor reports, as plain facts, what a user has configured —
// the part of "help me with Prism" that no documentation can answer. Only
// states this code can actually determine are reported; each "not configured"
// line names where to fix it. u nil / id 0 = the single-owner deployment.
func (s *Server) integrationsStatusFor(u *memory.User) func(ctx context.Context) string {
	return func(ctx context.Context) string {
		ms := s.store()
		if ms == nil {
			return ""
		}
		us := ms
		if u != nil && u.ID > 0 {
			us = ms.ConfigScope(fmt.Sprintf("u%d", u.ID))
		}
		has := func(st *memory.Store, key string) bool {
			v, ok, _ := st.GetConfig(ctx, key)
			return ok && strings.TrimSpace(v) != ""
		}
		val := func(key string) string { v, _, _ := us.GetConfig(ctx, key); return strings.TrimSpace(v) }
		secret := func(st *memory.Store, name string) bool { v, ok, _ := st.GetSecret(ctx, name); return ok && v != "" }
		var sb strings.Builder
		row := func(what, state string) { fmt.Fprintf(&sb, "- %s: %s\n", what, state) }

		if has(us, "email_config") {
			row("Email", "connected (the email tool works; Settings → Email to change the account)")
		} else {
			row("Email", "not configured — Settings → Email, or ask me to set it up (email action=config)")
		}
		switch vault, p := val("notes_vault_path"), val("notes_provider"); {
		case vault != "":
			row("Notes", fmt.Sprintf("vault linked at %s (provider %s)", vault, p))
		case p != "":
			row("Notes", "provider "+p)
		default:
			row("Notes", "built-in notes — Settings → Notes to link an Obsidian/Logseq vault")
		}
		var cal []string
		if has(us, "caldav_config") {
			cal = append(cal, "CalDAV configured")
		}
		if has(us, "oauth_google_token") {
			cal = append(cal, "Google account connected")
		}
		if has(us, "oauth_microsoft_token") {
			cal = append(cal, "Microsoft account connected")
		}
		calState := "built-in calendar"
		if cp := val("calendar_provider"); cp != "" {
			calState = "provider " + cp
		}
		if len(cal) > 0 {
			calState += " (" + strings.Join(cal, ", ") + ")"
		}
		row("Calendar", calState+" — Settings → Calendar")
		if tp := val("tasks_provider"); tp != "" {
			row("Tasks", "provider "+tp)
		} else {
			row("Tasks", "built-in tasks — Settings → Calendar to use Todoist or a calendar provider's tasks")
		}
		if secret(us, tgTokenSecret) {
			row("Telegram", "bot connected (Settings → Channels → Telegram)")
		} else {
			row("Telegram", "not linked — Settings → Channels → Telegram")
		}
		if secret(ms, slackBotTokenSecret) {
			row("Slack", "deployment bot connected (configured by a global admin)")
		} else {
			row("Slack", "not configured — a global admin sets it up in Settings → Channels")
		}
		if u != nil && u.ID > 0 {
			groups, _ := ms.UserGroups(ctx, u.ID)
			if len(groups) == 0 {
				if s.cfg.MultiUser {
					row("Groups", "none — the group knowledge base and MCP servers need one; ask an admin to add you")
				}
			} else {
				var gs []string
				for _, g := range groups {
					line := fmt.Sprintf("%s (%s)", g.GroupName, g.Role)
					if secret(ms, webexKey(webexTokenSecret, g.GroupID)) {
						line += ", Webex bot connected"
					}
					gs = append(gs, line)
				}
				row("Groups", strings.Join(gs, "; ")+" — the group knowledge base, MCP servers and group secrets are curated by a group admin in the admin console")
			}
		}
		return sb.String()
	}
}

// groupStatusFor is integrationsStatusFor's counterpart for a group's shared
// agent (room chat, Webex space).
func (s *Server) groupStatusFor(groupID int64) func(ctx context.Context) string {
	return func(ctx context.Context) string {
		ms := s.store()
		if ms == nil {
			return ""
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "- This is a group's shared agent (group %d): its knowledge base, MCP servers and group secrets are managed by a group admin in the admin console (Shared agent / knowledge base / MCP servers / Group secrets panes)\n", groupID)
		if v, ok, _ := ms.GetSecret(ctx, webexKey(webexTokenSecret, groupID)); ok && v != "" {
			sb.WriteString("- Webex: bot connected for this group\n")
		} else {
			sb.WriteString("- Webex: no bot for this group — admin console → Shared agent → Webex integration\n")
		}
		return sb.String()
	}
}

// helpDoc is one bundled markdown file.
type helpDoc struct {
	name string // e.g. "overview.md"
	body string
}

func (s *Server) loadHelpDocs() ([]helpDoc, string) {
	var docs []helpDoc
	h := sha256.New()
	entries, err := fs.ReadDir(s.cfg.HelpFS, helpEmbedDir)
	if err != nil {
		return nil, ""
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // stable hash
	for _, name := range names {
		b, err := fs.ReadFile(s.cfg.HelpFS, helpEmbedDir+"/"+name)
		if err != nil {
			continue
		}
		docs = append(docs, helpDoc{name: name, body: string(b)})
		h.Write([]byte(name))
		h.Write(b)
	}
	return docs, fmt.Sprintf("%x", h.Sum(nil))
}

// materializeHelpDocs writes the bundled docs to the workspace so read_file works
// regardless of RAG. Cheap; called synchronously at startup.
func (s *Server) materializeHelpDocs(docs []helpDoc) {
	dir := filepath.Join(s.cfg.WorkspaceDir, helpWorkspaceNS)
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("[help] mkdir: %v", err)
		return
	}
	for _, d := range docs {
		if err := os.WriteFile(filepath.Join(dir, d.name), []byte(d.body), 0644); err != nil {
			log.Printf("[help] write %s: %v", d.name, err)
		}
	}
}

// ingestHelpDocs indexes the docs into the prism-help RAG collection if their
// content changed since last time. Safe to call once RAG is ready.
func (s *Server) ingestHelpDocs(ctx context.Context, docs []helpDoc, hash string) {
	if s.ragStore == nil || s.ragEmbedder == nil || len(docs) == 0 {
		return
	}
	if s.memStore != nil {
		if prev, ok, _ := s.memStore.GetConfig(ctx, helpHashKey); ok && prev == hash {
			return // already up to date
		}
	}
	if err := s.ragStore.EnsureCollection(ctx, helpCollection, helpSession); err != nil {
		log.Printf("[help] ensure collection: %v", err)
		return
	}
	for _, d := range docs {
		chunks := rag.SplitText(d.body)
		if len(chunks) == 0 {
			continue
		}
		pageNums := make([]int, len(chunks))
		embeddings, err := s.ragEmbedder.EmbedBatch(ctx, chunks)
		if err != nil {
			log.Printf("[help] embed %s: %v", d.name, err)
			return // try again next start
		}
		sum := sha256.Sum256([]byte(d.body))
		if err := s.ragStore.UpsertDocument(ctx, helpCollection, d.name, fmt.Sprintf("%x", sum), int64(len(d.body)), chunks, pageNums, embeddings); err != nil {
			log.Printf("[help] upsert %s: %v", d.name, err)
			return
		}
	}
	if s.memStore != nil {
		s.memStore.SetConfig(ctx, helpHashKey, hash)
	}
	log.Printf("[help] indexed %d doc(s) into %q", len(docs), helpCollection)
}
