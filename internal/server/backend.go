package server

import (
	"context"
	"time"

	"prism/internal/ollama"
	"prism/internal/openai"
	"prism/internal/rag"
)

// newChatBackend returns the chat backend selected by LLM_BACKEND. It defaults
// to Ollama; "openai" targets any OpenAI-compatible server (SGLang, vLLM, …).
func (s *Server) newChatBackend() ollama.Backend {
	if s.cfg.LLMBackend == "openai" {
		return openai.NewClient(s.cfg.OpenAIBaseURL, s.cfg.OpenAIAPIKey)
	}
	return ollama.NewClient(s.cfg.OllamaURL)
}

// dualBackend reports whether both an OpenAI-compatible server AND an Ollama
// server are wired, so the model picker can span both regardless of which one is
// the default — e.g. AcidBurn on Ollama (default) + DFlash `qwen` on vLLM.
func (s *Server) dualBackend() bool {
	return s.cfg.OpenAIBaseURL != "" && s.cfg.OllamaURL != ""
}

// chatModels returns the union of selectable chat models. In dual-backend mode
// it merges the primary backend's models with the other server's, so one picker
// offers both — the full Ollama menu plus the vLLM model (in either direction).
func (s *Server) chatModels(ctx context.Context) ([]string, error) {
	models, err := s.newChatBackend().ListModels(ctx)
	if err != nil {
		return nil, err
	}
	if s.dualBackend() {
		var secondary ollama.Backend
		if s.cfg.LLMBackend == "openai" {
			secondary = ollama.NewClient(s.cfg.OllamaURL)
		} else {
			secondary = openai.NewClient(s.cfg.OpenAIBaseURL, s.cfg.OpenAIAPIKey)
		}
		if extra, e := secondary.ListModels(ctx); e == nil {
			seen := make(map[string]bool, len(models))
			for _, m := range models {
				seen[m] = true
			}
			for _, m := range extra {
				if !seen[m] {
					models = append(models, m)
				}
			}
		}
	}
	return models, nil
}

// chatBackendFor returns the backend that serves the given model, letting a
// single picker route each choice to the right server: a model the vLLM/openai
// server lists goes there, anything else goes to Ollama. Outside dual-backend
// mode (or for an empty model, or if the openai server can't be reached) it
// falls back to the configured primary chat backend.
func (s *Server) chatBackendFor(model string) ollama.Backend {
	if model == "" || !s.dualBackend() {
		return s.newChatBackend()
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
	return s.newChatBackend() // openai server unreachable: use the primary backend
}

// embedBackend selects the backend for RAG embeddings.
// It defaults to the chat backend (LLMBackend) but can be pinned independently
// via EMBED_BACKEND — e.g. keep RAG on Ollama when chat runs on a vLLM server
// that only serves /v1/chat/completions and has no /v1/embeddings (Qwen3.5 +
// DFlash on the DGX Spark is exactly this case).
func (s *Server) embedBackend() string {
	if s.cfg.EmbedBackend != "" {
		return s.cfg.EmbedBackend
	}
	return s.cfg.LLMBackend
}

// newEmbedder returns the RAG embedder for the active backend.
func (s *Server) newEmbedder() *rag.Embedder {
	if s.embedBackend() == "openai" {
		return rag.NewOpenAIEmbedder(s.cfg.OpenAIBaseURL, s.cfg.OpenAIAPIKey, s.cfg.EmbedModel)
	}
	return rag.NewEmbedder(s.cfg.OllamaURL, s.cfg.EmbedModel)
}
