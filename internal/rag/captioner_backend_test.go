package rag

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"prism/internal/openai"
)

func TestBackendCaptionerSendsImageAndHidesProviderErrors(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(raw), "image_url") || !strings.Contains(string(raw), "vision-fixture") {
					t.Error("image or model not sent")
				}
				if fail {
					http.Error(w, "sensitive-upstream-error", 400)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Visible widget\"}}]}\n\ndata: [DONE]\n\n")
			}))
			defer upstream.Close()
			path := filepath.Join(t.TempDir(), "image.png")
			os.WriteFile(path, []byte("test-image"), 0600)
			captioner := NewBackendCaptioner(openai.NewClient(upstream.URL, "fixture-key"), "vision-fixture")
			got, err := captioner.DescribeWidget(context.Background(), path)
			if fail {
				if err == nil || strings.Contains(err.Error(), "sensitive") {
					t.Fatal("upstream error exposed", err)
				}
			} else if err != nil || got != "Visible widget" {
				t.Fatal(got, err)
			}
		})
	}
}
