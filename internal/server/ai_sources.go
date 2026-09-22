package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"prism/internal/memory"
	"regexp"
	"strings"
	"time"

	"prism/internal/ollama"
)

type aiSource struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	BaseURL  string `json:"baseURL"`
	APIKey   string `json:"apiKey,omitempty"`
	Model    string `json:"model"`
	ClearKey bool   `json:"clearKey,omitempty"`
}

func (s aiSource) profile() aiProfile {
	return aiProfile{Provider: s.Provider, BaseURL: s.BaseURL, APIKey: s.APIKey, Model: s.Model}
}

var sourceIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,48}$`)

func (p *aiProfile) normalizeSources() error {
	if len(p.Sources) == 0 || len(p.Sources) > 20 {
		return errors.New("configure between 1 and 20 AI sources")
	}
	seen := map[string]bool{}
	found := false
	for i := range p.Sources {
		src := &p.Sources[i]
		if !sourceIDPattern.MatchString(src.ID) || seen[src.ID] {
			return errors.New("each source needs a unique ID (letters, digits, - or _)")
		}
		seen[src.ID] = true
		src.Name = strings.TrimSpace(src.Name)
		if src.Name == "" {
			src.Name = src.ID
		}
		if len(src.Name) > 100 {
			return errors.New("source name too long")
		}
		conn := src.profile()
		if err := conn.normalize(); err != nil {
			return fmt.Errorf("source %s: %w", src.ID, err)
		}
		src.Provider, src.BaseURL, src.APIKey, src.Model = conn.Provider, conn.BaseURL, conn.APIKey, conn.Model
		if src.ID == p.DefaultSource {
			p.Provider, p.BaseURL, p.APIKey, p.Model = src.Provider, src.BaseURL, src.APIKey, src.Model
			found = true
		}
	}
	if !found {
		return errors.New("choose an existing default source")
	}
	return nil
}

// Present environment connections alongside an older single-connection profile.
// Only explicit source deletion removes a connection from the saved configuration.
func (s *Server) withAISources(p *aiProfile) *aiProfile {
	cp := *p
	cp.Sources = append([]aiSource(nil), p.Sources...)
	if p.Sources != nil {
		return &cp
	}
	cp.DefaultSource = "primary"
	cp.Sources = []aiSource{{ID: "primary", Name: "Default source", Provider: p.Provider, BaseURL: p.BaseURL, APIKey: p.APIKey, Model: p.Model}}
	add := func(id, name, provider, url, key, model string) {
		c := aiProfile{Provider: provider, BaseURL: url, APIKey: key, Model: model}
		if c.normalize() != nil {
			return
		}
		for _, src := range cp.Sources {
			if src.Provider == c.Provider && src.BaseURL == c.BaseURL {
				return
			}
		}
		cp.Sources = append(cp.Sources, aiSource{ID: id, Name: name, Provider: c.Provider, BaseURL: c.BaseURL, APIKey: key, Model: model})
	}
	if s.cfg.OpenAIBaseURL != "" {
		add("openai", "OpenAI / compatible", "openai", s.cfg.OpenAIBaseURL, s.cfg.OpenAIAPIKey, "")
	}
	if s.cfg.OllamaURL != "" {
		add("ollama", "Ollama", "ollama", s.cfg.OllamaURL, "", "")
	}
	if s.cfg.AnthropicToken != "" {
		add("anthropic", "Anthropic", "anthropic", s.cfg.AnthropicBaseURL, s.cfg.AnthropicToken, s.cfg.AnthropicModel)
	}
	return &cp
}
func (s *Server) prepareSources(p, old *aiProfile) error {
	old = s.withAISources(old)
	if p.Sources == nil {
		// Compatibility for chat-only tools/API clients: edit the default, retain others.
		p.Sources = append([]aiSource(nil), old.Sources...)
		p.DefaultSource = old.DefaultSource
		for i := range p.Sources {
			if p.Sources[i].ID == p.DefaultSource {
				p.Sources[i].Provider, p.Sources[i].BaseURL, p.Sources[i].APIKey, p.Sources[i].Model = p.Provider, p.BaseURL, p.APIKey, p.Model
			}
		}
	} else {
		if err := p.normalizeSources(); err != nil {
			return err
		}
		for i := range p.Sources {
			src := &p.Sources[i]
			if src.ClearKey {
				src.APIKey = ""
			} else if src.APIKey == "" {
				for _, prev := range old.Sources {
					if src.ID == prev.ID && src.Provider == prev.Provider && src.BaseURL == prev.BaseURL {
						src.APIKey = prev.APIKey
						break
					}
				}
			}
		}
	}
	return p.normalizeSources()
}
func publicSources(p *aiProfile) []map[string]any {
	out := []map[string]any{}
	for _, s := range p.Sources {
		out = append(out, map[string]any{"id": s.ID, "name": s.Name, "provider": s.Provider, "baseURL": s.BaseURL, "model": s.Model, "keyConfigured": s.APIKey != ""})
	}
	return out
}

// Qualified model IDs keep two servers exposing the same model unambiguous.
// The selected default source retains raw IDs for existing sessions/policies.
type sourcesBackend struct {
	sources []aiSource
	primary string
}

func (b *sourcesBackend) source(id string) (aiSource, bool) {
	for _, s := range b.sources {
		if s.ID == id {
			return s, true
		}
	}
	return aiSource{}, false
}
func (b *sourcesBackend) resolve(ctx context.Context, model string) (aiSource, string, error) {
	if id, raw, ok := strings.Cut(model, "::"); ok {
		src, found := b.source(id)
		if !found || raw == "" {
			return aiSource{}, "", errors.New("AI source unavailable; select a model from the current list")
		}
		return src, raw, nil
	}
	def, _ := b.source(b.primary)
	if model == "" || model == def.Model {
		return def, def.Model, nil
	}
	// Old sessions may still reference a secondary model without a source ID.
	var matches []aiSource
	for _, src := range b.sources {
		probe, cancel := context.WithTimeout(ctx, 4*time.Second)
		models, err := src.profile().backendConfig().newChatBackend().ListModels(probe)
		cancel()
		if err != nil {
			continue
		}
		for _, m := range models {
			if m == model {
				matches = append(matches, src)
				break
			}
		}
	}
	if len(matches) == 1 {
		return matches[0], model, nil
	}
	if len(matches) > 1 {
		return aiSource{}, "", errors.New("model exists on multiple sources; select a source-qualified model")
	}
	return aiSource{}, "", errors.New("model unavailable; select a model from an AI source")
}
func (b *sourcesBackend) Chat(ctx context.Context, req ollama.ChatRequest, out chan<- ollama.StreamEvent) {
	src, model, err := b.resolve(ctx, req.Model)
	if err != nil {
		out <- ollama.StreamEvent{Err: err}
		return
	}
	req.Model = model
	src.profile().backendConfig().newChatBackend().Chat(ctx, req, out)
}
func (b *sourcesBackend) Ping(ctx context.Context) error {
	src, _ := b.source(b.primary)
	return src.profile().backendConfig().newChatBackend().Ping(ctx)
}
func (b *sourcesBackend) ContextBudgetChars() int {
	n := 0
	for _, src := range b.sources {
		v := src.profile().backendConfig().newChatBackend().ContextBudgetChars()
		if v > 0 && (n == 0 || v < n) {
			n = v
		}
	}
	return n
}
func (b *sourcesBackend) ListModels(ctx context.Context) ([]string, error) {
	type result struct {
		src    aiSource
		models []string
		err    error
	}
	ch := make(chan result, len(b.sources))
	for _, src := range b.sources {
		go func(src aiSource) {
			probe, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			models, err := src.profile().backendConfig().newChatBackend().ListModels(probe)
			ch <- result{src, models, err}
		}(src)
	}
	results := map[string]result{}
	for range b.sources {
		r := <-ch
		results[r.src.ID] = r
	}
	out := []string{}
	seen := map[string]bool{}
	success := false
	for _, src := range b.sources {
		r := results[src.ID]
		if r.err != nil {
			continue
		}
		success = true
		// Configured IDs remain usable even when a provider's catalog is incomplete.
		if src.Model != "" {
			r.models = append(r.models, src.Model)
		}
		for _, m := range r.models {
			id := src.ID + "::" + m
			if src.ID == b.primary && m == src.Model {
				id = m
			}
			if !seen[id] {
				out = append(out, id)
				seen[id] = true
			}
		}
	}
	if !success {
		return nil, errors.New("cannot list models from any configured source")
	}
	return out, nil
}

// Source operations share validation and persistence with the settings form. Credentials are
// obtained from the caller's secret store, never from raw tool arguments.
func (s *Server) aiSourceTool(ctx context.Context, u *memory.User, args map[string]any) (string, error) {
	old, err := loadAIProfile(ctx, s.store())
	if err != nil {
		return "", err
	}
	if old == nil || old.ServerDefaults {
		old = s.environmentAIProfile()
	}
	p := s.withAISources(old)
	p.ServerDefaults = false
	if p.Embedding == nil {
		p.Embedding = s.environmentAIProfile().Embedding
	}
	e := *p.Embedding
	p.Embedding = &e
	credentials := s.store()
	if u != nil {
		credentials = credentials.ConfigScope(fmt.Sprintf("u%d", u.ID))
	}
	id := argStr(args, "source_id")
	index := -1
	for i, src := range p.Sources {
		if src.ID == id {
			index = i
			break
		}
	}
	action := argStr(args, "action")
	requestAction := "save"
	switch action {
	case "ai_source_set":
		if index < 0 {
			if !sourceIDPattern.MatchString(id) {
				return "", errors.New("source_id required: choose a short unique name using letters, digits, - or _")
			}
			p.Sources = append(p.Sources, aiSource{ID: id, Name: id})
			index = len(p.Sources) - 1
		}
		src := &p.Sources[index]
		prior := *src
		if v, ok := args["source_name"].(string); ok {
			src.Name = v
		}
		if v, ok := args["provider"].(string); ok {
			src.Provider = v
			if v != prior.Provider {
				src.BaseURL = ""
			}
		}
		if v, ok := args["base_url"].(string); ok {
			src.BaseURL = v
		}
		if v, ok := args["model"].(string); ok {
			src.Model = v
		}
		cp := src.profile()
		if err := cp.normalize(); err != nil {
			return "", err
		}
		src.Provider, src.BaseURL = cp.Provider, cp.BaseURL
		if src.Provider != prior.Provider || src.BaseURL != prior.BaseURL {
			src.APIKey = ""
		}
		if name := argStr(args, "key_secret"); name != "" {
			if memory.ValidateScriptSecretName(name) != nil {
				return "", errors.New("use a script secret name")
			}
			key, found, err := credentials.GetSecret(ctx, name)
			if err != nil || !found || key == "" {
				return "", errors.New("credential unavailable; use request_secret or the secure settings form")
			}
			src.APIKey = key
		}
		if args["make_default"] == true {
			p.DefaultSource = id
		}
	case "ai_source_remove":
		if index < 0 {
			return "", errors.New("unknown source_id; use ai_get")
		}
		if id == p.DefaultSource {
			return "", errors.New("select a different default source before removing this source")
		}
		p.Sources = append(p.Sources[:index], p.Sources[index+1:]...)
	case "ai_source_default":
		if index < 0 {
			return "", errors.New("unknown source_id; use ai_get")
		}
		p.DefaultSource = id
		if v, ok := args["model"].(string); ok {
			p.Sources[index].Model = v
		}
	case "ai_source_models", "ai_source_test":
		if index < 0 {
			return "", errors.New("unknown source_id; use ai_get")
		}
		if v, ok := args["model"].(string); ok {
			p.Sources[index].Model = v
		}
		requestAction = strings.TrimPrefix(action, "ai_source_")
	default:
		return "", errors.New("unknown source action")
	}
	if err := s.embeddingToolArgs(ctx, credentials, args, p); err != nil {
		return "", err
	}
	p.Embedding.Reindex = args["reindex"] == true
	if err := s.prepareSources(p, old); err != nil {
		return "", err
	}
	if requestAction == "save" {
		if err := s.saveAIProfile(ctx, u, p, old); err != nil {
			return "", err
		}
		raw, _ := json.Marshal(map[string]any{"ok": true, "embeddingPending": s.embeddingPending(*p)})
		return string(raw), nil
	}
	target := p.Sources[index].profile()
	r, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/api/ai/config", nil)
	if err := s.validateAIProfileFor(withUser(r, u), &target); err != nil {
		return "", err
	}
	probe, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	be := target.backendConfig().newChatBackend()
	if requestAction == "models" {
		models, err := be.ListModels(probe)
		if err != nil {
			return "", errors.New("cannot list source models")
		}
		raw, _ := json.Marshal(map[string]any{"models": models})
		return string(raw), nil
	}
	if target.Model == "" {
		return "", errors.New("model required")
	}
	if err := probeAI(probe, be, target.Model); err != nil {
		return "", err
	}
	return `{"ok":true}`, nil
}
func modelInSet(set map[string]bool, model string) bool {
	if set[model] {
		return true
	}
	_, raw, ok := strings.Cut(model, "::")
	return ok && set[raw]
}

func (p *aiProfile) clearTransientFlags() {
	for i := range p.Sources {
		p.Sources[i].ClearKey = false
	}
	if p.Embedding != nil {
		p.Embedding.ClearKey = false
	}
}

func (s *Server) saveAIProfile(ctx context.Context, u *memory.User, p, old *aiProfile) error {
	if p.Model == "" {
		return errors.New("default model required")
	}
	r, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/api/ai/config", nil)
	r = withUser(r, u)
	if err := s.validateAIProfileFor(r, p); err != nil {
		return err
	}
	for _, src := range p.Sources {
		cp := src.profile()
		if err := s.validateAIProfileFor(r, &cp); err != nil {
			return fmt.Errorf("source %s: %w", src.Name, err)
		}
	}
	if err := s.validateEmbeddingSave(ctx, p, old); err != nil {
		return err
	}
	p.ServerDefaults = false
	p.clearTransientFlags()
	raw, _ := json.Marshal(p)
	if err := s.store().SetSecret(ctx, aiProfileSecret, string(raw)); err != nil {
		return errors.New("cannot save AI settings")
	}
	s.scheduleRAGApply()
	return nil
}
