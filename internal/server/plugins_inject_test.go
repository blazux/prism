package server

import (
	"strings"
	"testing"
)

// TestInjectWidgetThemeAddsHelper verifies every served widget gets the session
// and the shared prism-widget.js helper (prismTool/prismChat) injected, so a
// widget never has to hand-wire an endpoint or session.
func TestInjectWidgetThemeAddsHelper(t *testing.T) {
	out := injectWidgetTheme("<html><head></head><body>hi</body></html>", "u2-home")

	for _, want := range []string{
		`window.PRISM_SESSION="u2-home"`,
		`src="/prism-widget.js"`,
		`prism-theme-vars`, // theme still injected
	} {
		if !strings.Contains(out, want) {
			t.Errorf("injected widget missing %q:\n%s", want, out)
		}
	}
	// The injected head must land inside <head>, before the body.
	if strings.Index(out, "/prism-widget.js") > strings.Index(out, "<body>") {
		t.Errorf("helper injected after <body>, should be in <head>:\n%s", out)
	}
}

// TestInjectWidgetThemeEscapesSession guards against a session id with a quote
// breaking out of the injected string literal.
func TestInjectWidgetThemeEscapesSession(t *testing.T) {
	out := injectWidgetTheme("<head></head>", `a"; alert(1); //`)
	if strings.Contains(out, `PRISM_SESSION="a";`) {
		t.Fatalf("session was not safely escaped:\n%s", out)
	}
	if !strings.Contains(out, `\"`) {
		t.Fatalf("expected the quote to be escaped:\n%s", out)
	}
}

// TestInjectWidgetThemeNoHead falls back to prepending for a bare fragment.
func TestInjectWidgetThemeNoHead(t *testing.T) {
	out := injectWidgetTheme("<div>just a fragment</div>", "s")
	if !strings.Contains(out, "/prism-widget.js") || !strings.Contains(out, "just a fragment") {
		t.Errorf("fragment injection failed:\n%s", out)
	}
}
