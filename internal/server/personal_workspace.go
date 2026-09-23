package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"prism/internal/docker"
)

// WorkspaceAccess is supplied by a trusted embedding host, never decoded from
// request fields. Lifetime must be cancelled on suspension or lease loss.
type WorkspaceAccess struct {
	Data     *PersonalData
	Owner    string
	Root     string
	Lifetime context.Context
	Execute  docker.WorkspaceExecution
}

// PersonalWorkspaceHandler is the first resource-bound route slice. Unported
// routes are absent, not delegated to a standalone/global Server. It starts no
// application, database, listener or background workers per owner.
func PersonalWorkspaceHandler(resolve func(*http.Request) (WorkspaceAccess, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			origin, err := url.Parse(r.Header.Get("Origin"))
			if r.Header.Get("Sec-Fetch-Site") == "cross-site" || (r.Header.Get("Origin") != "" && (err != nil || origin.Host != r.Host || (origin.Scheme != "http" && origin.Scheme != "https"))) {
				http.Error(w, "cross-origin request refused", 403)
				return
			}
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		requestCtx, requestCancel := context.WithCancel(r.Context())
		defer requestCancel()
		r = r.WithContext(requestCtx)
		if resolve == nil {
			http.Error(w, "workspace resolver unavailable", 503)
			return
		}
		a, err := resolve(r)
		if err != nil {
			http.Error(w, "workspace access refused", 403)
			return
		}
		if a.Owner == "" || !filepath.IsAbs(a.Root) || filepath.Clean(a.Root) == "/" || a.Lifetime == nil || a.Execute == nil {
			http.Error(w, "workspace unavailable", 503)
			return
		}
		if a.Lifetime.Err() != nil {
			http.Error(w, "workspace access revoked", 403)
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		stop := context.AfterFunc(a.Lifetime, cancel)
		defer stop()
		defer cancel()
		// This request-local receiver has only the two capabilities needed by
		// these handlers. No copied mutexes, global store or deployment credentials.
		s := &Server{cfg: Config{WorkspaceDir: a.Root, PluginDir: filepath.Join(a.Root, ".plugins")}, docker: docker.WithExecution(func(ctx context.Context, command string, input []byte, env map[string]string) (string, error) {
			if a.Lifetime.Err() != nil {
				return "", errors.New("workspace access revoked")
			}
			return a.Execute(ctx, command, input, env)
		})}
		if a.Data != nil {
			ms, release, ok := a.Data.acquire(a.Owner)
			if !ok {
				http.Error(w, "personal data unavailable", 403)
				return
			}
			defer release()
			s.memStore = ms
		}
		r = withUser(r.WithContext(ctx), serviceUser)
		if a.Data == nil && (strings.HasPrefix(r.URL.Path, "/api/sessions") || strings.HasPrefix(r.URL.Path, "/api/user/secrets") || r.URL.Path == "/api/notes" || r.URL.Path == "/api/tasks" || r.URL.Path == "/api/events") {
			http.NotFound(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/user/secrets/") {
			s.handleUserSecretByName(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/sessions/") {
			s.handleSessionByID(w, r)
			return
		}
		switch r.URL.Path {
		case "/api/sessions":
			s.handleSessions(w, r)
		case "/api/user/secrets":
			s.handleUserSecrets(w, r)
		case "/api/notes":
			s.handleNotes(w, r)
		case "/api/tasks":
			s.handleTasks(w, r)
		case "/api/events":
			s.handleEvents(w, r)
		case "/api/files":
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", 405)
				return
			}
			s.handleFiles(w, r)
		case "/api/file":
			s.handleFile(w, r)
		case "/api/exec":
			s.handleExec(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}
