package hosting

import (
	"context"
	"io/fs"
	"net"
	"net/http"
	"strings"

	agenttools "prism/agent_tools"
	help "prism/docs/help"
	"prism/internal/docker"
	"prism/internal/server"
	"prism/web"
)

// Application owns a Prism server; its internals are deliberately not exported.
type Application struct{ server *server.Server }

// New constructs an application without listening or starting background work.
func New(cfg Config) (*Application, error) {
	backend := docker.Backend{Mode: cfg.DockerBackend.Mode, Socket: cfg.DockerBackend.Socket, User: cfg.DockerBackend.User}
	if err := backend.Validate(); err != nil {
		return nil, err
	}
	s := server.New(server.Config{
		DockerBackend:    backend,
		Port:             cfg.Port,
		WorkspaceDir:     cfg.WorkspaceDir,
		SecretKeyPath:    cfg.SecretKeyPath,
		PluginDir:        cfg.PluginDir,
		OllamaURL:        cfg.OllamaURL,
		Model:            cfg.Model,
		LLMBackend:       cfg.LLMBackend,
		OpenAIBaseURL:    cfg.OpenAIBaseURL,
		OpenAIAPIKey:     cfg.OpenAIAPIKey,
		AnthropicToken:   cfg.AnthropicToken,
		AnthropicBaseURL: cfg.AnthropicBaseURL,
		AnthropicModel:   cfg.AnthropicModel,
		EmbedBackend:     cfg.EmbedBackend,
		VisionModel:      cfg.VisionModel,
		ChatVision:       cfg.ChatVision,
		AgentContainer:   cfg.AgentContainer,
		SearxngURL:       cfg.SearxngURL,
		ServicePortStart: cfg.ServicePortStart,
		ServicePortEnd:   cfg.ServicePortEnd,
		PostgresURL:      cfg.PostgresURL,
		EmbedModel:       cfg.EmbedModel,
		AuthToken:        cfg.AuthToken,
		MultiUser:        cfg.MultiUser,
		VoxURL:           cfg.VoxURL,
		VoxUser:          cfg.VoxUser,
		VoxPassword:      cfg.VoxPassword,
		WebFS:            prefixedFS{"web", web.Files()},
		HelpFS:           prefixedFS{"docs/help", help.Files()},
		ToolsFS:          prefixedFS{"agent_tools", agenttools.Files()},
	})
	return &Application{server: s}, nil
}

// Start preserves the standalone blocking entry point.
func (a *Application) Start() error { return a.server.Start() }

// Initialize starts stores and workers once without opening a listener.
// Database readiness is asynchronous, as in the standalone application.
func (a *Application) Initialize(ctx context.Context) error { return a.server.Initialize(ctx) }

// Handler builds authenticated product routes without starting resources.
func (a *Application) Handler() (http.Handler, error) { return a.server.Handler() }

// Shutdown rejects new requests, cancels work and drains it before closing stores.
// A deadline error means the drain is incomplete. The application cannot restart.
func (a *Application) Shutdown(ctx context.Context) error { return a.server.Shutdown(ctx) }

// Serve initializes the application and owns the listener until ctx is cancelled.
func (a *Application) Serve(ctx context.Context, listener net.Listener) error {
	return a.server.Serve(ctx, listener)
}

// Preserve the existing internal asset paths without copying embedded bytes.
type prefixedFS struct {
	prefix string
	source fs.FS
}

func (f prefixedFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if name == f.prefix {
		return f.source.Open(".")
	}
	if strings.HasPrefix(name, f.prefix+"/") {
		return f.source.Open(strings.TrimPrefix(name, f.prefix+"/"))
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}
