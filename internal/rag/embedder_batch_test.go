package rag

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Slicing must preserve order and count: chunk i's vector must stay with chunk i,
// otherwise every search silently returns the wrong passage.
func TestEmbedBatchProgressSlicesAndKeepsOrder(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body.Input) > EmbedBatchSize {
			t.Errorf("slice of %d exceeds EmbedBatchSize=%d", len(body.Input), EmbedBatchSize)
		}
		// Echo back a 1-dim vector holding the text's first byte, so order is checkable.
		// The real API returns an `index` per entry and embedOpenAI places vectors
		// by it — mirror that, otherwise the fake, not the code, decides the order.
		data := make([]map[string]interface{}, len(body.Input))
		for i, txt := range body.Input {
			data[i] = map[string]interface{}{"index": i, "embedding": []float32{float32(txt[0])}}
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": data})
	}))
	defer srv.Close()

	e := NewOpenAIEmbedder(srv.URL, "", "test-model")
	n := EmbedBatchSize*2 + 7 // forces three slices, last one partial
	texts := make([]string, n)
	for i := range texts {
		texts[i] = string(rune('A'+i%26)) + " chunk"
	}

	var lastDone, lastTotal int
	calls := 0
	out, err := e.EmbedBatchProgress(context.Background(), texts, func(done, total int) {
		calls++
		if done <= lastDone {
			t.Errorf("progress went backwards: %d after %d", done, lastDone)
		}
		lastDone, lastTotal = done, total
	})
	if err != nil {
		t.Fatalf("EmbedBatchProgress: %v", err)
	}
	if len(out) != n {
		t.Fatalf("got %d vectors for %d texts", len(out), n)
	}
	if requests != 3 {
		t.Errorf("expected 3 slices for %d texts (size %d), got %d requests", n, EmbedBatchSize, requests)
	}
	if lastDone != n || lastTotal != n {
		t.Errorf("final progress %d/%d, want %d/%d", lastDone, lastTotal, n, n)
	}
	if calls != 3 {
		t.Errorf("progress called %d times, want once per slice (3)", calls)
	}
	for i := range texts {
		if want := float32(texts[i][0]); out[i][0] != want {
			t.Fatalf("vector %d belongs to another chunk: got %v want %v", i, out[i][0], want)
		}
	}
}

// A document smaller than one slice must still work, and still report progress.
func TestEmbedBatchProgressSingleSlice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		data := make([]map[string]interface{}, len(body.Input))
		for i := range body.Input {
			data[i] = map[string]interface{}{"index": i, "embedding": []float32{1}}
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": data})
	}))
	defer srv.Close()

	e := NewOpenAIEmbedder(srv.URL, "", "m")
	done, total := 0, 0
	out, err := e.EmbedBatchProgress(context.Background(), []string{"a", "b"}, func(d, tt int) { done, total = d, tt })
	if err != nil || len(out) != 2 {
		t.Fatalf("out=%d err=%v", len(out), err)
	}
	if done != 2 || total != 2 {
		t.Errorf("progress %d/%d, want 2/2", done, total)
	}
}
