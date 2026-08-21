package agent

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestExecutor creates a ToolExecutor with a real temp workspace directory.
// No Docker connection is needed for file-system tests.
func newTestExecutor(t *testing.T) (*ToolExecutor, string) {
	t.Helper()
	dir := t.TempDir()
	e := &ToolExecutor{workspaceDir: dir}
	return e, dir
}

// ─── writeFile / readFile path-traversal protection ───────────────────────────

func TestWriteFile_ValidPath(t *testing.T) {
	e, dir := newTestExecutor(t)

	msg, err := e.writeFile("hello.txt", "content")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(msg, "hello.txt") {
		t.Errorf("expected path in success message, got %q", msg)
	}
	data, err := os.ReadFile(filepath.Join(dir, "hello.txt"))
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if string(data) != "content" {
		t.Errorf("want content %q, got %q", "content", string(data))
	}
}

func TestWriteFile_SubdirPath(t *testing.T) {
	e, dir := newTestExecutor(t)

	_, err := e.writeFile("subdir/file.txt", "data")
	if err != nil {
		t.Fatalf("subdirectory path: unexpected error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "subdir", "file.txt")); err != nil {
		t.Errorf("expected file to exist: %v", err)
	}
}

func TestWriteFile_ParentDotDot_Blocked(t *testing.T) {
	e, _ := newTestExecutor(t)

	_, err := e.writeFile("../escape.txt", "content")
	if err == nil {
		t.Error("path '../escape.txt' should have been rejected")
	}
}

func TestWriteFile_DeepDotDot_Blocked(t *testing.T) {
	e, _ := newTestExecutor(t)

	// filepath.Clean("subdir/../../escape.txt") == "../escape.txt" → must be blocked
	_, err := e.writeFile("subdir/../../escape.txt", "content")
	if err == nil {
		t.Error("path 'subdir/../../escape.txt' should have been rejected (resolves to ../escape.txt)")
	}
}

func TestWriteFile_AbsolutePath_StaysInWorkspace(t *testing.T) {
	// In Go, filepath.Join(base, "/absolute") = base+"/absolute",
	// so an absolute path input cannot escape the workspace directory.
	e, dir := newTestExecutor(t)

	_, err := e.writeFile("/etc/passwd", "evil")
	// The function may or may not error, but it must NOT write outside the workspace.
	if err == nil {
		// If no error, the file must be inside the workspace.
		escapedPath := "/etc/passwd"
		if _, statErr := os.Stat(escapedPath); statErr == nil {
			// Check if the workspace-owned version was created instead.
			workspacePath := filepath.Join(dir, "etc", "passwd")
			if _, wErr := os.Stat(workspacePath); wErr != nil {
				t.Errorf("absolute path /etc/passwd wrote outside workspace (real /etc/passwd exists and workspace file does not)")
			}
		}
		// If /etc/passwd does not exist on this system, the write is to workspace — fine.
	}
}

func TestReadFile_ValidPath(t *testing.T) {
	e, dir := newTestExecutor(t)

	path := filepath.Join(dir, "read_me.txt")
	if err := os.WriteFile(path, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	content, err := e.readFile("read_me.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != "hello" {
		t.Errorf("want %q, got %q", "hello", content)
	}
}

func TestReadFile_ParentDotDot_Blocked(t *testing.T) {
	e, _ := newTestExecutor(t)

	_, err := e.readFile("../anything")
	if err == nil {
		t.Error("'../anything' should have been rejected")
	}
}

func TestReadFile_Truncation(t *testing.T) {
	e, dir := newTestExecutor(t)

	// Write a file larger than the 10000-char truncation limit.
	big := strings.Repeat("a", 15000)
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(big), 0644); err != nil {
		t.Fatal(err)
	}
	content, err := e.readFile("big.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) > 10100 {
		t.Errorf("truncation not applied: got %d chars", len(content))
	}
	if !strings.Contains(content, "truncated") {
		t.Errorf("expected truncation notice in output")
	}
}

// ─── listFiles ────────────────────────────────────────────────────────────────

func TestListFiles_EmptyDir(t *testing.T) {
	e, _ := newTestExecutor(t)

	result, err := e.listFiles(".")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "(empty directory)" {
		t.Errorf("want '(empty directory)', got %q", result)
	}
}

func TestListFiles_ShowsFiles(t *testing.T) {
	e, dir := newTestExecutor(t)

	if err := os.WriteFile(filepath.Join(dir, "foo.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "mydir"), 0755); err != nil {
		t.Fatal(err)
	}

	result, err := e.listFiles(".")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "foo.txt") {
		t.Errorf("file 'foo.txt' not listed in %q", result)
	}
	if !strings.Contains(result, "mydir/") {
		t.Errorf("directory 'mydir/' not listed in %q", result)
	}
}

// TestListFiles_NilInfoPanic documents a potential panic: if entry.Info() returns
// an error (e.g. file deleted between ReadDir and Info), info is nil and
// info.Size() on line 535 of tools.go will panic.
// This test exercises the normal (non-racy) path to confirm no panic occurs there,
// but it cannot reliably reproduce the race condition in a unit test.
func TestListFiles_NoPanicOnNormalFiles(t *testing.T) {
	e, dir := newTestExecutor(t)

	for i := 0; i < 5; i++ {
		name := filepath.Join(dir, strings.Repeat("f", i+1)+".txt")
		if err := os.WriteFile(name, []byte("content"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Should not panic.
	result, err := e.listFiles(".")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty listing")
	}
}

func TestListFiles_DotDot_Blocked(t *testing.T) {
	e, _ := newTestExecutor(t)

	_, err := e.listFiles("../")
	if err == nil {
		t.Error("'../' should have been rejected")
	}
}

// ─── slugify / toolFilename — deriving a machine-safe id from free text ───────

func TestSlugify(t *testing.T) {
	cases := []struct{ in, want string }{
		{"say_hello", "say_hello"},
		{"Fetch RT Tickets", "fetch_rt_tickets"},
		{"Tickets DBS_PROD_VOIP", "tickets_dbs_prod_voip"},
		{"  leading and trailing spaces  ", "leading_and_trailing_spaces"},
		{"éàü unicode", "unicode"}, // non-ASCII collapses to '_', leading run trimmed
		{"already-a-slug-123", "already-a-slug-123"},
		{"", ""},
	}
	for _, c := range cases {
		if got := slugify(c.in); got != c.want {
			t.Errorf("slugify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestToolFilename(t *testing.T) {
	if got := toolFilename("say_hello"); got != "say_hello.py" {
		t.Errorf("toolFilename(%q) = %q, want %q", "say_hello", got, "say_hello.py")
	}
	if got := toolFilename("Fetch RT Tickets"); got != "fetch_rt_tickets.py" {
		t.Errorf("toolFilename(%q) = %q, want %q", "Fetch RT Tickets", got, "fetch_rt_tickets.py")
	}
}

// httpRequestError must rewrite the two errors that misled the model: a
// non-existent host (NXDOMAIN — the raw "...on 127.0.0.11:53: no such host"
// read as a DNS-server fault) and a timeout (a slow/blocking remote read as a
// network problem). Anything else stays a generic "request failed".
func TestHTTPRequestError(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "marees.shom.fr", IsNotFound: true}
	got := httpRequestError("marees.shom.fr", nx).Error()
	if !strings.Contains(got, "does not exist") || !strings.Contains(got, "web_search") || strings.Contains(got, "127.0.0.11") {
		t.Errorf("NXDOMAIN rewrite wrong: %q", got)
	}

	to := &net.DNSError{Err: "i/o timeout", Name: "www.meteo-france.fr", IsTimeout: true}
	got = httpRequestError("www.meteo-france.fr", to).Error()
	if !strings.Contains(got, "did not respond") || !strings.Contains(got, "not your network") {
		t.Errorf("timeout rewrite wrong: %q", got)
	}

	other := errors.New("connection reset by peer")
	if got = httpRequestError("x", other).Error(); !strings.Contains(got, "request failed") {
		t.Errorf("generic error should stay generic: %q", got)
	}
}
