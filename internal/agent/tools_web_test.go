package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func webSearchFixture(t *testing.T, response string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "test query" {
			t.Errorf("query = %q, want %q", got, "test query")
		}
		if got := r.URL.Query().Get("format"); got != "json" {
			t.Errorf("format = %q, want json", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func TestWebSearchZeroResultsWithEngineFailuresReportsIncompleteCoverage(t *testing.T) {
	e := &ToolExecutor{searxngURL: webSearchFixture(t, `{
		"results": [],
		"unresponsive_engines": [
			["brave", "too many requests"],
			["ask", "HTTP error"],
			["duckduckgo", "CAPTCHA"]
		]
	}`)}

	got, err := e.webSearch(context.Background(), "test query")
	if err != nil {
		t.Fatalf("webSearch returned an error: %v", err)
	}
	for _, want := range []string{
		"search coverage was incomplete",
		"brave (too many requests)",
		"ask (HTTP error)",
		"duckduckgo (CAPTCHA)",
		"does not mean web search is globally unavailable",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("result %q does not contain %q", got, want)
		}
	}
	for _, misleading := range []string{"every search engine", "Web search is unavailable"} {
		if strings.Contains(got, misleading) {
			t.Errorf("result still makes the unsupported claim %q: %q", misleading, got)
		}
	}
}

func TestWebSearchZeroResultsWithoutEngineFailuresReportsNoMatches(t *testing.T) {
	e := &ToolExecutor{searxngURL: webSearchFixture(t, `{"results": [], "unresponsive_engines": []}`)}

	got, err := e.webSearch(context.Background(), "test query")
	if err != nil {
		t.Fatalf("webSearch returned an error: %v", err)
	}
	if !strings.Contains(got, "web genuinely returned nothing") {
		t.Fatalf("unexpected result: %q", got)
	}
	if strings.Contains(got, "incomplete") {
		t.Fatalf("healthy zero-result search reported incomplete coverage: %q", got)
	}
}

func TestWebSearchReturnsResultsDespiteOtherEngineFailures(t *testing.T) {
	e := &ToolExecutor{searxngURL: webSearchFixture(t, `{
		"results": [{"title": "Example", "url": "https://example.com", "content": "Found it"}],
		"unresponsive_engines": [["brave", "too many requests"]]
	}`)}

	got, err := e.webSearch(context.Background(), "test query")
	if err != nil {
		t.Fatalf("webSearch returned an error: %v", err)
	}
	if !strings.Contains(got, "Example") || !strings.Contains(got, "https://example.com") {
		t.Fatalf("search result missing from output: %q", got)
	}
	if strings.Contains(got, "unavailable") || strings.Contains(got, "incomplete") {
		t.Fatalf("successful search was reported as degraded: %q", got)
	}
}
