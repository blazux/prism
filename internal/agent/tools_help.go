package agent

import (
	"context"
	"fmt"
	"strings"

	"prism/internal/rag"
)

// ─── prism_help: Prism's own documentation + what is configured ───────────────
//
// The agent is the product's onboarding/support layer, so it needs two things a
// search over the docs cannot give it: a discoverable, scope-independent way to
// reach the bundled documentation, and FACTS about what this caller has
// configured (email, calendar, channels, groups…) — otherwise "is my calendar
// connected?" can only be answered by trying a tool and reading its error.

// HelpFn serves the bundled documentation: topic "" → an index of topics,
// otherwise one page. Provided by the server, where the docs are embedded.
type HelpFn func(ctx context.Context, topic string) (string, error)

const (
	// HelpCollection is the RAG collection the bundled docs are indexed into. It
	// lives OUTSIDE tenant scoping (one copy per deployment, under
	// HelpCollectionScope) and is read-only for the agent — resolveCollection
	// special-cases it the way agent-learnings is special-cased, else a scoped
	// session would look for "<scope>--prism-help" and conclude there is no
	// documentation at all.
	HelpCollection      = "prism-help"
	HelpCollectionScope = "default"
)

const helpReadOnlyMsg = "prism-help is Prism's bundled documentation — read-only, refreshed automatically on upgrade. Search it with rag_search or prism_help; put your own material in another collection."

// SetHelp wires the bundled documentation and the caller's integration status
// into the prism_help tool. Either may be nil (the tool then says so).
func (e *ToolExecutor) SetHelp(help HelpFn, status func(ctx context.Context) string) {
	e.helpFn, e.integrationsStatusFn = help, status
}

func (e *ToolExecutor) prismHelp(ctx context.Context, topic string) (string, error) {
	if e.helpFn == nil {
		return "Prism's documentation isn't wired into this context. Read the bundled pages instead: list_files .prism_help, then read_file .prism_help/<topic>.md.", nil
	}
	body, err := e.helpFn(ctx, topic)
	if err != nil {
		return err.Error(), nil // a teaching error: the unknown topic + the real list
	}
	if strings.TrimSpace(topic) != "" {
		return body, nil
	}
	var sb strings.Builder
	sb.WriteString(body)
	sb.WriteString("\n## What is configured right now\n")
	if e.integrationsStatusFn != nil {
		if st := strings.TrimSpace(e.integrationsStatusFn(ctx)); st != "" {
			sb.WriteString(st)
			sb.WriteString("\n")
		}
	}
	// Knowledge base and MCP servers: the executor sees those itself.
	switch {
	case e.ragStore == nil:
		sb.WriteString("- Knowledge base: RAG is not available on this deployment\n")
	case e.ragBlocked():
		sb.WriteString("- Knowledge base: " + ragBlockedMsg + "\n")
	default:
		if cols, cerr := e.ragStore.ListCollections(ctx, e.ragScope); cerr == nil {
			if len(cols) == 0 {
				sb.WriteString("- Knowledge base: no collections yet (rag_ingest creates one; Settings → Knowledge to upload documents)\n")
			} else {
				var names []string
				for _, c := range cols {
					names = append(names, fmt.Sprintf("%s (%d docs)", e.uncol(c.Name), c.DocCount))
				}
				fmt.Fprintf(&sb, "- Knowledge base: %d collection(s): %s\n", len(cols), strings.Join(names, ", "))
			}
		}
	}
	if e.mcpMgr != nil {
		if list, lerr := e.mcpListServers(ctx); lerr == nil {
			sb.WriteString("- MCP servers: " + strings.ReplaceAll(strings.TrimSpace(list), "\n", "\n  ") + "\n")
		}
	}
	return sb.String(), nil
}

// helpCollectionInfo returns the bundled-docs collection if it has been indexed.
func (e *ToolExecutor) helpCollectionInfo(ctx context.Context) *rag.Collection {
	if e.ragStore == nil {
		return nil
	}
	cols, err := e.ragStore.ListCollections(ctx, HelpCollectionScope)
	if err != nil {
		return nil
	}
	for i := range cols {
		if cols[i].Name == HelpCollection {
			return &cols[i]
		}
	}
	return nil
}
