package server

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"prism/internal/docker"
	"prism/internal/memory"
	"prism/internal/workspace"
)

type resourceContextKey struct{}

// PersonalEnvironment is the state of one personal workspace inside a shared
// HTTP application: connections, integrations, model state and live clients.
// It owns no listener, HTTP router, deployment or independent application.
type PersonalEnvironment struct{ state *Server }

// OpenPersonalEnvironment attaches already-provisioned resources. All product
// handlers, tools and prompts remain the same implementations as standalone.
func OpenPersonalEnvironment(ctx context.Context, cfg Config, key []byte, execute docker.WorkspaceExecution) (*PersonalEnvironment, error) {
	if cfg.MultiUser || cfg.WorkspaceDir == "" || !filepath.IsAbs(cfg.WorkspaceDir) || filepath.Clean(cfg.WorkspaceDir) == "/" || cfg.PostgresURL == "" || len(key) != 32 || execute == nil || len(cfg.AuthToken) < 32 {
		return nil, errors.New("incomplete personal resource configuration")
	}
	cfg.PluginDir = filepath.Join(cfg.WorkspaceDir, "plugins")
	s := New(cfg)
	s.docker = docker.WithExecution(execute).WithWorkspaceServices(cfg.WorkspaceDir).WithServiceURL(cfg.ServiceURL)
	ms, err := memory.NewStore(ctx, cfg.PostgresURL, append([]byte(nil), key...), false)
	if err != nil {
		return nil, err
	}
	s.memStore = ms
	s.mcpMgr.SetStore(ms)
	for _, dir := range []string{"plugins", "data", ".screenshots"} {
		if err = workspace.MkdirAll(cfg.WorkspaceDir, dir, 0700); err != nil {
			ms.Close()
			return nil, err
		}
	}
	docs, _ := s.loadHelpDocs()
	s.materializeHelpDocs(docs)
	s.materializeAgentTools()
	s.life.started = true
	lifetime := s.runtimeContext()
	stop := context.AfterFunc(ctx, func() { s.life.cancel() })
	context.AfterFunc(lifetime, func() { stop() })
	s.startEmailRules()
	s.startChannels()
	s.background(func() { s.initRAG(lifetime) })
	return &PersonalEnvironment{state: s}, nil
}
func (e *PersonalEnvironment) Close(ctx context.Context) error { return e.state.Shutdown(ctx) }

// SharedPersonalHandler builds one router. The private host authenticates each
// entry and selects resources; no missing identity can reach local resources.
func SharedPersonalHandler(cfg Config, resolve func(*http.Request) (*PersonalEnvironment, error)) (http.Handler, error) {
	if resolve == nil {
		return nil, errors.New("personal resolver required")
	}
	s := New(cfg)
	s.hostedResolver = func(r *http.Request) (*Server, error) {
		e, err := resolve(r)
		if err != nil || e == nil || e.state == nil {
			return nil, errors.New("personal environment unavailable")
		}
		return e.state, nil
	}
	return s.Handler()
}

func (s *Server) resourceRoute(fn func(*Server, http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.hostedResolver != nil {
			bound, _ := r.Context().Value(resourceContextKey{}).(*Server)
			if bound == nil {
				http.Error(w, "personal resources unavailable", 503)
				return
			}
			fn(bound, w, r)
			return
		}
		fn(s, w, r)
	}
}
func (s *Server) hostedAuth(next http.Handler, w http.ResponseWriter, r *http.Request) {
	// Operator logs and enterprise administration are not personal resources.
	if strings.HasPrefix(r.URL.Path, "/api/admin/") || r.URL.Path == "/api/terminal" || r.URL.Path == "/admin" || strings.HasPrefix(r.URL.Path, "/api/voice") || strings.HasPrefix(r.URL.Path, "/api/vox/") {
		http.Error(w, "not available in personal hosting", 404)
		return
	}
	bound, err := s.hostedResolver(r)
	if err != nil || bound == nil {
		http.Error(w, "personal access refused", 403)
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), resourceContextKey{}, bound))
	// Preserve capability validation and session scoping for agent self-calls.
	// The resolver authenticates cookies itself and attaches the internal bearer
	// only after owner resolution. Signed capabilities are left intact.
	bound.lifecycleHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { bound.singleUserAuth(next, w, r) })).ServeHTTP(w, r)
}

func (e *PersonalEnvironment) VerifyCapability(token string) bool {
	_, _, ok := e.state.verifyCapToken(token)
	return ok
}
