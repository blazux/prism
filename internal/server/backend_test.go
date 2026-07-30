package server

import (
	"testing"

	"prism/internal/anthropic"
	"prism/internal/ollama"
	"prism/internal/openai"
)

// backendKind names the concrete client behind the interface, so a routing test
// can assert where a model was sent without issuing a request.
func backendKind(b ollama.Backend) string {
	switch b.(type) {
	case *anthropic.Client:
		return "anthropic"
	case *openai.Client:
		return "openai"
	case *ollama.Client:
		return "ollama"
	default:
		return "unknown"
	}
}

// fleetConfig is the everyday shape: an OpenAI-compatible gateway as the brain,
// Ollama alongside it. Claude joins by adding a key, without displacing either.
func fleetConfig() Config {
	return Config{
		LLMBackend:    "openai",
		OpenAIBaseURL: "http://gateway.invalid:4000/v1",
		OllamaURL:     "http://ollama.invalid:11434",
		Model:         "qwen3.6-35b-a3b",
	}
}

func TestChatBackendForRoutesClaudeWithoutDisplacingThePrimary(t *testing.T) {
	cfg := fleetConfig()
	cfg.AnthropicToken = "sk-ant-api03-test"
	cfg.AnthropicModel = "claude-sonnet-5"
	s := &Server{cfg: cfg}

	// The default (empty model) must stay on the fleet — adding a Claude key does
	// not change which brain answers an ordinary turn.
	if got := backendKind(s.chatBackendFor("")); got != "openai" {
		t.Errorf("the default turn should stay on the primary, went to %s", got)
	}

	// A Claude id routes to Anthropic. Without this it falls through to
	// localBackendFor, which sends anything the gateway does not list to Ollama —
	// a guaranteed 404 on a claude-* name.
	for _, model := range []string{"claude-sonnet-5", "claude-opus-5", "claude-haiku-4-5"} {
		if got := backendKind(s.chatBackendFor(model)); got != "anthropic" {
			t.Errorf("%s should route to anthropic, went to %s", model, got)
		}
	}
}

func TestChatBackendForIgnoresClaudeModelsWithoutAKey(t *testing.T) {
	// No key means Claude was never offered in the picker, so a claude-* id here
	// is stale (a saved session, a hand-typed name). It must not be handed to a
	// backend that would report an auth error for a model it never served.
	s := &Server{cfg: fleetConfig()}
	if got := backendKind(s.chatBackendFor("claude-sonnet-5")); got == "anthropic" {
		t.Error("with no API key configured, nothing should route to anthropic")
	}
}

func TestOtherChatBackendsSpansTheFleetOllamaAndClaude(t *testing.T) {
	cfg := fleetConfig()
	cfg.AnthropicToken = "sk-ant-api03-test"
	s := &Server{cfg: cfg}

	kinds := map[string]bool{}
	for _, b := range s.otherChatBackends() {
		kinds[backendKind(b)] = true
	}
	if !kinds["ollama"] {
		t.Error("Ollama's models should still be offered next to the primary")
	}
	if !kinds["anthropic"] {
		t.Error("a configured Claude key should put its models in the same picker")
	}

	// Without a key, Claude is simply absent — not an unreachable entry the model
	// list has to time out on.
	s = &Server{cfg: fleetConfig()}
	for _, b := range s.otherChatBackends() {
		if backendKind(b) == "anthropic" {
			t.Error("Claude should not appear when no key is configured")
		}
	}
}

func TestOtherChatBackendsOffersClaudeBesideAnOllamaDefault(t *testing.T) {
	// Same expectation with Ollama as the brain and no gateway wired at all.
	s := &Server{cfg: Config{
		LLMBackend:     "ollama",
		OllamaURL:      "http://ollama.invalid:11434",
		Model:          "AcidBurn:latest",
		AnthropicToken: "sk-ant-api03-test",
		AnthropicModel: "claude-sonnet-5",
	}}
	found := false
	for _, b := range s.otherChatBackends() {
		if backendKind(b) == "anthropic" {
			found = true
		}
	}
	if !found {
		t.Error("Claude should be offered alongside an Ollama default too")
	}
	if got := backendKind(s.chatBackendFor("claude-sonnet-5")); got != "anthropic" {
		t.Errorf("a Claude id should route to anthropic, went to %s", got)
	}
	if got := backendKind(s.chatBackendFor("")); got != "ollama" {
		t.Errorf("the default turn should stay on Ollama, went to %s", got)
	}
}

func TestAnthropicModelIsHeldSeparatelyFromThePrimaryModel(t *testing.T) {
	// Seeding Anthropic's fallback list with cfg.Model would offer a local model
	// name as though Anthropic served it, whenever /v1/models is unavailable.
	cfg := fleetConfig()
	cfg.AnthropicToken = "sk-ant-api03-test"
	cfg.AnthropicModel = "claude-sonnet-5"
	s := &Server{cfg: cfg}
	if got := s.anthropicModel(); got != "claude-sonnet-5" {
		t.Errorf("expected the configured Claude model, got %q", got)
	}

	// With Anthropic as the primary, ANTHROPIC_MODEL has already been promoted to
	// cfg.Model, so that is the right source.
	s = &Server{cfg: Config{LLMBackend: "anthropic", Model: "claude-opus-5"}}
	if got := s.anthropicModel(); got != "claude-opus-5" {
		t.Errorf("expected the promoted primary model, got %q", got)
	}

	// Never a local model name.
	s = &Server{cfg: fleetConfig()}
	if got := s.anthropicModel(); got != "" {
		t.Errorf("expected no Claude model when none is configured, got %q", got)
	}
}
