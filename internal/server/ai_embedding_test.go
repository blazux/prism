package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestEmbeddingInheritanceAndCredentialBoundaries(t *testing.T) {
	s := &Server{cfg: Config{LLMBackend: "openai", OpenAIBaseURL: "https://local.example/v1", OpenAIAPIKey: "private-chat", Model: "chat", EmbedBackend: "ollama", OllamaURL: "http://ollama:11434", EmbedModel: "embed"}}
	old := s.environmentAIProfile()
	if old.Embedding.UseSameProvider || old.Provider != "other" {
		t.Fatal("existing .env routing changed")
	}
	p := aiProfile{Provider: "other", BaseURL: "https://new.example/v1", Model: "new-chat", APIKey: "new-private"}
	if err := s.prepareEmbedding(&p, old); err != nil {
		t.Fatal(err)
	}
	if p.effectiveEmbedding().BaseURL != "http://ollama:11434" {
		t.Fatal("chat update moved embedding server")
	}
	p.Embedding = &embeddingProfile{UseSameProvider: true, Model: "embed"}
	if err := s.prepareEmbedding(&p, old); err != nil {
		t.Fatal(err)
	}
	if p.effectiveEmbedding().APIKey != "new-private" {
		t.Fatal("same provider did not reuse key")
	}
	old = p.backendProfileCopy()
	p.Embedding = &embeddingProfile{Provider: "other", BaseURL: "https://unrelated.example/v1", Model: "embed"}
	if err := s.prepareEmbedding(&p, old); err != nil {
		t.Fatal(err)
	}
	if p.Embedding.APIKey != "" {
		t.Fatal("embedding credential crossed endpoints")
	}
	p.Embedding.BaseURL = "https://new.example/v1"
	p.Embedding.ClearKey = true
	if err := s.prepareEmbedding(&p, old); err != nil {
		t.Fatal(err)
	}
	if err := s.prepareEmbedding(&p, old); err != nil {
		t.Fatal(err)
	}
	if p.Embedding.APIKey != "" {
		t.Fatal("cleared key was restored on revalidation")
	}
	raw, _ := json.Marshal(s.aiPublicView(old, "configured"))
	if strings.Contains(string(raw), "private") || strings.Contains(string(raw), "apiKey") {
		t.Fatal("credential exposed")
	}
}
func (p aiProfile) backendProfileCopy() *aiProfile {
	cp := p
	if p.Embedding != nil {
		e := *p.Embedding
		cp.Embedding = &e
	}
	return &cp
}
func TestEmbeddingIdentityAndAnthropic(t *testing.T) {
	a := aiProfile{Provider: "other", BaseURL: "http://host/v1", Model: "a"}
	b := a
	b.Provider = "openai"
	if embeddingIdentity(&a) != embeddingIdentity(&b) {
		t.Fatal("compatible aliases differ")
	}
	b.Model = "b"
	if embeddingIdentity(&a) == embeddingIdentity(&b) {
		t.Fatal("models with same dimensions could mix")
	}
	s := &Server{}
	p := aiProfile{Provider: "anthropic", BaseURL: "https://api.anthropic.com", Embedding: &embeddingProfile{UseSameProvider: true, Model: "embed"}}
	if err := s.prepareEmbedding(&p, nil); err == nil {
		t.Fatal("Anthropic silently accepted embeddings")
	}
	p.Embedding.Model = ""
	if err := s.prepareEmbedding(&p, nil); err != nil {
		t.Fatal("chat-only Anthropic blocked", err)
	}
	// No provider request for a chat-only update with unchanged embeddings.
	old := s.environmentAIProfile()
	p = *old
	if err := s.validateEmbeddingSave(context.Background(), &p, old); err != nil {
		t.Fatal(err)
	}
}
