package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVoiceInferenceReusesSourceWithoutExportingCredentials(t *testing.T) {
	var forwarded map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer supplier-key-fixture" {
			t.Error("source credential was not applied")
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected source route %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&forwarded); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Bonjour.\"},\"finish_reason\":null}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_one\",\"type\":\"function\",\"function\":{\"name\":\"search_knowledge_base\",\"arguments\":\"{\\\"query\\\":\\\"horaires\\\"}\"}}]},\"finish_reason\":null}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	s := New(Config{AISources: []aiSource{{ID: "voice", Provider: "openai", BaseURL: upstream.URL + "/v1", APIKey: "supplier-key-fixture", Model: "phone"}}, AIDefaultSource: "voice", Model: "phone"})
	s.voxState.Store(&voxConnection{Enabled: true, Model: "voice::phone", GatewayToken: "phone-gateway-fixture"})
	for _, stream := range []bool{false, true} {
		body := fmt.Sprintf(`{"model":"voice::phone","messages":[{"role":"system","content":"Reception instructions"},{"role":"user","content":"Quels horaires ?"}],"stream":%v,"reasoning_effort":"low","chat_template_kwargs":{"enable_thinking":true},"temperature":0.6,"top_p":0.95,"top_k":20,"max_tokens":384,"tools":[{"type":"function","function":{"name":"search_knowledge_base","parameters":{"type":"object","properties":{"query":{"type":"string"},"max_visits":{"type":"integer","enum":[1,2]},"filters":{"type":"object","properties":{"city":{"type":"string"}}}},"required":["query"],"additionalProperties":false}}}]}`, stream)
		r := withUser(httptest.NewRequest("POST", "/api/voice/inference/v1/chat/completions", strings.NewReader(body)), serviceUser)
		r.Header.Set("Authorization", "Bearer phone-gateway-fixture")
		w := httptest.NewRecorder()
		s.handleVoiceInference(w, r)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Bonjour.") || !strings.Contains(w.Body.String(), "tool_calls") {
			t.Fatalf("stream=%v code=%d %s", stream, w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), "supplier-key-fixture") {
			t.Fatal("provider secret exported")
		}
		if forwarded["model"] != "phone" || forwarded["max_tokens"] != float64(384) || forwarded["top_p"] != 0.95 || forwarded["top_k"] != float64(20) {
			t.Fatalf("generation options/source lost: %#v", forwarded)
		}
		if forwarded["reasoning_effort"] != "low" {
			t.Fatal("telephone reasoning effort was lost")
		}
		if kwargs, ok := forwarded["chat_template_kwargs"].(map[string]any); !ok || kwargs["enable_thinking"] != true {
			t.Fatal("telephone thinking request was changed")
		}
		tools := forwarded["tools"].([]any)
		params := tools[0].(map[string]any)["function"].(map[string]any)["parameters"].(map[string]any)
		if values := params["properties"].(map[string]any)["max_visits"].(map[string]any)["enum"].([]any); len(values) != 2 || values[0] != float64(1) {
			t.Fatal("numeric tool enum was lost")
		}
		if params["additionalProperties"] != false || params["properties"].(map[string]any)["filters"].(map[string]any)["properties"] == nil {
			t.Fatal("nested tool schema was damaged")
		}
		if stream && !strings.Contains(w.Body.String(), "data: [DONE]") {
			t.Fatal("missing terminal event")
		}
	}
	r := withUser(httptest.NewRequest("POST", "/api/voice/inference/v1/chat/completions", strings.NewReader(`{"model":"other::expensive","messages":[{"role":"user","content":"test"}]}`)), serviceUser)
	r.Header.Set("Authorization", "Bearer phone-gateway-fixture")
	w := httptest.NewRecorder()
	s.handleVoiceInference(w, r)
	if w.Code != 403 {
		t.Fatal("gateway selected an unapproved model")
	}
	s.voxState.Store(&voxConnection{Enabled: true, Model: "default", PreviousModels: []string{"voice::phone"}, GatewayToken: "phone-gateway-fixture"})
	r = withUser(httptest.NewRequest("POST", "/api/voice/inference/v1/chat/completions", strings.NewReader(`{"model":"voice::phone","messages":[{"role":"user","content":"Continue the current call"}]}`)), serviceUser)
	r.Header.Set("Authorization", "Bearer phone-gateway-fixture")
	w = httptest.NewRecorder()
	s.handleVoiceInference(w, r)
	if w.Code != 200 {
		t.Fatalf("changing the phone model interrupted an already-approved call: %d %s", w.Code, w.Body)
	}
}

func TestVoiceInferenceUpstreamErrorReturnsOneValidJSONResponse(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		fmt.Fprint(w, `{"error":{"message":"invalid model"}}`)
	}))
	defer provider.Close()
	s := New(Config{LLMBackend: "openai", OpenAIBaseURL: provider.URL, Model: "fixture"})
	r := withUser(httptest.NewRequest("POST", "/api/voice/inference/v1/chat/completions", strings.NewReader(`{"model":"default","messages":[{"role":"user","content":"test"}]}`)), serviceUser)
	w := httptest.NewRecorder()
	s.handleVoiceInference(w, r)
	if w.Code != 502 || !json.Valid(w.Body.Bytes()) {
		t.Fatalf("bad error contract: %d %s", w.Code, w.Body)
	}
}

func TestVoiceInferenceIncompleteGenerationIsNotReportedAsComplete(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Unfinished\"},\"finish_reason\":null}]}\n\n")
	}))
	defer upstream.Close()
	s := New(Config{LLMBackend: "openai", OpenAIBaseURL: upstream.URL, Model: "fixture"})
	for _, stream := range []bool{false, true} {
		r := withUser(httptest.NewRequest("POST", "/api/voice/inference/v1/chat/completions", strings.NewReader(fmt.Sprintf(`{"model":"default","stream":%v,"messages":[{"role":"user","content":"test"}]}`, stream))), serviceUser)
		w := httptest.NewRecorder()
		s.handleVoiceInference(w, r)
		if !stream && w.Code != 502 {
			t.Fatalf("incomplete nonstream succeeded: %d %s", w.Code, w.Body)
		}
		if stream && (strings.Contains(w.Body.String(), "[DONE]") || !strings.Contains(w.Body.String(), "error")) {
			t.Fatalf("incomplete stream was marked complete: %s", w.Body)
		}
	}
}

func TestVoiceInferenceUsesTheNativeOllamaAdapter(t *testing.T) {
	var forwarded map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/show" {
			fmt.Fprint(w, `{"capabilities":["completion"]}`)
			return
		}
		if r.URL.Path != "/api/chat" {
			t.Errorf("unexpected native route: %s", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&forwarded)
		fmt.Fprintln(w, `{"message":{"role":"assistant","content":"Hello."},"done":false}`)
		fmt.Fprintln(w, `{"message":{"role":"assistant","content":""},"done":true,"done_reason":"stop"}`)
	}))
	defer upstream.Close()
	s := New(Config{LLMBackend: "ollama", OllamaURL: upstream.URL, Model: "tiny"})
	r := withUser(httptest.NewRequest("POST", "/api/voice/inference/v1/chat/completions", strings.NewReader(`{"model":"default","messages":[{"role":"user","content":"hello"}],"top_p":0.9,"top_k":10,"max_tokens":64}`)), serviceUser)
	w := httptest.NewRecorder()
	s.handleVoiceInference(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Hello.") {
		t.Fatalf("native adapter: %d %s", w.Code, w.Body)
	}
	options := forwarded["options"].(map[string]any)
	if forwarded["model"] != "tiny" || options["top_p"] != 0.9 || options["top_k"] != float64(10) {
		t.Fatalf("native sampling lost: %#v", forwarded)
	}
}
