package server

// Interactive terminal: a WebSocket that attaches a PTY to `docker exec -it` in
// the agent's workspace container and streams it both ways (for xterm.js in the
// browser). Binary WS messages are raw keystrokes; text messages are control
// JSON (currently just {type:"resize",cols,rows}). The global middleware only
// proves the caller is signed in; a shell in the tools container is an admin
// capability, so this handler checks the role itself.

import (
	"encoding/json"
	"log"
	"net/http"
	"os/exec"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	// Refuse before upgrading: once the socket is open the browser only sees it
	// close, with no status code to explain why.
	if !s.requireAdminUser(w, r) {
		return
	}
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	container := s.cfg.AgentContainer
	if container == "" {
		container = "prism-workspace"
	}
	// Prefer an interactive bash, fall back to sh. NB: don't redirect bash's
	// stderr — its prompt and readline echo are written there, so 2>/dev/null
	// would silently swallow the prompt and the characters you type.
	cmd := exec.Command("docker", "exec", "-it", container, "sh", "-c",
		"if command -v bash >/dev/null 2>&1; then exec bash -i; else exec sh -i; fi")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		conn.WriteMessage(websocket.BinaryMessage, []byte("\r\nFailed to start terminal: "+err.Error()+"\r\n"))
		return
	}
	defer func() {
		_ = ptmx.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	}()

	// PTY → browser.
	go func() {
		buf := make([]byte, 8192)
		for {
			n, rerr := ptmx.Read(buf)
			if n > 0 {
				if werr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					break
				}
			}
			if rerr != nil {
				break
			}
		}
		conn.Close()
	}()

	// Browser → PTY (binary = keystrokes; text = control JSON).
	for {
		mt, data, rerr := conn.ReadMessage()
		if rerr != nil {
			break
		}
		if mt == websocket.TextMessage {
			var msg struct {
				Type string `json:"type"`
				Cols uint16 `json:"cols"`
				Rows uint16 `json:"rows"`
			}
			if json.Unmarshal(data, &msg) == nil && msg.Type == "resize" {
				if msg.Cols > 0 && msg.Rows > 0 {
					_ = pty.Setsize(ptmx, &pty.Winsize{Rows: msg.Rows, Cols: msg.Cols})
				}
				continue
			}
		}
		if _, werr := ptmx.Write(data); werr != nil {
			break
		}
	}
	log.Printf("[terminal] session closed")
}
