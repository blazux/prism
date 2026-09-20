package calendar

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type patchTransport func(*http.Request) (*http.Response, error)

func (f patchTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestCloudLocationPatchPreservesUnrelatedFields(t *testing.T) {
	for _, kind := range []string{"google", "microsoft"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			cl := &http.Client{Transport: patchTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				raw := `{"id":"1","summary":"Keep title","description":"Keep text","start":{"dateTime":"2026-09-22T09:00:00Z"},"end":{"dateTime":"2026-09-22T10:30:00Z"}}`
				if kind == "microsoft" {
					raw = `{"id":"1","subject":"Keep title","body":{"contentType":"html","content":"<b>Keep HTML</b>"},"start":{"dateTime":"2026-09-22T09:00:00","timeZone":"UTC"},"end":{"dateTime":"2026-09-22T10:30:00","timeZone":"UTC"}}`
				}
				if r.Method == "PATCH" {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if len(body) != 1 || body["location"] == nil {
						t.Errorf("rewrote unrelated fields: %v", body)
					}
					raw = `{}`
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(raw)), Request: r}, nil
			})}
			var p Provider = &GoogleProvider{client: cl}
			if kind == "microsoft" {
				p = &MicrosoftProvider{client: cl}
			}
			if err := Update(context.Background(), p, "1", Patch{Location: ptr("Room B")}); err != nil {
				t.Fatal(err)
			}
			if calls != 2 {
				t.Fatal(calls)
			}
		})
	}
}
