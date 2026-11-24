package openai

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModelCatalogHTTPFailureDoesNotBecomeAnEmptySuccessfulList(t *testing.T) {
	for _, status := range []int{401, 403, 404, 429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
					t.Errorf("unexpected model generation or route: %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(status)
				fmt.Fprint(w, `{"data":[{"id":"must-not-be-offered"}],"error":"fixture-private-provider-body"}`)
			}))
			defer server.Close()
			models, err := NewClient(server.URL+"/v1", "fixture-private-key").ListModels(t.Context())
			var catalogError *ModelCatalogError
			if len(models) != 0 || !errors.As(err, &catalogError) || catalogError.StatusCode != status || strings.Contains(err.Error(), "fixture-private") {
				t.Fatalf("provider failure was accepted or private response reflected: models=%v err=%v", models, err)
			}
		})
	}
}
