package openai

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"prism/internal/ollama"
)

// Compatible gateways have their own parameter contracts. Native policy must
// never be inferred from a model name or applied to a gateway/path alias.
func (c *Client) officialOpenAI() bool {
	u, err := url.Parse(c.baseURL)
	return err == nil && u.Scheme == "https" && strings.EqualFold(u.Hostname(), "api.openai.com") &&
		(u.Port() == "" || u.Port() == "443") && u.Path == "/v1" && u.RawPath == "" &&
		u.User == nil && u.Opaque == "" && u.RawQuery == "" && !u.ForceQuery && !strings.Contains(c.baseURL, "#")
}

type nativeModelPolicy struct {
	efforts          []string // empty means no known configurable reasoning
	defaultEffort    string
	completionBudget bool
	samplingAtNone   bool
	automaticRouting bool
	responsesTools   bool
	noSampling       bool
}

// Keep capability decisions narrow: a new model name is not evidence that it
// accepts another model's effort values. Date snapshots inherit the named
// family's inference contract; cache retention uses a separate exact allowlist.
func nativePolicy(model string) nativeModelPolicy {
	// These exact models document Responses-only function calling. Do not apply
	// the restriction to other GPT-6 variants or hypothetical model names.
	switch model {
	case "gpt-6-astra", "gpt-6.1-sol", "gpt-6.2-astra", "gpt-6.2-sol":
		return nativeModelPolicy{efforts: []string{"low", "medium", "high", "xhigh", "max"}, defaultEffort: "medium", completionBudget: true, automaticRouting: true, responsesTools: true}
	}
	name := model
	if len(name) > 11 && name[len(name)-11] == '-' {
		if _, err := time.Parse("2006-01-02", name[len(name)-10:]); err == nil {
			name = name[:len(name)-11]
		}
	}
	switch name {
	case "gpt-5", "gpt-5-mini", "gpt-5-nano":
		return nativeModelPolicy{efforts: []string{"minimal", "low", "medium", "high"}, completionBudget: true}
	case "gpt-5.1":
		return nativeModelPolicy{efforts: []string{"none", "low", "medium", "high"}, defaultEffort: "none", completionBudget: true, samplingAtNone: true}
	case "gpt-5.2", "gpt-5.4", "gpt-5.4-mini", "gpt-5.4-nano":
		return nativeModelPolicy{efforts: []string{"none", "low", "medium", "high", "xhigh"}, defaultEffort: "none", completionBudget: true, samplingAtNone: true}
	case "gpt-5.5":
		return nativeModelPolicy{efforts: []string{"none", "low", "medium", "high", "xhigh"}, defaultEffort: "medium", completionBudget: true, samplingAtNone: true}
	case "gpt-5.6", "gpt-5.6-sol":
		return nativeModelPolicy{efforts: []string{"none", "low", "medium", "high", "xhigh", "max"}, completionBudget: true, automaticRouting: true}
	case "o1", "o3", "o3-mini", "o4-mini":
		return nativeModelPolicy{efforts: []string{"low", "medium", "high"}, completionBudget: true}
	case "o1-preview", "o1-mini":
		return nativeModelPolicy{completionBudget: true, noSampling: true}
	case "gpt-5-chat-latest", "gpt-5.1-chat-latest", "gpt-5.2-chat-latest":
		return nativeModelPolicy{completionBudget: true}
	}
	return nativeModelPolicy{}
}

// Official API contracts are independent of EnableThinking being present:
// compaction and background callers use NoThinking with a nil boolean too.
// Model documentation: https://developers.openai.com/api/docs/models
func configureOfficialRequest(payload *chatRequest, req ollama.ChatRequest) error {
	policy := nativePolicy(req.Model)
	if policy.responsesTools && len(req.Tools) > 0 {
		return fmt.Errorf("OpenAI model %q requires the Responses API for tool calling; this connection uses Chat Completions", req.Model)
	}
	if policy.completionBudget {
		payload.MaxCompletionTokens, payload.MaxTokens = payload.MaxTokens, 0
	}
	if policy.noSampling {
		payload.Temperature = 0
		payload.TopP = nil
	}
	if len(policy.efforts) == 0 {
		// Non-reasoning and unknown models retain ordinary sampling. In particular,
		// the compatible-server default "medium" must not leak into GPT-4 calls.
		return nil
	}
	disabled := req.NoThinking || (req.EnableThinking != nil && !*req.EnableThinking)
	effort := req.ReasoningEffort
	if disabled || effort == "none" {
		// Earlier reasoning models cannot disable reasoning. Use their documented
		// lowest effort (minimal for GPT-5, low for o-series), never invent none.
		effort = policy.efforts[0]
	} else if effort == "" {
		effort = reasoningEffort
	}
	if effort != "" && !slices.Contains(policy.efforts, effort) {
		if req.NoThinkingIfUnsupported {
			effort = policy.efforts[0]
		} else {
			return fmt.Errorf("reasoning effort %q is not supported by OpenAI model %q", effort, req.Model)
		}
	}
	payload.ReasoningEffort = effort
	samplingEffort := effort
	if samplingEffort == "" {
		samplingEffort = policy.defaultEffort
	}
	if !policy.samplingAtNone || samplingEffort != "none" {
		payload.Temperature = 0
		payload.TopP = nil
	}
	return nil
}
