package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestPrismHelp_NotWiredFallsBackToFiles(t *testing.T) {
	e := &ToolExecutor{}
	out, _ := e.prismHelp(context.Background(), "")
	if !strings.Contains(out, ".prism_help") {
		t.Errorf("unwired help must point at the bundled files: %q", out)
	}
}

func TestPrismHelp_IndexCarriesStatusAndTopicReturnsPage(t *testing.T) {
	e := &ToolExecutor{}
	e.SetHelp(func(_ context.Context, topic string) (string, error) {
		switch topic {
		case "":
			return "- overview — Overview\n- connect-telegram — Telegram\n", nil
		case "connect-telegram":
			return "# Telegram\nsteps…", nil
		}
		return "", fmt.Errorf("no help page named %q. Topics: overview, connect-telegram", topic)
	}, func(context.Context) string { return "- Email: connected" })

	idx, _ := e.prismHelp(context.Background(), "")
	for _, want := range []string{"connect-telegram", "What is configured right now", "Email: connected", "RAG is not available"} {
		if !strings.Contains(idx, want) {
			t.Errorf("index missing %q:\n%s", want, idx)
		}
	}
	page, _ := e.prismHelp(context.Background(), "connect-telegram")
	if page != "# Telegram\nsteps…" {
		t.Errorf("topic page = %q", page)
	}
	miss, _ := e.prismHelp(context.Background(), "nope")
	if !strings.Contains(miss, "Topics: overview") {
		t.Errorf("unknown topic must list the real topics: %q", miss)
	}
}

// The bundled docs collection lives outside tenant scoping: a scoped session
// must resolve it to its real, unprefixed name — and never write to it.
func TestHelpCollection_UnscopedAndReadOnly(t *testing.T) {
	e := &ToolExecutor{ragScope: "g5"}
	if got := e.resolveCollection(HelpCollection); got != HelpCollection {
		t.Errorf("resolveCollection(prism-help) = %q, want unscoped", got)
	}
	if got := e.resolveCollection("docs"); got != ScopeCollection("g5", "docs") {
		t.Errorf("ordinary collections must stay scoped: %q", got)
	}
	if got := e.resolveScope(HelpCollection); got != HelpCollectionScope {
		t.Errorf("resolveScope(prism-help) = %q", got)
	}
	if out, _ := e.ragIngest(context.Background(), HelpCollection, "x", "y", ""); out != helpReadOnlyMsg {
		t.Errorf("ingest into prism-help must be refused: %q", out)
	}
	if out, _ := e.ragDelete(context.Background(), HelpCollection, ""); out != helpReadOnlyMsg {
		t.Errorf("delete of prism-help must be refused: %q", out)
	}
}
