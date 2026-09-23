package ollama

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProviderUsageUnknownVersusZero(t *testing.T) {
	for _, counts := range []string{"", `,"prompt_eval_count":0,"eval_count":0`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"done":true,"done_reason":"stop"%s}`+"\n", counts)
		}))
		ch := make(chan StreamEvent, 10)
		NewClient(srv.URL).Chat(t.Context(), ChatRequest{Model: "fixture"}, ch)
		close(ch)
		srv.Close()
		for ev := range ch {
			if ev.Err != nil {
				t.Fatal(ev.Err)
			}
			if counts == "" && ev.Usage != nil {
				t.Fatal("fabricated zero usage")
			}
			if counts != "" && (ev.Usage == nil || ev.Usage.InputTokens == nil || *ev.Usage.InputTokens != 0) {
				t.Fatal("lost reported zero")
			}
		}
	}
}
