package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"

	"prism/internal/memory"
	"prism/internal/ollama"
)

// One encrypted record makes provider, endpoint, model and credential changes
// atomic. It is an integration secret, never exposed to scripts or secret APIs.
const aiProfileSecret = "prism_ai_profile"

type aiProfile struct {
	Sources        []aiSource        `json:"sources,omitempty"`
	DefaultSource  string            `json:"defaultSource,omitempty"`
	ServerDefaults bool              `json:"serverDefaults,omitempty"`
	Provider       string            `json:"provider"`
	BaseURL        string            `json:"baseURL"`
	Model          string            `json:"model"`
	APIKey         string            `json:"apiKey,omitempty"`
	Embedding      *embeddingProfile `json:"embedding,omitempty"`
	ChatVision     *bool             `json:"chatVision,omitempty"`
}

func loadAIProfile(ctx context.Context, ms *memory.Store) (*aiProfile, error) {
	if ms == nil {
		return nil, nil
	}
	raw, ok, err := ms.GetSecret(ctx, aiProfileSecret)
	if err != nil {
		return nil, errors.New("cannot read AI configuration")
	}
	if !ok || raw == "" {
		return nil, nil
	}
	var p aiProfile
	if json.Unmarshal([]byte(raw), &p) != nil {
		return nil, errors.New("invalid stored AI configuration")
	}
	if err := p.normalize(); err != nil || p.Model == "" {
		return nil, errors.New("invalid stored AI configuration")
	}
	return &p, nil
}

func (p *aiProfile) normalize() error {
	if p.Sources != nil {
		return p.normalizeSources()
	}
	p.Provider = strings.TrimSpace(p.Provider)
	p.Model = strings.TrimSpace(p.Model)
	p.BaseURL = strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
	p.APIKey = strings.TrimSpace(p.APIKey)
	switch p.Provider {
	case "openai":
		if p.BaseURL == "" {
			p.BaseURL = "https://api.openai.com/v1"
		}
	case "anthropic":
		if p.BaseURL == "" {
			p.BaseURL = "https://api.anthropic.com"
		}
	case "other":
		if p.BaseURL == "" {
			return errors.New("compatible API URL required")
		}
	case "ollama":
		if p.BaseURL == "" {
			return errors.New("Ollama server URL required")
		}
	default:
		return errors.New("choose OpenAI, Anthropic, Ollama or Other")
	}
	if p.Provider == "openai" && p.BaseURL != "https://api.openai.com/v1" {
		p.Provider = "other"
	}
	u, err := url.Parse(p.BaseURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("use an HTTP(S) server URL without credentials, query or fragment")
	}
	if len(p.Model) > 200 || strings.ContainsAny(p.Model, "\r\n") {
		return errors.New("model required (maximum 200 characters)")
	}
	return nil
}

// This value is used only by backend.go; never copy a live Server and its locks.
func (p aiProfile) backendConfig() *Server {
	cfg := Config{LLMBackend: p.Provider, Model: p.Model}
	switch p.Provider {
	case "openai", "other":
		cfg.LLMBackend = "openai"
		cfg.OpenAIBaseURL, cfg.OpenAIAPIKey = p.BaseURL, p.APIKey
	case "anthropic":
		cfg.AnthropicBaseURL, cfg.AnthropicToken, cfg.AnthropicModel = p.BaseURL, p.APIKey, p.Model
	case "ollama":
		cfg.OllamaURL = p.BaseURL
	}
	if p.ChatVision != nil {
		cfg.ChatVision = *p.ChatVision
	}
	cfg.AISources = p.Sources
	cfg.AIDefaultSource = p.DefaultSource
	return &Server{cfg: cfg}
}

func (s *Server) aiStoreForID(id int64) *memory.Store {
	return s.store() // Deployment AI settings: local owner or global administrator.

}

func (s *Server) aiConfigFor(ctx context.Context, id int64) (*Server, error) {
	p, err := loadAIProfile(ctx, s.aiStoreForID(id))
	if err != nil {
		return nil, err
	}
	if p == nil || p.ServerDefaults {
		return &Server{cfg: s.cfg}, nil
	}
	ai := p.backendConfig()
	if p.ChatVision == nil {
		ai.cfg.ChatVision = s.cfg.ChatVision
	}
	return ai, nil
}

func requestUserID(r *http.Request) int64 {
	if u := currentUser(r); u != nil {
		return u.ID
	}
	return 0
}

func (s *Server) validateAIProfileFor(r *http.Request, p *aiProfile) error {
	if err := p.normalize(); err != nil {
		return err
	}
	u := currentUser(r)
	if s.cfg.MultiUser && (u == nil || !u.IsGlobalAdmin()) {
		return errors.New("AI configuration is managed by a global administrator")
	}
	if !s.userCanUseModel(r.Context(), u, p.Model) {
		return errors.New("this model is not allowed by your administrator")
	}
	if p.Provider == "anthropic" && p.APIKey == "" {
		return errors.New("Anthropic API key required")
	}
	if p.BaseURL == "https://api.openai.com/v1" && p.APIKey == "" {
		return errors.New("OpenAI API key required")
	}
	return nil
}

// GET never returns any credential, including deployment environment values.
// POST tests or saves a full profile. A blank key retains the old key only if
// provider AND endpoint are identical; never send it to a newly entered host.
func (s *Server) handleAIProfile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.cfg.MultiUser && s.requireGlobalAdmin(w, r) == nil {
		return
	}
	ms := s.store()
	if ms == nil {
		writeErr(w, 503, "AI settings require the database")
		return
	}
	if r.Method == http.MethodDelete {
		if err := s.resetAIProfile(r.Context(), r.URL.Query().Get("reindex") == "true"); err != nil {
			writeErr(w, 409, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	old, err := loadAIProfile(r.Context(), ms)
	if err != nil {
		writeErr(w, 503, err.Error())
		return
	}
	switch r.Method {
	case http.MethodGet:
		source := "configured"
		if old == nil || old.ServerDefaults {
			old = s.environmentAIProfile()
			source = "server"
		}
		writeJSON(w, s.aiPublicView(old, source))
	case http.MethodPost:
		var b struct {
			aiProfile
			Action   string `json:"action"`
			ClearKey bool   `json:"clearKey"`
			SourceID string `json:"sourceID"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&b) != nil {
			writeErr(w, 400, "invalid AI configuration")
			return
		}
		if b.Action != "save" && b.Action != "test" && b.Action != "models" && b.Action != "embedding_models" && b.Action != "embedding_test" {
			writeErr(w, 400, "choose save, test, models, embedding_models or embedding_test")
			return
		}
		if err := b.normalize(); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		if old == nil || old.ServerDefaults {
			old = s.environmentAIProfile()
		}
		if b.ClearKey {
			b.APIKey = ""
		}
		if b.APIKey == "" && !b.ClearKey && old != nil && old.Provider == b.Provider && old.BaseURL == b.BaseURL {
			b.APIKey = old.APIKey
		}
		if b.Sources != nil || b.Action == "save" {
			if err := s.prepareSources(&b.aiProfile, old); err != nil {
				writeErr(w, 400, err.Error())
				return
			}
		}
		if (b.Action == "models" || b.Action == "test") && b.SourceID != "" {
			found := false
			for _, src := range b.Sources {
				if src.ID == b.SourceID {
					b.aiProfile = src.profile()
					found = true
					break
				}
			}
			if !found {
				writeErr(w, 400, "unknown AI source")
				return
			}
		}
		if b.Action != "models" && b.Action != "embedding_models" && b.Action != "embedding_test" && b.Model == "" {
			writeErr(w, 400, "model required")
			return
		}
		if err := s.validateAIProfileFor(r, &b.aiProfile); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		if b.Action == "save" || strings.HasPrefix(b.Action, "embedding_") {
			if err := s.prepareEmbedding(&b.aiProfile, old); err != nil {
				writeErr(w, 400, err.Error())
				return
			}
		}
		if strings.HasPrefix(b.Action, "embedding_") {
			ep := b.effectiveEmbedding()
			if ep == nil || ep.Provider == "anthropic" {
				writeErr(w, 400, "This provider does not offer embeddings. Disable Use same provider and select another connection.")
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			defer cancel()
			if b.Action == "embedding_models" {
				models, err := ep.backendConfig().newChatBackend().ListModels(ctx)
				if err != nil {
					writeErr(w, 502, "Cannot load models from the embedding provider")
					return
				}
				writeJSON(w, map[string]any{"models": embeddingCandidates(models), "manualAllowed": true})
				return
			}
			if ep.Model == "" {
				writeErr(w, 400, "embedding model required")
				return
			}
			dim, err := ep.embedder().Dim(ctx)
			if err != nil {
				writeErr(w, 502, "Embedding test failed: check the model, URL, credential and quota")
				return
			}
			writeJSON(w, map[string]any{"ok": true, "dimension": dim})
			return
		}
		if b.Action == "save" {
			if err := s.saveAIProfile(r.Context(), currentUser(r), &b.aiProfile, old); err != nil {
				writeErr(w, 409, err.Error())
				return
			}
			writeJSON(w, map[string]any{"ok": true, "embeddingPending": s.embeddingPending(b.aiProfile)})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		backend := b.backendConfig().newChatBackend()
		if b.Action == "models" {
			models, err := backend.ListModels(ctx)
			if err != nil {
				writeErr(w, 502, "cannot list models: check the server and credential")
				return
			}
			allowed := []string{}
			for _, m := range models {
				if s.userCanUseModel(ctx, currentUser(r), m) {
					allowed = append(allowed, m)
				}
			}
			writeJSON(w, map[string]any{"models": allowed})
			return
		}
		if err := probeAI(ctx, backend, b.Model); err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	default:
		writeErr(w, 405, "method not allowed")
	}
}

// A completion verifies actual model access; listing models alone can succeed
// with an unusable model (some providers even return a fallback list).
func probeAI(ctx context.Context, backend ollama.Backend, model string) error {
	events := make(chan ollama.StreamEvent, 16)
	go func() {
		backend.Chat(ctx, ollama.ChatRequest{Model: model, Messages: []ollama.Message{{Role: "user", Content: "Reply OK."}}, NoThinking: true, Options: ollama.Options{NumPredict: 32}}, events)
		close(events)
	}()
	content := false
	var failed bool
	for ev := range events {
		if ev.Err != nil {
			failed = true
		}
		if strings.TrimSpace(ev.Content) != "" {
			content = true
		}
	}
	if ctx.Err() != nil {
		return errors.New("AI test timed out or was cancelled")
	}
	if failed || !content {
		return errors.New("AI test failed: check the credential, model access, server and provider quota")
	}
	return nil
}

// Compare only chat routing values, without serializing credentials or copying locks.
func sameAIConfig(a, b Config) bool {
	return a.LLMBackend == b.LLMBackend && a.Model == b.Model && a.OpenAIBaseURL == b.OpenAIBaseURL && a.OpenAIAPIKey == b.OpenAIAPIKey && a.OllamaURL == b.OllamaURL && a.AnthropicBaseURL == b.AnthropicBaseURL && a.AnthropicToken == b.AnthropicToken && a.AnthropicModel == b.AnthropicModel && a.ChatVision == b.ChatVision && a.AIDefaultSource == b.AIDefaultSource && reflect.DeepEqual(a.AISources, b.AISources)
}

// The existing agent_settings tool owns AI settings too. A secret name may be
// supplied, never a credential in model arguments or tool results.
func (s *Server) aiSettingsTool(u *memory.User) func(context.Context, map[string]any) (string, error) {
	return func(ctx context.Context, args map[string]any) (string, error) {
		if s.cfg.MultiUser && (u == nil || !u.IsGlobalAdmin()) {
			return "", errors.New("AI configuration is managed in Admin → AI provider")
		}
		if strings.HasPrefix(argStr(args, "action"), "ai_source_") {
			return s.aiSourceTool(ctx, u, args)
		}
		id := int64(0)
		if u != nil {
			id = u.ID
		}
		ms := s.aiStoreForID(id)
		if ms == nil {
			return "", errors.New("AI settings require the database")
		}
		if argStr(args, "action") == "ai_reset" {
			if err := s.resetAIProfile(ctx, args["reindex"] == true); err != nil {
				return "", err
			}
			return "Server AI defaults restored. Chat applies next message; embedding changes apply automatically.", nil
		}
		old, err := loadAIProfile(ctx, ms)
		if err != nil {
			return "", err
		}
		action := argStr(args, "action")
		if action == "ai_get" {
			source := "configured"
			if old == nil || old.ServerDefaults {
				old = s.environmentAIProfile()
				source = "server"
			}
			raw, _ := json.Marshal(s.aiPublicView(old, source))
			return string(raw), nil
		}
		if action != "ai_set" && action != "ai_test" && action != "ai_models" && action != "ai_embedding_models" && action != "ai_embedding_test" {
			return "", errors.New("expected ai_get, ai_set, ai_test, ai_models, ai_embedding_models, ai_embedding_test or ai_reset")
		}
		if old == nil || old.ServerDefaults {
			old = s.environmentAIProfile()
		}
		p := *s.environmentAIProfile()
		if old != nil {
			p = *old
		}
		p.Sources = nil // Legacy ai_set edits the default source only.
		if p.Embedding != nil {
			cp := *p.Embedding
			p.Embedding = &cp
		}
		explicitEmbedding := false
		for key := range args {
			if strings.HasPrefix(key, "embedding_") {
				explicitEmbedding = true
			}
		}
		if !explicitEmbedding {
			p.Embedding = nil
		}
		if v, ok := args["provider"].(string); ok {
			p.Provider = v
		}
		if v, ok := args["base_url"].(string); ok {
			p.BaseURL = v
		} else if old != nil && p.Provider != old.Provider {
			p.BaseURL = ""
		}
		if v, ok := args["model"].(string); ok {
			p.Model = v
		}
		if err := p.normalize(); err != nil {
			return "", err
		}
		if old != nil && (old.Provider != p.Provider || old.BaseURL != p.BaseURL) {
			p.APIKey = ""
		}
		if name := argStr(args, "key_secret"); name != "" {
			if err := memory.ValidateScriptSecretName(name); err != nil {
				return "", errors.New("use a personal script secret name, not an integration or scoped secret")
			}
			credentialStore := ms
			if u != nil {
				credentialStore = ms.ConfigScope(fmt.Sprintf("u%d", u.ID))
			}
			key, ok, err := credentialStore.GetSecret(ctx, name)
			if err != nil || !ok || key == "" {
				return "", errors.New("credential unavailable; use request_secret or Settings → AI provider to enter it securely")
			}
			p.APIKey = key
		}
		if action != "ai_models" && p.Model == "" {
			return "", errors.New("model required")
		}
		r, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/api/ai/config", nil)
		r = withUser(r, u)
		if err := s.validateAIProfileFor(r, &p); err != nil {
			return "", err
		}
		// Key names refer to the caller's script secrets, not global credentials.
		credentials := ms
		if u != nil {
			credentials = ms.ConfigScope(fmt.Sprintf("u%d", u.ID))
		}
		if err := s.embeddingToolArgs(ctx, credentials, args, &p); err != nil {
			return "", err
		}
		if err := s.prepareSources(&p, old); err != nil {
			return "", err
		}
		if err := s.prepareEmbedding(&p, old); err != nil {
			return "", err
		}
		if strings.HasPrefix(action, "ai_embedding_") {
			ep := p.effectiveEmbedding()
			if ep == nil || ep.Provider == "anthropic" {
				return "", errors.New("choose an embedding provider; Anthropic has no embedding endpoint")
			}
			probe, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if action == "ai_embedding_models" {
				models, err := ep.backendConfig().newChatBackend().ListModels(probe)
				if err != nil {
					return "", errors.New("cannot list embedding models")
				}
				data, _ := json.Marshal(embeddingCandidates(models))
				return string(data), nil
			}
			if ep.Model == "" {
				return "", errors.New("embedding_model required")
			}
			dim, err := ep.embedder().Dim(probe)
			if err != nil {
				return "", errors.New("embedding test failed; check model, URL and credential")
			}
			return fmt.Sprintf("Embedding endpoint verified: %d dimensions. Configuration unchanged.", dim), nil
		}
		if action == "ai_set" {
			if err := s.saveAIProfile(ctx, u, &p, old); err != nil {
				return "", err
			}
			return "AI configuration saved. Other sources retained. Chat applies next message; embedding changes apply automatically.", nil
		}
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		be := p.backendConfig().newChatBackend()
		if action == "ai_models" {
			models, err := be.ListModels(ctx)
			if err != nil {
				return "", errors.New("cannot list models; check server and credential")
			}
			allowed := []string{}
			for _, m := range models {
				if s.userCanUseModel(ctx, u, m) {
					allowed = append(allowed, m)
				}
			}
			raw, _ := json.Marshal(allowed)
			return string(raw), nil
		}
		if err := probeAI(ctx, be, p.Model); err != nil {
			return "", err
		}
		return "The model responded successfully. Testing did not change your settings.", nil
	}
}
