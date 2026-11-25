package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"prism/internal/docker"
	webassets "prism/web"
)

type widgetBrowserWebFS struct{ fs.FS }

func (f widgetBrowserWebFS) Open(name string) (fs.File, error) {
	if name == "web" {
		return f.FS.Open(".")
	}
	if strings.HasPrefix(name, "web/") {
		return f.FS.Open(strings.TrimPrefix(name, "web/"))
	}
	return nil, fs.ErrNotExist
}

// TestWidgetBrowserFixture is an opt-in companion to widget-backend-browser.cjs.
// Normal test runs skip it. The browser launches this test in a separate process
// and receives only ephemeral loopback URLs. All files, credentials, tools and
// services are synthetic; no model, database or Docker daemon is contacted.
func TestWidgetBrowserFixture(t *testing.T) {
	ready := os.Getenv("PRISM_WIDGET_BROWSER_READY")
	if ready == "" {
		t.Skip("launched by the widget backend browser test")
	}
	root := t.TempDir()
	for _, dir := range []string{"data", "plugins/main", "agent_tools"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	activeHTML := `<!doctype html><html><head></head><body><p>Generated fixture</p><script>try{parent.document.getElementById('private-marker');document.body.dataset.parentBlocked='false'}catch(_){document.body.dataset.parentBlocked='true'}document.body.dataset.ready='true'</script></body></html>`
	for name, content := range map[string]string{
		"data/fixture.json":             `{"answer":"data-é"}`,
		"data/fixture.html":             activeHTML,
		"plugins/main/fixture.html":     activeHTML,
		"agent_tools/widget_fixture.py": "# TOOL: {\"name\":\"widget_fixture\",\"description\":\"Browser fixture\"}\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	toolCalls, proxyCalls := 0, 0
	proxyCredential, serviceCookie := false, false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		proxyCalls++
		proxyCredential = proxyCredential || strings.Contains(r.Header.Get("Cookie"), sessionCookie+"=") || strings.Contains(r.Header.Get("Cookie"), widgetCookie) || r.Header.Get("Authorization") != "" || r.URL.Query().Has("widget_grant")
		serviceCookie = serviceCookie || strings.Contains(r.Header.Get("Cookie"), "fixture_service=retained")
		mu.Unlock()
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			connection, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer connection.Close()
			if _, _, err := connection.ReadMessage(); err == nil {
				_ = connection.WriteMessage(websocket.TextMessage, []byte("pong"))
			}
			return
		}
		if r.URL.Path == "/state" {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"answer":"service-é"}`)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "fixture_service", Value: "retained", Path: "/", SameSite: http.SameSiteLaxMode})
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, activeHTML)
	}))
	main := httptest.NewUnstartedServer(nil)
	content := httptest.NewUnstartedServer(nil)
	_, mainPort, _ := net.SplitHostPort(main.Listener.Addr().String())
	s := New(Config{WorkspaceDir: root, PluginDir: filepath.Join(root, "plugins"), AuthToken: "synthetic-browser-fixture-token",
		Port: mainPort, WidgetListenAddr: content.Listener.Addr().String(), WidgetOrigin: "http://" + content.Listener.Addr().String(),
		AgentContainer: "fixture-workspace", WebFS: widgetBrowserWebFS{webassets.Files()}})
	s.docker = docker.WithExecution(func(ctx context.Context, command string, input []byte, env map[string]string) (string, error) {
		mu.Lock()
		toolCalls++
		mu.Unlock()
		return `{"answer":"tool-é"}`, nil
	})
	target, _ := url.Parse(upstream.URL)
	s.cfg.WorkspaceDial = func(ctx context.Context, port int) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", target.Host)
	}
	handler, err := s.Handler()
	if err != nil {
		t.Fatal(err)
	}
	widgetHandler, err := s.WidgetHandler()
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var stopped sync.Once
	main.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/__widget_fixture_host":
			setSessionCookie(w, s.cfg.AuthToken, 3600, r)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			io.WriteString(w, `<!doctype html><html><head><script src="/theme.js"></script></head><body><p id="private-marker">Synthetic dashboard private marker</p><main></main><script>
let activeFrame;
window.showFixture=async html=>{
 const identity=await fetch('/api/me').then(r=>r.json());window.fixtureHosting=identity.widgetHosting;
 const f=document.createElement('iframe');f.sandbox='allow-scripts allow-same-origin allow-forms allow-popups allow-downloads';f.style='width:900px;height:600px';f.src=identity.widgetHosting.frameURL;
 activeFrame={frame:f,html};document.querySelector('main').replaceChildren(f);
};
addEventListener('message',async e=>{
 const active=activeFrame;if(!active||e.source!==active.frame.contentWindow||e.origin!==new URL(active.frame.src).origin||e.data?.type!=='prism-widget-ready')return;
 const grant=await fetch(fixtureHosting.grantURL,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({session:'main'})}).then(r=>r.json());
 if(activeFrame!==active)return;e.source.postMessage({type:'prism-widget-render',grant:grant.token,html:PrismTheme.composeWidgetDoc(active.html,'main')},e.origin);
});
</script></body></html>`)
		case "/__widget_fixture_state":
			mu.Lock()
			defer mu.Unlock()
			writeJSON(w, map[string]any{"tools": toolCalls, "proxyRequests": proxyCalls, "proxyCredential": proxyCredential, "serviceCookie": serviceCookie})
		case "/__widget_fixture_stop":
			stopped.Do(func() { close(stop) })
			w.WriteHeader(http.StatusNoContent)
		default:
			handler.ServeHTTP(w, r)
		}
	})
	content.Config.Handler = widgetHandler
	main.Start()
	content.Start()
	defer func() {
		main.Close()
		content.Close()
		upstream.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	}()
	data, _ := json.Marshal(map[string]string{"main": main.URL, "widget": content.URL})
	if err := os.WriteFile(ready+".tmp", data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(ready+".tmp", ready); err != nil {
		t.Fatal(err)
	}
	tick, timeout := time.NewTicker(50*time.Millisecond), time.NewTimer(2*time.Minute)
	defer tick.Stop()
	defer timeout.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			if _, err := os.Stat(ready + ".stop"); err == nil {
				return
			}
		case <-timeout.C:
			t.Fatal(fmt.Errorf("widget browser fixture was not stopped"))
		}
	}
}
