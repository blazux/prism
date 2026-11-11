package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestEvalCreatesWorkspaceAndOnlyDeletesItsReturnedID(t *testing.T) {
	const owned = "u7-eval-returned-id"
	var actions []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actions = append(actions, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/api/sessions":
			if r.Method != "POST" {
				t.Error("evaluation must create a workspace")
			}
			json.NewEncoder(w).Encode(map[string]string{"id": owned})
		case "/ws":
			if r.URL.Query().Get("session") != owned {
				t.Error("ignored server-owned workspace ID")
			}
			upgrader := websocket.Upgrader{}
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			var request map[string]any
			if err := conn.ReadJSON(&request); err != nil {
				t.Error(err)
				return
			}
			conn.WriteJSON(map[string]string{"type": "turn_complete"})
		default:
			if r.Method != "DELETE" || !strings.HasSuffix(r.URL.Path, owned) {
				t.Error("unexpected cleanup", r.Method, r.URL.Path)
			}
		}
	}))
	defer server.Close()
	c := &client{base: server.URL, http: server.Client(), timeout: time.Second}
	result := c.runTask(Task{Name: "file-check", Prompt: "fixture"}, 0, false)
	if !result.Success {
		t.Fatal(result.Failures)
	}
	if len(actions) != 3 || actions[0] != "POST /api/sessions" || actions[2] != "DELETE /api/sessions/"+owned {
		t.Fatal("evaluation reused/deleted an unrelated workspace", actions)
	}
}

func TestTimeEvaluationAcceptsFrenchAndEnglish(t *testing.T) {
	data, err := os.ReadFile("../../eval/tasks.json")
	if err != nil {
		t.Fatal(err)
	}
	var tasks []Task
	if err := json.Unmarshal(data, &tasks); err != nil {
		t.Fatal(err)
	}
	var expression string
	for _, task := range tasks {
		if task.Name == "answer-not-widget-fact" {
			for _, check := range task.Checks {
				if check.Response && check.Regex != "" {
					expression = check.Regex
				}
			}
		}
	}
	if expression == "" {
		t.Fatal("time evaluation fixture missing")
	}
	pattern := regexp.MustCompile(expression)
	for _, answer := range []string{"22h00 à Tokyo", "22h le même jour", "22:00", "22", "10pm", "10:00 pm"} {
		if !pattern.MatchString(answer) {
			t.Error("valid time refused", answer)
		}
	}
	for _, answer := range []string{"12h00", "22h30", "22:30", "122h00"} {
		if pattern.MatchString(answer) {
			t.Error("wrong time accepted", answer)
		}
	}
}

func TestFileCountEvaluationAcceptsFrenchAndEnglish(t *testing.T) {
	data, err := os.ReadFile("../../eval/tasks.json")
	if err != nil {
		t.Fatal(err)
	}
	var tasks []Task
	if err := json.Unmarshal(data, &tasks); err != nil {
		t.Fatal(err)
	}
	var expression string
	for _, task := range tasks {
		if task.Name == "file-list-count" {
			for _, check := range task.Checks {
				if check.Response && check.Regex != "" {
					expression = check.Regex
				}
			}
		}
	}
	if expression == "" {
		t.Fatal("file count evaluation fixture missing")
	}
	pattern := regexp.MustCompile(expression)
	for _, answer := range []string{"3 fichiers", "three files", "Trois fichiers"} {
		if !pattern.MatchString(answer) {
			t.Error("valid file count refused", answer)
		}
	}
	for _, answer := range []string{"13 fichiers", "thirteen files", "quatre fichiers"} {
		if pattern.MatchString(answer) {
			t.Error("wrong file count accepted", answer)
		}
	}
}
