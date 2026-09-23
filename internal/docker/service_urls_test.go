package docker

import (
	"fmt"
	"strings"
	"testing"
)

func TestComposePublishedServiceURLs(t *testing.T) {
	row := `{"Name":"app","Publishers":[{"PublishedPort":20000,"Protocol":"tcp"},{"PublishedPort":20000,"Protocol":"tcp"},{"PublishedPort":53,"Protocol":"udp"}]}`
	resolve := func(port int) string { return fmt.Sprintf("https://service-%d.example/", port) }
	for _, raw := range []string{row, "[" + row + "]"} {
		got := composeURLs(raw, resolve)
		if strings.Count(got, "https://service-20000.example/") != 1 || strings.Contains(got, "service-53.example") {
			t.Fatal(got)
		}
	}
	if got := WithExecution(nil).ServiceURL(20000); got != "/proxy/20000/" {
		t.Fatal("local default changed", got)
	}
}
