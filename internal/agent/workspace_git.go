package agent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// ─── Workspace version control ────────────────────────────────────────────────
//
// The shared /workspace is a git repo: every agent turn's file changes are
// captured as one commit, giving a transparent history the agent (or a user) can
// inspect (workspace_history) and roll back (workspace_restore) when a file gets
// clobbered. It is a SAFETY NET, never a critical path — any git failure is
// logged and swallowed so a turn is never broken by it.
//
// git is absent from the prism-server image but present in the workspace
// container, and that is where the files physically live, so every git command
// runs there through e.docker (the same exec path as exec_command), rooted at
// /workspace. Commits happen once per turn, fire-and-forget from a goroutine.

// workspaceGitMu serializes git operations: /workspace has multiple concurrent
// writers (server-side write_file/delete plus exec_command / tool runs in the
// container), so overlapping `git add`/`commit` would race the index.
var workspaceGitMu sync.Mutex

// Files that must never enter history: transient (screenshots, logs, browser
// session state), regenerable caches (multi-MB blobs refreshed periodically —
// committing each refresh would bloat the repo for nothing), generated (pycache),
// or secret (.env, keys). Written only when the repo has no .gitignore yet, so a
// hand-tuned one is always respected.
const workspaceGitignore = `# Auto-managed by Prism. Transient / cache / generated / secret files stay out of history.
.screenshots/
logs/
*.log
*.pid
.browser_session_*
data/cache/
*-cache.json
tmp_*
*.tmp
__pycache__/
*.pyc
.env
.env.*
*.key
*.pem
*.p12
`

func (e *ToolExecutor) gitAvailable(ctx context.Context) bool {
	if e.docker == nil {
		return false
	}
	out, err := e.docker.Exec(ctx, "command -v git >/dev/null 2>&1 && echo yes || echo no", 15*time.Second)
	return err == nil && strings.TrimSpace(out) == "yes"
}

func (e *ToolExecutor) isWorkspaceRepo(ctx context.Context) bool {
	if e.docker == nil {
		return false
	}
	out, err := e.docker.Exec(ctx, "test -d /workspace/.git && echo yes || echo no", 10*time.Second)
	return err == nil && strings.TrimSpace(out) == "yes"
}

// git runs one git command at the workspace root. The -c flags give commits an
// identity without touching any global config, and let git operate on a repo it
// may see as owned by another uid (root-owned volume served by a non-root process).
func (e *ToolExecutor) git(ctx context.Context, args string, timeout time.Duration) (string, error) {
	if e.docker == nil {
		return "", fmt.Errorf("no workspace container")
	}
	cmd := "cd /workspace && git -c safe.directory=/workspace -c user.name=Prism -c user.email=prism@local " + args
	return e.docker.Exec(ctx, cmd, timeout)
}

// initWorkspaceRepo makes /workspace a repo: ignore file, init, baseline commit.
// The caller holds workspaceGitMu. Best-effort; returns false if git is unusable.
func (e *ToolExecutor) initWorkspaceRepo(ctx context.Context) bool {
	if !e.gitAvailable(ctx) {
		return false
	}
	// Write .gitignore only if none exists (respect a hand-tuned one).
	ignore := "cd /workspace && [ -f .gitignore ] || cat > .gitignore <<'PRISM_GITIGNORE_EOF'\n" + workspaceGitignore + "PRISM_GITIGNORE_EOF"
	if _, err := e.docker.Exec(ctx, ignore, 15*time.Second); err != nil {
		log.Printf("[workspace-git] .gitignore write failed, versioning disabled: %v", err)
		return false
	}
	if _, err := e.git(ctx, "init -q", 30*time.Second); err != nil {
		log.Printf("[workspace-git] init failed, versioning disabled: %v", err)
		return false
	}
	if _, err := e.git(ctx, "add -A", 120*time.Second); err != nil {
		log.Printf("[workspace-git] baseline add failed: %v", err)
		return false
	}
	// --allow-empty so a fully-ignored workspace still gets a root commit to build on.
	e.git(ctx, "commit -q --allow-empty -m 'baseline: existing workspace'", 60*time.Second)
	log.Printf("[workspace-git] /workspace is now version-controlled")
	return true
}

// CommitWorkspace snapshots the workspace after an agent turn. It lazily
// initializes the repo on first use, is a no-op when nothing changed, and is
// NEVER fatal — call it fire-and-forget in a goroutine with a background context.
func (e *ToolExecutor) CommitWorkspace(ctx context.Context, turnMsg string) {
	if e == nil || e.docker == nil {
		return
	}
	workspaceGitMu.Lock()
	defer workspaceGitMu.Unlock()

	if !e.isWorkspaceRepo(ctx) {
		if !e.initWorkspaceRepo(ctx) {
			return // git unavailable / init failed — versioning silently off
		}
	}
	if _, err := e.git(ctx, "add -A", 120*time.Second); err != nil {
		log.Printf("[workspace-git] add failed: %v", err)
		return
	}
	// `commit` exits non-zero when there is nothing to commit — the common case, so
	// that error is expected and ignored; a real failure just means no snapshot.
	e.git(ctx, "commit -q -m "+shellEscape(commitMessage(turnMsg)), 60*time.Second)
}

// commitMessage turns a raw user message into a one-line, rune-safe, bounded
// commit subject (the model emits multilingual text, so byte-slicing could split
// a rune).
func commitMessage(userMsg string) string {
	s := strings.TrimSpace(userMsg)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	const max = 72
	if r := []rune(s); len(r) > max {
		s = strings.TrimSpace(string(r[:max])) + "…"
	}
	if s == "" {
		return "agent turn"
	}
	return "turn: " + s
}

// workspaceHistory lists recent workspace commits (read-only).
func (e *ToolExecutor) workspaceHistory(ctx context.Context, limit int) (string, error) {
	if !e.isWorkspaceRepo(ctx) {
		return "Workspace history isn't available yet — versioning starts on the first change.", nil
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	out, err := e.git(ctx, fmt.Sprintf("log --pretty=format:'%%h  %%cr  %%s' -n %d", limit), 15*time.Second)
	if err != nil {
		return "", fmt.Errorf("read history: %w", err)
	}
	if out = strings.TrimSpace(out); out == "" {
		return "No workspace history yet.", nil
	}
	return "Recent workspace changes (newest first). Roll a file back with workspace_restore + a hash:\n" + out, nil
}

// workspaceRestore restores one path from a past commit into the current
// workspace and commits that restoration (itself reversible — nothing is lost).
func (e *ToolExecutor) workspaceRestore(ctx context.Context, commit, path string) (string, error) {
	commit = strings.TrimSpace(commit)
	path = strings.TrimSpace(NormalizeWorkspacePath(path))
	if commit == "" || path == "" {
		return "", fmt.Errorf("both 'commit' (a hash from workspace_history) and 'path' are required")
	}
	if !e.isWorkspaceRepo(ctx) {
		return "", fmt.Errorf("workspace versioning isn't initialized yet")
	}
	workspaceGitMu.Lock()
	defer workspaceGitMu.Unlock()
	if _, err := e.git(ctx, fmt.Sprintf("checkout %s -- %s", shellEscape(commit), shellEscape(path)), 30*time.Second); err != nil {
		return "", fmt.Errorf("couldn't restore %q from %s — check the hash/path with workspace_history: %w", path, commit, err)
	}
	e.git(ctx, "add -A", 60*time.Second)
	e.git(ctx, "commit -q -m "+shellEscape(fmt.Sprintf("restore %s from %s", path, commit)), 30*time.Second)
	return fmt.Sprintf("Restored %s from commit %s. The state you're replacing is still in history if you need it back.", path, commit), nil
}
