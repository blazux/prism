package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"prism/internal/memory"
	"prism/internal/rag"
)

type embeddingProfile struct {
	SourceID        string `json:"sourceID,omitempty"`
	ReindexFor      string `json:"reindexFor,omitempty"`
	UseSameProvider bool   `json:"useSameProvider"`
	Provider        string `json:"provider"`
	BaseURL         string `json:"baseURL"`
	APIKey          string `json:"apiKey,omitempty"`
	Model           string `json:"model"`
	ClearKey        bool   `json:"clearKey,omitempty"`
	Reindex         bool   `json:"reindex,omitempty"`
}

func (s *Server) environmentAIProfile() *aiProfile {
	p := &aiProfile{Provider: s.cfg.LLMBackend, Model: s.cfg.Model}
	vision := s.cfg.ChatVision
	p.ChatVision = &vision
	switch p.Provider {
	case "openai":
		p.BaseURL, p.APIKey = s.cfg.OpenAIBaseURL, s.cfg.OpenAIAPIKey
	case "anthropic":
		p.BaseURL, p.APIKey = s.cfg.AnthropicBaseURL, s.cfg.AnthropicToken
	default:
		p.Provider = "ollama"
		p.BaseURL = s.cfg.OllamaURL
	}
	// A custom OpenAI-compatible endpoint belongs under Other in the UI.
	if p.Provider == "openai" && p.BaseURL != "" && strings.TrimRight(p.BaseURL, "/") != "https://api.openai.com/v1" {
		p.Provider = "other"
	}
	_ = p.normalize()
	e := &embeddingProfile{Provider: s.embedBackend(), Model: s.cfg.EmbedModel}
	if e.Provider == "openai" {
		e.BaseURL, e.APIKey = s.cfg.OpenAIBaseURL, s.cfg.OpenAIAPIKey
		if strings.TrimRight(e.BaseURL, "/") != "https://api.openai.com/v1" {
			e.Provider = "other"
		}
	} else {
		e.Provider = "ollama"
		e.BaseURL = s.cfg.OllamaURL
	}
	e.BaseURL = strings.TrimRight(e.BaseURL, "/")
	e.UseSameProvider = e.Provider == p.Provider && e.BaseURL == p.BaseURL && e.APIKey == p.APIKey
	if e.Model == "" && s.cfg.EmbedBackend == "" {
		e.UseSameProvider = true
	}
	p.Embedding = e
	return p
}

func (p aiProfile) effectiveEmbedding() *aiProfile {
	e := p.Embedding
	if e == nil {
		return nil
	}
	ep := &aiProfile{Provider: e.Provider, BaseURL: e.BaseURL, APIKey: e.APIKey, Model: e.Model}
	if !e.UseSameProvider && e.SourceID != "" {
		for _, src := range p.Sources {
			if src.ID == e.SourceID {
				ep.Provider, ep.BaseURL, ep.APIKey = src.Provider, src.BaseURL, src.APIKey
				break
			}
		}
	}
	if e.UseSameProvider {
		ep.Provider, ep.BaseURL, ep.APIKey = p.Provider, p.BaseURL, p.APIKey
	}
	return ep
}
func (p aiProfile) embedder() *rag.Embedder {
	if p.Provider == "ollama" {
		return rag.NewEmbedder(p.BaseURL, p.Model)
	}
	return rag.NewOpenAIEmbedder(p.BaseURL, p.APIKey, p.Model)
}
func embeddingIdentity(p *aiProfile) string {
	if p == nil || p.Model == "" {
		return ""
	}
	provider := p.Provider
	if provider == "other" {
		provider = "openai"
	}
	sum := sha256.Sum256([]byte(provider + "\n" + strings.TrimRight(p.BaseURL, "/") + "\n" + p.Model))
	return hex.EncodeToString(sum[:])
}
func sameEmbedding(a, b aiProfile) bool {
	ap, bp := a.effectiveEmbedding(), b.effectiveEmbedding()
	if embeddingIdentity(ap) == "" && embeddingIdentity(bp) == "" {
		return true
	}
	if embeddingIdentity(ap) != embeddingIdentity(bp) {
		return false
	}
	return ap == nil && bp == nil || ap != nil && bp != nil && ap.APIKey == bp.APIKey
}
func (s *Server) prepareEmbedding(p, old *aiProfile) error {
	if old == nil {
		old = s.environmentAIProfile()
	}
	if p.Embedding == nil {
		e := old.effectiveEmbedding()
		if e == nil {
			e = s.environmentAIProfile().effectiveEmbedding()
		}
		// Legacy chat-only callers must not silently move the document index.
		p.Embedding = &embeddingProfile{Provider: e.Provider, BaseURL: e.BaseURL, APIKey: e.APIKey, Model: e.Model}
		return nil
	}
	e := p.Embedding
	e.Model = strings.TrimSpace(e.Model)
	if !e.UseSameProvider && e.SourceID != "" {
		found := false
		for _, src := range p.Sources {
			if src.ID == e.SourceID {
				e.Provider, e.BaseURL, e.APIKey = src.Provider, src.BaseURL, src.APIKey
				found = true
				break
			}
		}
		if !found {
			return errors.New("embedding source was removed; choose another source or a dedicated connection")
		}
	}
	if e.UseSameProvider {
		e.Provider, e.BaseURL, e.APIKey = p.Provider, p.BaseURL, p.APIKey
	} else if e.SourceID == "" {
		ep := aiProfile{Provider: e.Provider, BaseURL: e.BaseURL, APIKey: e.APIKey, Model: e.Model}
		if err := ep.normalize(); err != nil {
			return err
		}
		e.Provider, e.BaseURL, e.APIKey = ep.Provider, ep.BaseURL, ep.APIKey
		prev := old.effectiveEmbedding()
		if prev != nil {
			_ = prev.normalize()
		}
		if e.ClearKey {
			e.APIKey = ""
		} else if e.APIKey == "" && prev != nil && e.Provider == prev.Provider && e.BaseURL == prev.BaseURL {
			e.APIKey = prev.APIKey
		}
	}
	if len(e.Model) > 200 || strings.ContainsAny(e.Model, "\r\n") {
		return errors.New("invalid embedding model")
	}
	if e.Model != "" && e.Provider == "anthropic" {
		return errors.New("Anthropic has no embedding endpoint. Disable Use same provider and choose an embedding provider")
	}
	return nil
}
func embeddingCandidates(models []string) []string {
	found := []string{}
	for _, m := range models {
		if strings.Contains(strings.ToLower(m), "embed") {
			found = append(found, m)
		}
	}
	// Catalogs do not reliably describe capabilities. Always allow a manual ID.
	if len(found) == 0 {
		return models
	}
	return found
}
func (s *Server) aiPublicView(p *aiProfile, source string) map[string]any {
	p = s.withAISources(p)
	cp := *p
	if cp.Embedding == nil {
		cp.Embedding = s.environmentAIProfile().Embedding
	}
	e := cp.Embedding
	ep := cp.effectiveEmbedding()
	_ = ep.normalize()
	vision := s.cfg.ChatVision
	if p.ChatVision != nil {
		vision = *p.ChatVision
	}
	status, _ := ragInitStatus.Load().(string)
	return map[string]any{"sources": publicSources(p), "defaultSource": p.DefaultSource, "embeddingStatus": status, "source": source, "provider": p.Provider, "baseURL": p.BaseURL, "model": p.Model, "keyConfigured": p.APIKey != "", "chatVision": vision, "multiUser": s.cfg.MultiUser,
		"embedding":         map[string]any{"sourceID": e.SourceID, "useSameProvider": e.UseSameProvider, "provider": ep.Provider, "baseURL": ep.BaseURL, "model": ep.Model, "keyConfigured": ep.APIKey != ""},
		"embeddingApplying": s.ragUpdating.Load(), "embeddingPending": s.embeddingPending(cp)}
}
func (s *Server) embeddingPending(p aiProfile) bool {
	s.mu.Lock()
	active := s.activeEmbedding
	s.mu.Unlock()
	if active == nil {
		return !sameEmbedding(p, *s.environmentAIProfile())
	}
	return !sameEmbedding(p, *active)
}
func (s *Server) validateEmbeddingSave(ctx context.Context, p, old *aiProfile) error {
	if err := s.prepareEmbedding(p, old); err != nil {
		return err
	}
	if old == nil {
		old = s.environmentAIProfile()
	}
	if old.Embedding == nil {
		cp := *old
		cp.Embedding = s.environmentAIProfile().Embedding
		old = &cp
	}
	ep := p.effectiveEmbedding()
	if sameEmbedding(*p, *old) {
		p.Embedding.Reindex = p.Embedding.Reindex || old.Embedding.Reindex
		if p.Embedding.Reindex {
			p.Embedding.ReindexFor = embeddingIdentity(ep)
		}
		return nil
	}
	if ep.Model != "" {
		probe, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if _, err := ep.embedder().Dim(probe); err != nil {
			return errors.New("embedding test failed: check model, URL, credential and quota; settings were not saved")
		}
	}
	if embeddingIdentity(ep) != embeddingIdentity(old.effectiveEmbedding()) && !p.Embedding.Reindex {
		return errors.New("confirm rebuilding the document index before changing embedding model or provider (reindex: true)")
	}
	if p.Embedding.Reindex {
		p.Embedding.ReindexFor = embeddingIdentity(ep)
	}
	return nil
}

func (s *Server) resetAIProfile(ctx context.Context, confirmed bool) error {
	old, err := loadAIProfile(ctx, s.store())
	if err != nil {
		return err
	}
	if old == nil {
		old = s.environmentAIProfile()
	}
	p := s.environmentAIProfile()
	p.Embedding.Reindex = confirmed
	if err = s.validateEmbeddingSave(ctx, p, old); err != nil {
		return err
	}
	// Keep the explicit reindex permission for the confirmed embedding identity.
	p.ServerDefaults = true
	raw, _ := json.Marshal(p)
	if err = s.store().SetSecret(ctx, aiProfileSecret, string(raw)); err != nil {
		return errors.New("cannot reset AI settings")
	}
	s.scheduleRAGApply()
	return nil
}

func (s *Server) embeddingToolArgs(ctx context.Context, ms *memory.Store, args map[string]any, p *aiProfile) error {
	if vision, ok := args["chat_vision"].(bool); ok {
		p.ChatVision = &vision
	}
	raw := map[string]any{}
	for _, key := range []string{"provider", "base_url", "model", "key_secret", "use_same_provider", "source_id"} {
		if v, ok := args["embedding_"+key]; ok {
			raw[key] = v
		}
	}
	if len(raw) > 0 {
		raw["reindex"] = args["reindex"]
		e := embeddingProfile{UseSameProvider: true}
		if p.Embedding != nil {
			e = *p.Embedding
		}
		if v, ok := raw["use_same_provider"].(bool); ok {
			e.UseSameProvider = v
		}
		if v, ok := raw["source_id"].(string); ok {
			e.SourceID = v
		}
		if v, ok := raw["provider"].(string); ok {
			if v != e.Provider {
				e.BaseURL = ""
				e.APIKey = ""
			}
			e.Provider = v
		}
		if v, ok := raw["base_url"].(string); ok {
			if v != e.BaseURL {
				e.APIKey = ""
			}
			e.BaseURL = v
		}
		if v, ok := raw["model"].(string); ok {
			e.Model = v
		}
		e.Reindex = raw["reindex"] == true
		if name, ok := raw["key_secret"].(string); ok && name != "" {
			if memory.ValidateScriptSecretName(name) != nil {
				return errors.New("use a script secret name for the embedding credential")
			}
			key, found, err := ms.GetSecret(ctx, name)
			if err != nil || !found || key == "" {
				return errors.New("embedding credential unavailable; use request_secret")
			}
			e.APIKey = key
		}
		p.Embedding = &e
	}
	return nil
}
