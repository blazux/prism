package anthropic

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sync/atomic"
)

func TestUnknownCredentialFormatIsNotGuessedToBeSubscriptionToken(t *testing.T) {
	for _, key := range []string{"sk-ant-api03-synthetic", "sk-ant-future-format-synthetic", "arbitrary-gateway-synthetic-key"} {
		if err := ValidateKey(key); err != nil {
			t.Fatalf("unknown or console credential must be validated by its provider, got %v", err)
		}
	}
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Errorf("unexpected model generation or route: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"type":"authentication_error","message":"invalid key"}}`)
	}))
	defer server.Close()
	_, err := NewClient(server.URL, "sk-ant-future-format-synthetic", "fixture").ListModels(t.Context())
	if calls.Load() != 1 || err == nil || !strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "OAuth") {
		t.Fatalf("credential was locally misclassified or provider rejection ignored: calls=%d err=%v", calls.Load(), err)
	}
}
