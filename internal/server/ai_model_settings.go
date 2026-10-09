package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"prism/internal/memory"
	"prism/internal/ollama"
	"strings"
)

func validateModelSettings(settings map[string]ollama.ModelCapabilities) error {
	if len(settings) > 100 {
		return errors.New("maximum 100 model overrides per source")
	}
	for model, c := range settings {
		if strings.TrimSpace(model) == "" || len(model) > 200 || strings.ContainsAny(model, "\r\n") {
			return errors.New("invalid model ID")
		}
		if c.ContextWindow != 0 && (c.ContextWindow < 4096 || c.ContextWindow > 2_000_000) {
			return errors.New("context window must be between 4096 and 2000000 tokens")
		}
	}
	return nil
}

// Uses the same validation/persistence as the compact settings dialog.
func (s *Server) aiModelSettingsTool(ctx context.Context, u *memory.User, args map[string]any) (string, error) {
	p, err := loadAIProfile(ctx, s.store())
	if err != nil {
		return "", err
	}
	if p == nil || p.ServerDefaults {
		p = s.environmentAIProfile()
	}
	p = s.withAISources(p)
	sourceID, model := argStr(args, "source_id"), argStr(args, "model")
	if model == "" || !s.userCanUseModel(ctx, u, model) {
		return "", errors.New("choose an allowed model")
	}
	index := -1
	for i, src := range p.Sources {
		if src.ID == sourceID {
			index = i
			break
		}
	}
	if index < 0 {
		return "", errors.New("source_id must identify an existing AI source from ai_get")
	}
	src := &p.Sources[index]
	settings := src.ModelSettings[model]
	if argStr(args, "action") == "ai_model_get" {
		var detected ollama.ModelCapabilities
		if provider, ok := src.profile().backendConfig().newChatBackend().(ollama.CapabilitiesProvider); ok {
			detected, _ = provider.ModelCapabilities(ctx, model)
		}
		raw, _ := json.Marshal(map[string]any{"source_id": sourceID, "model": model, "detected": detected, "settings": settings})
		return string(raw), nil
	}
	if n, ok := args["context_window"]; ok {
		switch v := n.(type) {
		case float64:
			if v != float64(int(v)) {
				return "", errors.New("context_window must be an integer")
			}
			settings.ContextWindow = int(v)
		case int:
			settings.ContextWindow = v
		default:
			return "", errors.New("context_window must be an integer")
		}
	}
	if v, ok := args["model_vision"].(string); ok {
		switch v {
		case "auto":
			settings.Vision = nil
		case "yes", "no":
			vision := v == "yes"
			settings.Vision = &vision
		default:
			return "", errors.New("model_vision must be auto, yes or no")
		}
	}
	if err := validateModelSettings(map[string]ollama.ModelCapabilities{model: settings}); err != nil {
		return "", err
	}
	old := *p
	next := make(map[string]ollama.ModelCapabilities, len(src.ModelSettings)+1)
	for k, v := range src.ModelSettings {
		next[k] = v
	}
	if settings.ContextWindow == 0 && settings.Vision == nil {
		delete(next, model)
	} else {
		next[model] = settings
	}
	src.ModelSettings = next
	if err := s.saveAIProfileMode(ctx, u, p, &old, false); err != nil {
		return "", err
	}
	return fmt.Sprintf("Model settings saved for %s on %s. Applies next message; default model and embeddings unchanged.", model, sourceID), nil
}

func (b *sourcesBackend) ModelCapabilities(ctx context.Context, model string) (ollama.ModelCapabilities, error) {
	src, id, err := b.resolve(ctx, model)
	if err != nil {
		return ollama.ModelCapabilities{}, err
	}
	override := src.ModelSettings[id]
	if override.ContextWindow > 0 && override.Vision != nil {
		return override, nil
	}
	var info ollama.ModelCapabilities
	if provider, ok := src.profile().backendConfig().newChatBackend().(ollama.CapabilitiesProvider); ok {
		info, _ = provider.ModelCapabilities(ctx, id)
	}
	if src.Provider == "ollama" && info.ContextWindow == 0 {
		info.ContextWindow = ollama.NumCtx
	}
	if override.ContextWindow > 0 {
		info.ContextWindow = override.ContextWindow
	}
	if override.Vision != nil {
		info.Vision = override.Vision
	}
	return info, nil
}

// These actions operate only on an already-saved source. They never change its
// default model, connection, credential, or embedding configuration.
func (s *Server) handleModelSettings(w http.ResponseWriter, r *http.Request, p *aiProfile, action, sourceID, model string, settings ollama.ModelCapabilities) {
	p = s.withAISources(p)
	if model == "" || len(model) > 200 || strings.ContainsAny(model, "\r\n") || !s.userCanUseModel(r.Context(), currentUser(r), model) {
		writeErr(w, 400, "choose an allowed model")
		return
	}
	index := -1
	for i, src := range p.Sources {
		if src.ID == sourceID {
			index = i
			break
		}
	}
	if index < 0 {
		writeErr(w, 400, "save this source before configuring its models")
		return
	}
	if action == "model_info" {
		src := p.Sources[index]
		var detected ollama.ModelCapabilities
		if provider, ok := src.profile().backendConfig().newChatBackend().(ollama.CapabilitiesProvider); ok {
			detected, _ = provider.ModelCapabilities(r.Context(), model)
		}
		if src.Provider == "ollama" && detected.ContextWindow == 0 {
			detected.ContextWindow = ollama.NumCtx
		}
		writeJSON(w, map[string]any{"detected": detected, "settings": src.ModelSettings[model], "fallbackVision": p.backendConfig().cfg.ChatVision})
		return
	}
	if err := validateModelSettings(map[string]ollama.ModelCapabilities{model: settings}); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	old := *p
	src := &p.Sources[index]
	next := make(map[string]ollama.ModelCapabilities, len(src.ModelSettings)+1)
	for k, v := range src.ModelSettings {
		next[k] = v
	}
	if settings.ContextWindow == 0 && settings.Vision == nil {
		delete(next, model)
	} else {
		next[model] = settings
	}
	src.ModelSettings = next
	if err := s.saveAIProfileMode(r.Context(), currentUser(r), p, &old, false); err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
