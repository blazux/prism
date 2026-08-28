package server

import (
	"encoding/json"
	"testing"
)

func TestShareSlug(t *testing.T) {
	cases := map[string]string{
		"Eval Clock":         "eval-clock",
		"  BTC / EUR price ": "btc-eur-price",
		"Déjà vu!!!":         "d-j-vu",
		"":                   "widget",
		"___":                "widget",
		"already-slug":       "already-slug",
	}
	for in, want := range cases {
		if got := shareSlug(in); got != want {
			t.Errorf("shareSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSharedPayloadRoundTrip(t *testing.T) {
	in := sharedPayload{Widgets: []sharedWidget{
		{Title: "A", Content: "<div>a</div>", Cols: 2, Height: 300},
		{Title: "B", Content: "<div>b</div>", Cols: 1, Height: 280},
	}}
	b, _ := json.Marshal(in)
	var out sharedPayload
	if json.Unmarshal(b, &out) != nil || len(out.Widgets) != 2 {
		t.Fatalf("roundtrip failed: %s", b)
	}
	if out.Widgets[0].Title != "A" || out.Widgets[1].Content != "<div>b</div>" {
		t.Errorf("payload corrupted: %+v", out)
	}
}
