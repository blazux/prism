package server

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"prism/internal/anthropic"
	"prism/internal/ollama"
	"prism/internal/openai"
	"prism/internal/rag"
)

// anthropicModelPrefix identifies a model served by Anthropic. Claude model ids
// all carry it, which is what lets the picker route a choice without asking each
// server what it holds.
const anthropicModelPrefix = "claude-"

// newChatBackend returns the chat backend selected by LLM_BACKEND. It defaults
// to Ollama; "openai" targets any OpenAI-compatible server (SGLang, vLLM, …) and
// "anthropic" targets Claude with a console API key.
func (s *Server) newChatBackend() ollama.Backend {
	if len(s.cfg.AISources) > 0 {
		return &sourcesBackend{sources: s.cfg.AISources, primary: s.cfg.AIDefaultSource}
	}
	switch s.cfg.LLMBackend {
	case "anthropic":
		return s.newAnthropicBackend()
	case "openai":
		return openai.NewClient(s.cfg.OpenAIBaseURL, s.cfg.OpenAIAPIKey)
	}
	return ollama.NewClient(s.cfg.OllamaURL)
}

// newAnthropicBackend builds a Claude client, seeded with the configured Claude
// model so the picker still offers it when Anthropic declines to enumerate.
func (s *Server) newAnthropicBackend() ollama.Backend {
	return anthropic.NewClient(s.cfg.AnthropicBaseURL, s.cfg.AnthropicToken, s.anthropicModel())
}

// anthropicConfigured reports whether Claude is reachable at all. It is a
// separate question from which backend is the default: with a key set, Claude
// belongs in the picker even when the fleet or Ollama is the everyday brain.
func (s *Server) anthropicConfigured() bool {
	return strings.TrimSpace(s.cfg.AnthropicToken) != ""
}

// anthropicModel is the Claude model to offer. ANTHROPIC_MODEL holds it whether
// or not Anthropic is the primary backend — when it is, it has already been
// promoted to cfg.Model, and seeding the fallback list with cfg.Model in any
// other case would offer a local model name as if Anthropic served it.
func (s *Server) anthropicModel() string {
	if s.cfg.AnthropicModel != "" {
		return s.cfg.AnthropicModel
	}
	if s.cfg.LLMBackend == "anthropic" {
		return s.cfg.Model
	}
	return ""
}

// dualBackend reports whether both an OpenAI-compatible server AND an Ollama
// server are wired, so the model picker can span both regardless of which one is
// the default — e.g. AcidBurn on Ollama (default) + DFlash `qwen` on vLLM.
func (s *Server) dualBackend() bool {
	return s.cfg.OpenAIBaseURL != "" && s.cfg.OllamaURL != ""
}

// otherChatBackends returns every configured chat backend that is not the
// primary, so one picker can span them all: the everyday local model, the
// heavyweight on the other server, and Claude, each one click away.
func (s *Server) otherChatBackends() []ollama.Backend {
	var others []ollama.Backend
	switch s.cfg.LLMBackend {
	case "anthropic":
		if s.cfg.OpenAIBaseURL != "" {
			others = append(others, openai.NewClient(s.cfg.OpenAIBaseURL, s.cfg.OpenAIAPIKey))
		}
		if s.cfg.OllamaURL != "" {
			others = append(others, ollama.NewClient(s.cfg.OllamaURL))
		}
	case "openai":
		if s.dualBackend() {
			others = append(others, ollama.NewClient(s.cfg.OllamaURL))
		}
		if s.anthropicConfigured() {
			others = append(others, s.newAnthropicBackend())
		}
	default:
		if s.dualBackend() {
			others = append(others, openai.NewClient(s.cfg.OpenAIBaseURL, s.cfg.OpenAIAPIKey))
		}
		if s.anthropicConfigured() {
			others = append(others, s.newAnthropicBackend())
		}
	}
	return others
}

// chatModels returns the union of selectable chat models: the primary backend's
// list merged with every other configured server's, so one picker offers the
// full menu. A server that can't be reached is skipped rather than fatal — only
// all configured backends failing is an error.
func (s *Server) chatModels(ctx context.Context) ([]string, error) {
	if len(s.cfg.AISources) > 0 {
		return s.newChatBackend().ListModels(ctx)
	}
	backends := append([]ollama.Backend{s.newChatBackend()}, s.otherChatBackends()...)
	type answer struct {
		models []string
		err    error
	}
	pending := make([]chan answer, len(backends))
	for i, backend := range backends {
		pending[i] = make(chan answer, 1)
		go func(b ollama.Backend, ch chan answer) { models, err := b.ListModels(ctx); ch <- answer{models, err} }(backend, pending[i])
	}
	models := []string{}
	seen := map[string]bool{}
	var failures []error
	for _, ch := range pending {
		result := <-ch
		if result.err != nil {
			failures = append(failures, result.err)
			log.Printf("[models] backend unavailable: %v", result.err)
			continue
		}
		for _, m := range result.models {
			if !seen[m] {
				seen[m] = true
				models = append(models, m)
			}
		}
	}
	if len(failures) == len(backends) {
		return nil, errors.Join(failures...)
	}
	return models, nil
}

// chatBackendFor returns the backend that serves the given model, letting a
// single picker route each choice to the right server. An empty model means "the
// default", which is the configured primary.
func (s *Server) chatBackendFor(model string) ollama.Backend {
	if len(s.cfg.AISources) > 0 {
		return s.newChatBackend()
	}
	if s.cfg.LLMBackend == "anthropic" {
		if model == "" || strings.HasPrefix(model, anthropicModelPrefix) || (s.cfg.OllamaURL == "" && s.cfg.OpenAIBaseURL == "") {
			return s.newAnthropicBackend()
		}
		return s.localBackendFor(model) // a local model chosen alongside Claude
	}
	// Claude picked alongside a local default. Routing it needs no round-trip to
	// ask each server what it holds, and it must not fall through to
	// localBackendFor: a claude-* id is not served there, so it would end up at
	// Ollama and 404.
	if s.anthropicConfigured() && strings.HasPrefix(model, anthropicModelPrefix) {
		return s.newAnthropicBackend()
	}
	if model == "" || !s.dualBackend() {
		return s.newChatBackend()
	}
	return s.localBackendFor(model)
}

// localBackendFor picks between the OpenAI-compatible server and Ollama: a model
// the vLLM/openai server lists goes there, anything else goes to Ollama.
func (s *Server) localBackendFor(model string) ollama.Backend {
	if s.cfg.OpenAIBaseURL == "" {
		return ollama.NewClient(s.cfg.OllamaURL)
	}
	oai := openai.NewClient(s.cfg.OpenAIBaseURL, s.cfg.OpenAIAPIKey)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if served, err := oai.ListModels(ctx); err == nil {
		for _, m := range served {
			if m == model {
				return oai // served by the vLLM/openai server
			}
		}
		return ollama.NewClient(s.cfg.OllamaURL) // otherwise it's an Ollama model
	}
	// A healthy secondary remains selectable when the primary is down.
	if s.cfg.OllamaURL != "" {
		local := ollama.NewClient(s.cfg.OllamaURL)
		probe, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if models, err := local.ListModels(probe); err == nil {
			for _, served := range models {
				if served == model {
					return local
				}
			}
		}
	}
	// openai server unreachable: fall back to the primary chat backend — except
	// when that is Anthropic, where a local model name is a guaranteed 404.
	if s.cfg.LLMBackend == "anthropic" {
		return ollama.NewClient(s.cfg.OllamaURL)
	}
	return s.newChatBackend()
}

// embedBackend selects the backend for RAG embeddings + vision captioning.
// It defaults to the chat backend (LLMBackend) but can be pinned independently
// via EMBED_BACKEND — e.g. keep RAG on Ollama when chat runs on a vLLM server
// that only serves /v1/chat/completions and has no /v1/embeddings (Qwen3.5 +
// DFlash on the DGX Spark is exactly this case).
func (s *Server) embedBackend() string {
	if s.cfg.EmbedBackend != "" {
		return s.cfg.EmbedBackend
	}
	// Anthropic serves no embeddings and no /v1/embeddings-shaped endpoint at
	// all, so following the chat backend there would leave RAG dead. Ollama is
	// the only local option, and it is already wired.
	if s.cfg.LLMBackend == "anthropic" {
		return "ollama"
	}
	return s.cfg.LLMBackend
}

// captionModel is the model used for RAG vision captioning. It defaults to the
// chat model but VISION_MODEL overrides it — needed when captioning is pinned to
// Ollama (EMBED_BACKEND=ollama) while chat runs a text-only server, since the
// chat model name won't exist on the Ollama side.
func (s *Server) captionModel() string {
	if s.cfg.VisionModel != "" {
		return s.cfg.VisionModel
	}
	return s.cfg.Model
}

// newEmbedder returns the RAG embedder for the embedding backend.
func (s *Server) newEmbedder() *rag.Embedder {
	if s.embedBackend() == "openai" {
		return rag.NewOpenAIEmbedder(s.cfg.OpenAIBaseURL, s.cfg.OpenAIAPIKey, s.cfg.EmbedModel)
	}
	return rag.NewEmbedder(s.cfg.OllamaURL, s.cfg.EmbedModel)
}

// newCaptioner returns the RAG vision captioner for the embedding backend.
func (s *Server) newCaptioner() *rag.Captioner {
	if s.embedBackend() == "openai" {
		return rag.NewOpenAICaptioner(s.cfg.OpenAIBaseURL, s.cfg.OpenAIAPIKey, s.captionModel())
	}
	return rag.NewCaptioner(s.cfg.OllamaURL, s.captionModel())
}
