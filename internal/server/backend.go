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

// dualBackend reports whether both a primary openai server AND an Ollama server
// are wired, so the model picker can span both (e.g. DFlash `qwen` on vLLM +
// AcidBurn on Ollama). Ollama is reachable in this mode because it already
// serves RAG embeddings (EMBED_BACKEND=ollama).
func (s *Server) dualBackend() bool {
	return s.cfg.LLMBackend == "openai" && s.cfg.OllamaURL != ""
}

// chatModels returns the union of selectable chat models. In dual-backend mode
// it merges the primary (openai/vLLM) models with the Ollama models so one
// picker offers both — like the original single-Ollama menu, plus the vLLM model.
func (s *Server) chatModels(ctx context.Context) ([]string, error) {
	models, err := s.newChatBackend().ListModels(ctx)
	if err != nil {
		return nil, err
	}
	if s.dualBackend() {
		if extra, e := ollama.NewClient(s.cfg.OllamaURL).ListModels(ctx); e == nil {
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
// single picker route each choice to the right server. Outside dual-backend
// mode (or for an empty model) it falls back to the configured chat backend.
func (s *Server) chatBackendFor(model string) ollama.Backend {
	if model == "" || !s.dualBackend() {
		return s.newChatBackend()
	}
	oai := openai.NewClient(s.cfg.OpenAIBaseURL, s.cfg.OpenAIAPIKey)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if primary, err := oai.ListModels(ctx); err == nil {
		for _, m := range primary {
			if m == model {
				return oai // served by the primary vLLM server
			}
		}
		return ollama.NewClient(s.cfg.OllamaURL) // otherwise it's an Ollama model
	}
	// Primary list unreachable: keep the default model on openai, route the rest
	// (an explicitly-picked non-default) to Ollama.
	if model != s.cfg.Model {
		return ollama.NewClient(s.cfg.OllamaURL)
	}
	return oai
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
