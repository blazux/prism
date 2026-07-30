package server

import (
	"net/http/httptest"
	"strings"
	"testing"

	"prism/internal/memory"
)

func TestComposeWebhookMessageSubstitutesThePayload(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/webhook/abc", nil)

	got := composeWebhookMessage("Résume ceci en une phrase :\n{{content}}\nSois bref.", "alerte disque plein", r)
	if !strings.Contains(got, "alerte disque plein") || strings.Contains(got, contentPlaceholder) {
		t.Errorf("placeholder not substituted: %q", got)
	}
	if !strings.HasSuffix(strings.TrimSpace(got), "Sois bref.") {
		t.Errorf("text after the placeholder was lost: %q", got)
	}
}

func TestComposeWebhookMessageAppendsWhenThePromptHasNoPlaceholder(t *testing.T) {
	// A prompt written in a hurry must still do something sensible rather than
	// silently drop the sender's payload.
	r := httptest.NewRequest("POST", "/api/webhook/abc", nil)
	got := composeWebhookMessage("Traite cette alerte.", "disque plein", r)
	if !strings.Contains(got, "Traite cette alerte.") || !strings.Contains(got, "disque plein") {
		t.Errorf("expected prompt then payload, got %q", got)
	}
}

func TestComposeWebhookMessagePrettyPrintsJSON(t *testing.T) {
	// Senders post minified JSON; an agent reads an indented object far more
	// reliably than one long line.
	r := httptest.NewRequest("POST", "/api/webhook/abc", nil)
	got := composeWebhookMessage("{{content}}", `{"alert":"disk","pct":91}`, r)
	if !strings.Contains(got, "\n  \"alert\"") {
		t.Errorf("JSON was not pretty-printed: %q", got)
	}
}

func TestComposeWebhookMessageFallsBackToTheQueryStringForGETTriggers(t *testing.T) {
	// A device that can only fire a GET carries its payload in the URL.
	r := httptest.NewRequest("GET", "/api/webhook/abc?event=motion&room=garage", nil)
	got := composeWebhookMessage("Que se passe-t-il ?", "", r)
	if !strings.Contains(got, "event=motion") || !strings.Contains(got, "room=garage") {
		t.Errorf("query string not used as payload: %q", got)
	}
}

func TestComposeWebhookMessageWithNoPromptIsJustThePayload(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/webhook/abc", nil)
	if got := composeWebhookMessage("", "  brut  ", r); got != "brut" {
		t.Errorf("expected the bare payload, got %q", got)
	}
}

func TestWebhookTokenAcceptedFromHeaderBearerOrQuery(t *testing.T) {
	const want = "s3cr3t"

	byHeader := httptest.NewRequest("POST", "/api/webhook/abc", nil)
	byHeader.Header.Set("X-Prism-Token", want)

	byBearer := httptest.NewRequest("POST", "/api/webhook/abc", nil)
	byBearer.Header.Set("Authorization", "Bearer "+want)

	byQuery := httptest.NewRequest("POST", "/api/webhook/abc?token="+want, nil)

	if !webhookTokenOK(byHeader, want) {
		t.Error("token in X-Prism-Token should be accepted")
	}
	if !webhookTokenOK(byBearer, want) {
		t.Error("token as a bearer should be accepted")
	}
	if !webhookTokenOK(byQuery, want) {
		t.Error("token in the query string should be accepted — many devices offer only a URL")
	}
}

func TestWebhookTokenRejectsWrongAndEmpty(t *testing.T) {
	bad := httptest.NewRequest("POST", "/api/webhook/abc?token=nope", nil)
	if webhookTokenOK(bad, "s3cr3t") {
		t.Error("a wrong token must be rejected")
	}
	none := httptest.NewRequest("POST", "/api/webhook/abc", nil)
	if webhookTokenOK(none, "s3cr3t") {
		t.Error("a missing token must be rejected")
	}
	// A webhook row with no token must never authenticate — otherwise a blank
	// column would make the endpoint open to anyone who guesses the id.
	if webhookTokenOK(none, "") {
		t.Error("an empty configured token must never authenticate")
	}
}

func TestWebhookSessionIsolatesAutomatedFeedsByDefault(t *testing.T) {
	if got := webhookSession(memory.WebhookRow{ID: "abc"}); got != "webhook-abc" {
		t.Errorf("expected a dedicated session, got %q", got)
	}
	if got := webhookSession(memory.WebhookRow{ID: "abc", SessionID: "finance"}); got != "finance" {
		t.Errorf("a configured session must win, got %q", got)
	}
}

func TestWebhookPublicPathCoversOnlyTheInboundPrefix(t *testing.T) {
	// The CRUD endpoints must stay behind the dashboard's auth: exempting them
	// would let anyone create a webhook, which is a shell on the agent.
	if !webhookPublicPath("/api/webhook/abc123") {
		t.Error("inbound calls must bypass dashboard auth — senders have no login")
	}
	for _, p := range []string{"/api/webhooks", "/api/webhooks/abc123", "/api/chat", "/api/files"} {
		if webhookPublicPath(p) {
			t.Errorf("%s must NOT be public", p)
		}
	}
}

func TestNewWebhookCredentialsAreUnguessableAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id, tok := newWebhookID(), newWebhookTok()
		if len(id) < 16 || len(tok) < 32 {
			t.Fatalf("credentials too short to be unguessable: id=%q token=%q", id, tok)
		}
		if seen[id] || seen[tok] {
			t.Fatal("collision in generated credentials")
		}
		seen[id], seen[tok] = true, true
	}
}
