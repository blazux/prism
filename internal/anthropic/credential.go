package anthropic

import (
	"fmt"
	"strings"
)

// This backend authenticates with a console API key, and only that.
//
// Driving it from a Claude Pro/Max subscription was implemented here and then
// removed. The OAuth token the Claude Code CLI holds does authenticate, and plain
// chat works on it; tool calls do not. Anthropic classifies a tool-bearing
// request from anything that is not Claude Code as a third-party app and bills it
// against extra usage rather than plan limits — with no such credits the request
// is refused. The refusal is intermittent and nothing avoids it: measured
// 2026-07-29, the same request on the same model was refused seven times running
// and then accepted, across every model offered, at any request size. PRISM is an
// agent, so a brain that drops tool calls at random is worse than no brain: the
// user spends the afternoon suspecting their own config.
//
// NousResearch/hermes-agent#31668 is the same wall from the other side, closed
// with no fix. It is a billing policy, not something a client works around. The
// implementation is in this branch's history if that policy ever changes.

// looksLikeOAuthToken reports whether a credential is a Claude Code OAuth token
// rather than a console API key.
//
// This check earns its keep because the two are nearly indistinguishable:
// sk-ant-api… is a key, sk-ant-oat… is a subscription token, and they differ by
// three characters in the middle. Pasted into ANTHROPIC_API_KEY, the token goes
// out as x-api-key and comes back 401 "invalid x-api-key" — which reads as an
// expired key, not as the wrong kind of credential. The natural response is to
// regenerate the key and get the same 401 forever.
func looksLikeOAuthToken(token string) bool {
	switch {
	case token == "":
		return false
	case strings.HasPrefix(token, "sk-ant-api"): // console API key
		return false
	case strings.HasPrefix(token, "sk-ant-"): // setup-token (sk-ant-oat…), managed key
		return true
	case strings.HasPrefix(token, "eyJ"): // JWT from the OAuth flow
		return true
	case strings.HasPrefix(token, "cc-"): // Claude Code access token
		return true
	default:
		return false
	}
}

// ValidateKey reports whether a credential can drive this backend at all. It runs
// at startup so the problem lands in the logs, and again on the first chat turn
// so it lands in the chat — the two places someone looks. Both callers want a
// warning rather than a fatal: the rest of PRISM (email, calendar, widgets) works
// fine without a working LLM, and killing it over one env var would be worse.
func ValidateKey(key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("ANTHROPIC_API_KEY is not set — the anthropic backend needs an API key from console.anthropic.com")
	}
	if looksLikeOAuthToken(key) {
		return fmt.Errorf("ANTHROPIC_API_KEY looks like a Claude Code OAuth token, not a console API key (sk-ant-api…). " +
			"A Pro/Max subscription token cannot drive PRISM: Anthropic bills third-party tool calls against extra " +
			"usage rather than plan limits, so the agent loop fails on it. Get a key at console.anthropic.com")
	}
	return nil
}
