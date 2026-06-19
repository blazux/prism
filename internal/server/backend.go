package server

import (
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

// newEmbedder returns the RAG embedder for the active backend.
func (s *Server) newEmbedder() *rag.Embedder {
	if s.cfg.LLMBackend == "openai" {
		return rag.NewOpenAIEmbedder(s.cfg.OpenAIBaseURL, s.cfg.OpenAIAPIKey, s.cfg.EmbedModel)
	}
	return rag.NewEmbedder(s.cfg.OllamaURL, s.cfg.EmbedModel)
}

// newCaptioner returns the RAG vision captioner for the active backend, reusing
// the main chat model.
func (s *Server) newCaptioner() *rag.Captioner {
	if s.cfg.LLMBackend == "openai" {
		return rag.NewOpenAICaptioner(s.cfg.OpenAIBaseURL, s.cfg.OpenAIAPIKey, s.cfg.Model)
	}
	return rag.NewCaptioner(s.cfg.OllamaURL, s.cfg.Model)
}
