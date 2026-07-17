package agent

import "testing"

// The browser is the only tool that can *render* a page rather than read its
// markup, which is what a chat-attached HTML export needs. Opening it means
// allowing file://, so the boundary of what file:// may reach is pinned here.
func TestValidateBrowserURL(t *testing.T) {
	allowed := []string{
		"http://example.com/x",
		"https://example.com/x",
		"file:///workspace/uploads/call.html",
		"file:///workspace/uploads/sub dir/a b.html",
		"file:///workspace/x/../y.html", // cleans to /workspace/y.html
		"file:///workspace",             // the workspace root: a directory listing, harmless
	}
	for _, u := range allowed {
		if err := validateBrowserURL(u); err != nil {
			t.Errorf("validateBrowserURL(%q) = %v, want nil", u, err)
		}
	}

	rejected := []string{
		"file:///etc/passwd",
		"file:///workspace/../etc/passwd", // traversal out of the workspace
		"file:///workspacer/secrets",      // prefix must stop at a path separator
		"file://evil.com/workspace/a",     // host component
		"ftp://example.com/x",
		"javascript:alert(1)",
		"",
	}
	for _, u := range rejected {
		if err := validateBrowserURL(u); err == nil {
			t.Errorf("validateBrowserURL(%q) = nil, want error", u)
		}
	}
}
