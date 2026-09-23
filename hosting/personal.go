package hosting

import (
	"context"
	"errors"
	"net"
	"net/http"

	agenttools "prism/agent_tools"
	help "prism/docs/help"
	"prism/internal/docker"
	"prism/internal/server"
	"prism/web"
)

// PersonalConfig contains resolved resources only. It deliberately cannot carry
// deployment model credentials, enterprise configuration or a host Docker socket.
type PersonalConfig struct {
	ServiceURL                                                                          func(int) string
	DialWorkspace                                                                       func(context.Context, int) (net.Conn, error)
	WidgetFrameURL, WidgetGrantURL                                                      string
	WorkspaceDir, PostgresURL, CapabilityKey, CapabilityPrefix, AgentAddress, SearchURL string
	EncryptionKey                                                                       []byte
	Execute                                                                             func(context.Context, string, []byte, map[string]string) (string, error)
}
type PersonalEnvironment struct{ environment *server.PersonalEnvironment }

func personalAssets() server.Config {
	return server.Config{WebFS: prefixedFS{"web", web.Files()}, HelpFS: prefixedFS{"docs/help", help.Files()}, ToolsFS: prefixedFS{"agent_tools", agenttools.Files()}}
}
func OpenPersonalEnvironment(ctx context.Context, cfg PersonalConfig) (*PersonalEnvironment, error) {
	c := personalAssets()
	c.DisableAIServerDefaults = true
	c.WorkspaceDial = cfg.DialWorkspace
	c.ServiceURL = cfg.ServiceURL
	c.WorkspaceDir, c.PostgresURL, c.AuthToken = cfg.WorkspaceDir, cfg.PostgresURL, cfg.CapabilityKey
	c.CapabilityPrefix = cfg.CapabilityPrefix
	c.WidgetFrameURL, c.WidgetGrantURL = cfg.WidgetFrameURL, cfg.WidgetGrantURL
	c.AgentContainer, c.SearxngURL = cfg.AgentAddress, cfg.SearchURL
	c.LLMBackend, c.OpenAIBaseURL, c.ChatVision = "openai", "https://api.openai.com/v1", true
	e, err := server.OpenPersonalEnvironment(ctx, c, cfg.EncryptionKey, docker.WorkspaceExecution(cfg.Execute))
	if err != nil {
		return nil, err
	}
	return &PersonalEnvironment{environment: e}, nil
}
func (e *PersonalEnvironment) Close(ctx context.Context) error { return e.environment.Close(ctx) }
func (e *PersonalEnvironment) VerifyCapability(token string) bool {
	return e.environment.VerifyCapability(token)
}

// SharedPersonalHandler creates one product router. Resolve authenticates every
// request and returns the owner's resources; errors have no standalone fallback.
// The host presents CapabilityKey as an internal bearer for authenticated browser
// calls, or preserves the verified inner capability for scoped agent self-calls.
func SharedPersonalHandler(resolve func(*http.Request) (*PersonalEnvironment, error)) (http.Handler, error) {
	if resolve == nil {
		return nil, errors.New("personal resolver required")
	}
	return server.SharedPersonalHandler(personalAssets(), func(r *http.Request) (*server.PersonalEnvironment, error) {
		e, err := resolve(r)
		if e == nil {
			return nil, err
		}
		return e.environment, err
	})
}
