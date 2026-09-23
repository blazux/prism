// Package hosting is the composition boundary for applications embedding Prism.
// It is experimental; consumers must pin an exact Prism revision.
// Hosted tenant resolution is not implemented by this package yet.
package hosting

// DockerBackend selects service execution; the workspace must be provisioned
// separately. An empty Mode retains the local host backend.
type DockerBackend struct{ Mode, Socket, User string }

// Config contains deployment inputs, not internal server state.
// MultiUser means the existing enterprise mode, never public Cloud tenancy.
type Config struct {
	DockerBackend DockerBackend
	Port          string
	WorkspaceDir  string
	SecretKeyPath string
	PluginDir     string
	OllamaURL     string
	Model         string
	LLMBackend    string // "ollama" (default), "openai" (SGLang/vLLM/…) or "anthropic" (Claude, API key)
	OpenAIBaseURL string // /v1 root, used when LLMBackend == "openai"
	OpenAIAPIKey  string // optional bearer token for the openai backend
	// AnthropicToken is the console API key for the Claude backend. A Pro/Max
	// subscription token is not an option — see internal/anthropic/credential.go.
	AnthropicToken   string
	AnthropicBaseURL string // API root; empty = https://api.anthropic.com
	// AnthropicModel is the Claude model to use, held separately from Model so
	// Claude can sit in the picker alongside a local default rather than only as
	// the primary backend.
	AnthropicModel   string
	EmbedBackend     string // "" (follow LLMBackend), "ollama" or "openai": backend for RAG embeddings + captioning
	VisionModel      string // optional override model for RAG vision captioning (needed when captioning ≠ chat backend)
	ChatVision       bool   // chat model can see images (default true); false → caption widget previews as text for a text-only model
	AgentContainer   string
	SearxngURL       string
	ServicePortStart int
	ServicePortEnd   int
	PostgresURL      string
	EmbedModel       string
	AuthToken        string
	// MultiUser turns Prism from a personal dashboard into a shared one: accounts,
	// groups, rooms, per-user scoping and a login page. Off by default — at home
	// there is one user, and AuthToken is the whole of authentication. It is read
	// in exactly one place, withAuth; everything downstream reads the identity that
	// puts in the request, and the service identity already means "global".
	MultiUser bool
	// VoxURL, when set, means this Prism is docked with a Prism Vox telephony
	// stack: the Téléphonie app appears, and /api/vox/* proxies Vox's API (call
	// logs, outbound calls, SIP, directory). Empty = standalone Prism, no
	// telephony surface.
	// VoxUser/VoxPassword are Vox's HTTP Basic credentials, used by that proxy.
	VoxURL      string
	VoxUser     string
	VoxPassword string
}
