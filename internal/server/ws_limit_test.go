package server

import (
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestChatRejectsOversizedFragmentedMessage(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	s := New(Config{WorkspaceDir: t.TempDir(), PluginDir: t.TempDir(), Model: "fixture", OllamaURL: "http://127.0.0.1:1", AgentContainer: "no-runtime"})
	server := httptest.NewServer(http.HandlerFunc(s.handleWS))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	done := make(chan error, 1)
	go func() {
		writer, err := conn.NextWriter(websocket.TextMessage)
		if err == nil {
			chunk := []byte(strings.Repeat("x", 4096))
			for i := 0; i < 4097; i++ {
				if _, err = writer.Write(chunk); err != nil {
					break
				}
			}
			writer.Close()
		}
		done <- err
	}()
	for {
		_, _, err = conn.ReadMessage()
		if err != nil {
			break
		}
	}
	if !websocket.IsCloseError(err, websocket.CloseMessageTooBig) {
		t.Fatalf("expected size-limit close: %v", err)
	}
	<-done
}
