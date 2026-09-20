package tasks

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTodoistV1PaginationAndPartialUpdate(t *testing.T) {
	gets, posts := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			gets++
			if r.URL.Query().Get("cursor") == "" {
				w.Write([]byte(`{"results":[{"id":"1","content":"One","priority":4,"due":{"date":"2026-09-22T09:30:00Z"}}],"next_cursor":"next page"}`))
			} else {
				if r.URL.Query().Get("cursor") != "next page" {
					t.Error("wrong cursor")
				}
				w.Write([]byte(`{"results":[{"id":"2","content":"Two","priority":1}],"next_cursor":null}`))
			}
			return
		}
		posts++
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if posts == 1 {
			if len(body) != 1 || body["content"] != "Edited" {
				t.Errorf("overwrote omitted fields: %v", body)
			}
		} else {
			if len(body) != 2 || body["due_string"] != "no date" || body["due_lang"] != "en" {
				t.Errorf("due clear: %v", body)
			}
		}
		w.Write([]byte(`{"id":"1"}`))
	}))
	defer srv.Close()
	p := &TodoistProvider{baseURL: srv.URL, token: "test"}
	items, err := p.List(context.Background(), false)
	if err != nil || len(items) != 2 || gets != 2 || items[0].DueAt == nil {
		t.Fatalf("pagination: %+v %v", items, err)
	}
	if err = p.Update(context.Background(), "1", Patch{Title: textPtr("Edited")}); err != nil {
		t.Fatal(err)
	}
	if err = p.Update(context.Background(), "1", Patch{Due: textPtr("")}); err != nil {
		t.Fatal(err)
	}
	if posts != 2 {
		t.Fatal(posts)
	}
}
