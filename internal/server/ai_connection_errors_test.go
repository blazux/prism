package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"prism/internal/anthropic"
	"prism/internal/memory"
	"prism/internal/openai"
)

func TestConnectionErrorsAreActionableWithoutReflectingPrivateData(t *testing.T) {
	for _, tc := range []struct {
		err          error
		code, detail string
	}{
		{context.DeadlineExceeded, "connection_timeout", "in time"},
		{context.Canceled, "connection_cancelled", "cancelled"},
		{errors.New("ANTHROPIC_API_KEY looks like a Claude Code OAuth token: fixture-private"), "unsupported_subscription_token", "Claude Console"},
		{errors.New("ANTHROPIC_API_KEY is not set: fixture-private"), "missing_api_key", "Enter"},
		{errors.New("anthropic rejected the API key (401): fixture-private"), "authentication_rejected", "401"},
		{errors.New("anthropic returned 503: fixture-private"), "provider_unavailable", "503"},
		{errors.New("ollama returned 404"), "catalog_not_found", "API path"},
		{&openai.ModelCatalogError{StatusCode: 403}, "catalog_access_denied", "permissions"},
		{&openai.ModelCatalogError{StatusCode: 402}, "provider_billing_required", "credits"},
		{&openai.ModelCatalogError{StatusCode: 429}, "provider_limit", "limits"},
		{&openai.ModelCatalogError{StatusCode: 400}, "catalog_request_rejected", "provider type"},
		{&tls.CertificateVerificationError{Err: errors.New("fixture-private")}, "certificate_error", "certificate"},
		{&url.Error{Op: "Get", URL: "https://fixture-private.invalid", Err: &net.DNSError{Err: "no such host", Name: "fixture-private.invalid"}}, "server_unreachable", "DNS"},
		{io.ErrUnexpectedEOF, "invalid_model_catalog", "API path"},
		{errors.New("provider said fixture-private with 401 in arbitrary text"), "catalog_unavailable", "manually"},
	} {
		t.Run(tc.code+"/"+tc.detail, func(t *testing.T) {
			code, message := modelCatalogFailure(tc.err)
			if code != tc.code || !strings.Contains(message, tc.detail) || strings.Contains(message, "fixture-private") {
				t.Fatalf("unsafe or misleading connection error: code=%s message=%s", code, message)
			}
		})
	}
}

func TestExplicitConnectionCheckDoesNotClaimAnthropicFallbackIsReachable(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Errorf("unexpected generation or route: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"error":"fixture-private-provider-body"}`)
	}))
	defer server.Close()
	backend := anthropic.NewClient(server.URL, "sk-ant-api03-synthetic", "fixture")
	if models, err := backend.ListModels(t.Context()); err != nil || len(models) == 0 {
		t.Fatalf("ordinary picker fallback unexpectedly removed: models=%v err=%v", models, err)
	}
	models, err := verifiedModelCatalog(t.Context(), "anthropic", backend)
	code, message := modelCatalogFailure(err)
	if err == nil || len(models) != 0 || calls.Load() != 2 || code != "provider_unavailable" || strings.Contains(message, "fixture-private") {
		t.Fatalf("explicit test accepted unavailable provider: models=%v code=%s message=%s calls=%d", models, code, message, calls.Load())
	}
}

func TestExplicitConnectionCheckReportsForbiddenAnthropicCatalog(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet {
			t.Errorf("model catalog check attempted generation: %s", r.Method)
		}
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":"fixture-private-provider-body"}`)
	}))
	defer server.Close()
	backend := anthropic.NewClient(server.URL, "sk-ant-api03-synthetic", "fixture")
	models, err := verifiedModelCatalog(t.Context(), "anthropic", backend)
	if err == nil || len(models) > 0 || calls.Load() != 1 {
		t.Fatalf("forbidden catalog claimed reachable: models=%v error=%v calls=%d", models, err, calls.Load())
	}
	code, message := modelCatalogFailure(err)
	if code != "catalog_access_denied" || !strings.Contains(message, "manually") || strings.Contains(message, "fixture-private") {
		t.Fatalf("forbidden catalog did not give safe manual path: code=%s message=%s", code, message)
	}
	if models, err := backend.ListModels(t.Context()); err != nil || len(models) == 0 {
		t.Fatal("ordinary picker lost its historical fallback")
	}
}

func TestGlobalSaveCanRemoveDefaultCredentialAndKeepConnection(t *testing.T) {
	store := securityStore(t)
	var requests atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		t.Errorf("save/removal must not contact provider: %s %s", r.Method, r.URL.Path)
		http.Error(w, "unexpected", 500)
	}))
	defer upstream.Close()
	s := &Server{memStore: store, cfg: Config{LLMBackend: "anthropic", AnthropicModel: "fixture", AnthropicBaseURL: upstream.URL, Model: "fixture"}}
	call := func(body map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := withUser(httptest.NewRequest("POST", "/api/ai/config", bytes.NewReader(raw)), serviceUser)
		w := httptest.NewRecorder()
		s.handleAIProfile(w, r)
		return w
	}
	profile := func(key string, clear bool) map[string]any {
		return map[string]any{"action": "save", "defaultSource": "test", "sources": []map[string]any{{"id": "test", "name": "Fixture", "provider": "anthropic", "baseURL": upstream.URL, "model": "fixture", "apiKey": key, "clearKey": clear}}, "embedding": map[string]any{"useSameProvider": false, "provider": "ollama", "baseURL": "http://unused.invalid", "model": ""}}
	}
	if w := call(profile("sk-ant-api03-synthetic", false)); w.Code != 200 {
		t.Fatalf("initial save: %d %s", w.Code, w.Body.String())
	}
	if w := call(profile("", true)); w.Code != 200 {
		t.Fatalf("credential removal: %d %s", w.Code, w.Body.String())
	}
	if w := call(profile("", false)); w.Code != 200 {
		t.Fatalf("later save incorrectly required deleted credential: %d %s", w.Code, w.Body.String())
	}
	p, err := loadAIProfile(t.Context(), store)
	if err != nil || p.APIKey != "" || len(p.Sources) != 1 || p.Sources[0].APIKey != "" || p.Sources[0].Model != "fixture" {
		t.Fatalf("key removal lost connection or retained credential: err=%v", err)
	}
	probe := profile("", false)
	probe["action"], probe["sourceID"] = "models", "test"
	if w := call(probe); w.Code != 400 || !strings.Contains(w.Body.String(), "API key required") {
		t.Fatalf("disconnected source was probed or unclear: %d %s", w.Code, w.Body.String())
	}
	if requests.Load() != 0 {
		t.Fatalf("configuration-only operation called provider %d times", requests.Load())
	}
	visible, _ := json.Marshal(s.aiPublicView(p, "configured"))
	if strings.Contains(string(visible), "sk-ant-api03-synthetic") || !strings.Contains(string(visible), `"keyConfigured":false`) {
		t.Fatal("public view leaked credential or did not reflect disconnected state")
	}
	// Scope checks still apply even when no credential is required to save.
	other := &Server{memStore: store, cfg: Config{MultiUser: true}}
	r := withUser(httptest.NewRequest("POST", "/api/ai/config", nil), &memory.User{ID: 7, Role: memory.RoleMember})
	if err := other.validateAIProfilePolicyFor(r, p); err == nil {
		t.Fatal("disconnected save bypassed administrator boundary")
	}
}

func TestConnectionCheckUsesDraftSelectedSourceAndDoesNotSaveIt(t *testing.T) {
	store := securityStore(t)
	var calls atomic.Int64
	var upstreamStatus atomic.Int64
	upstreamStatus.Store(200)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" || r.Header.Get("x-api-key") != "sk-ant-api03-synthetic-selected" {
			t.Errorf("verification used wrong source, credential or generated a response: %s %s", r.Method, r.URL.Path)
		}
		status := int(upstreamStatus.Load())
		w.WriteHeader(status)
		if status != 200 {
			fmt.Fprint(w, `{"error":"fixture-private-provider-body"}`)
			return
		}
		fmt.Fprint(w, `{"data":[{"id":"fixture-selected"}]}`)
	}))
	defer upstream.Close()
	s := &Server{memStore: store}
	body := map[string]any{
		"action": "models", "sourceID": "selected", "defaultSource": "default",
		"sources": []map[string]any{
			{"id": "default", "name": "Default", "provider": "other", "baseURL": "http://127.0.0.1:1/v1", "model": "fixture-default", "apiKey": "fixture-private-default-key"},
			{"id": "selected", "name": "Selected", "provider": "anthropic", "baseURL": upstream.URL, "model": "fixture-selected", "apiKey": "sk-ant-api03-synthetic-selected"},
		},
	}
	for _, status := range []int{200, 401, 403, 503} {
		upstreamStatus.Store(int64(status))
		raw, _ := json.Marshal(body)
		r := withUser(httptest.NewRequest("POST", "/api/ai/config", bytes.NewReader(raw)), serviceUser)
		w := httptest.NewRecorder()
		s.handleAIProfile(w, r)
		if status == 200 {
			if w.Code != 200 || !strings.Contains(w.Body.String(), "fixture-selected") {
				t.Fatalf("draft selected source was not checked: %d %s", w.Code, w.Body.String())
			}
		} else if w.Code != 502 || !strings.Contains(w.Body.String(), fmt.Sprint(status)) || strings.Contains(w.Body.String(), "fixture-private") {
			t.Fatalf("provider failure not reported safely: %d %s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "sk-ant-") {
			t.Fatal("credential reached browser connection result")
		}
	}
	if calls.Load() != 4 {
		t.Fatalf("verification made extra catalog or generation calls: %d", calls.Load())
	}
	if p, err := loadAIProfile(t.Context(), store); err != nil || p != nil {
		t.Fatal("checking a draft saved it")
	}
}
