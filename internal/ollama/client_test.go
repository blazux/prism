package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The generation cap is the only guard against a model that loops instead of
// emitting a stop token: Ollama's own default is -1 — generate until the context
// is full — so a request that carries no num_predict can stream for as long as
// that takes, with the caller blocked and nothing to log. Assert on the wire, not
// on the struct: the cap is only worth anything if it actually reaches Ollama.
func TestChat_SendsGenerationCap(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		want int
	}{
		{"default applies when the caller sets none", Options{}, DefaultNumPredict},
		{"an explicit cap is left alone", Options{NumPredict: 64}, 64},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got ChatRequest
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Errorf("decode request: %v", err)
				}
				w.Write([]byte(`{"done":true}` + "\n"))
			}))
			defer srv.Close()

			ch := make(chan StreamEvent, 8)
			go func() {
				NewClient(srv.URL).Chat(context.Background(), ChatRequest{Model: "m", Options: tc.opts}, ch)
				close(ch)
			}()
			for range ch { // drain
			}

			if got.Options.NumPredict != tc.want {
				t.Errorf("num_predict on the wire = %d, want %d", got.Options.NumPredict, tc.want)
			}
		})
	}
}
