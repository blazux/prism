package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"prism/internal/docker"
	"prism/workspaceexec"
)

// Opt-in Docker test: a unique, unpublished HTTP/WebSocket fixture, temporary
// browser state and screenshots only. No existing widgets, cookies or data are
// read or modified. Traefik must share the chosen workspace's Docker network.
func TestBrowserTraefikRouting(t *testing.T) {
	container := os.Getenv("PRISM_BROWSER_TEST_CONTAINER")
	if container == "" {
		t.Skip("explicit Docker workspace fixture required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cli := func(args ...string) string {
		t.Helper()
		out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("docker %s: %v: %s", args[0], err, out)
		}
		return strings.TrimSpace(string(out))
	}
	image := cli("inspect", "--format", "{{.Config.Image}}", container)
	networks := strings.Fields(cli("inspect", "--format", "{{range $k,$v := .NetworkSettings.Networks}}{{$k}} {{end}}", container))
	if len(networks) == 0 {
		t.Fatal("workspace has no network")
	}
	name := "prism-browser-check-" + workspaceexec.ID()[:12]
	host, frameHost := name+".localhost", "frame-"+name+".localhost"
	root := "/tmp/" + name
	t.Cleanup(func() {
		exec.Command("docker", "rm", "-f", name).Run()
		exec.Command("docker", "exec", container, "rm", "-rf", root).Run()
	})
	cli("run", "-d", "--name", name, "--network", networks[0],
		"--label", "traefik.enable=true",
		"--label", "traefik.docker.network="+networks[0],
		"--label", fmt.Sprintf("traefik.http.routers.%s.rule=Host(`%s`) || Host(`%s`)", name, host, frameHost),
		"--label", "traefik.http.services."+name+".loadbalancer.server.port=8080",
		"--entrypoint", "python3", image, "-u", "-c", browserRoutingFixture, frameHost)
	m := docker.NewManager(container, "", 0, 0)
	// Wait for Traefik's provider to discover the fixture before browser assertions.
	probe := `import urllib.request, time
o=urllib.request.build_opener(urllib.request.ProxyHandler({}))
for _ in range(40):
    try:
        with o.open(urllib.request.Request('http://traefik/api', headers={'Host':` + fmt.Sprintf("%q", host) + `}), timeout=1) as r:
            if r.status == 200: break
    except Exception: time.sleep(.25)
else: raise RuntimeError('Traefik not reachable from workspace or route not discovered')
`
	if out, err := m.ExecWithStdin(ctx, "python3 -", []byte(probe), 20*time.Second, nil); err != nil {
		t.Fatalf("proxy: %v: %s", err, out)
	}
	url := "http://" + host + "/start"
	getExpr := "JSON.stringify(window.report)"
	getCommand := fmt.Sprintf("python3 - %q %q 0 %q", url, getExpr, (&ToolExecutor{docker: m}).browserServiceProxy())
	getOut, err := m.ExecWithStdin(ctx, getCommand, []byte(browserScript), 30*time.Second, nil)
	if err != nil {
		t.Fatalf("browser_get: %v: %s", err, getOut)
	}
	var getJSON string
	if err := json.Unmarshal([]byte(getOut), &getJSON); err != nil {
		t.Fatalf("browser_get result: %s: %v", getOut, err)
	}
	assertBrowserRouteReport(t, getJSON, host, frameHost, 0, false)

	input, _ := json.Marshal(map[string]interface{}{
		"url": url, "service_proxy": (&ToolExecutor{docker: m}).browserServiceProxy(), "auth_cookie": "fixture-cookie-only",
		"actions": []map[string]interface{}{
			{"type": "click", "selector": "#increment"},
			{"type": "evaluate", "expression": "JSON.stringify(window.report)"},
			{"type": "screenshot"},
		},
	})
	// Use the exact production script, with its output directory relocated to a
	// private test directory. Never share the user's browser session state.
	actScript := strings.Replace(browserActScript, "SCREENSHOT_DIR = '/workspace/.screenshots'", "SCREENSHOT_DIR = "+fmt.Sprintf("%q", root+"/screenshots"), 1)
	bootstrap := "import os\nos.makedirs(" + fmt.Sprintf("%q", root) + ", exist_ok=True)\nopen(os.environ['BROWSER_ACT_INPUT'], 'w').write(" + fmt.Sprintf("%q", string(input)) + ")\n"
	actOut, err := m.ExecWithStdin(ctx, "python3 -", []byte(bootstrap+actScript), 40*time.Second, map[string]string{
		"BROWSER_ACT_INPUT": root + "/input.json", "BROWSER_ACT_SESSION": root + "/session.json",
	})
	if err != nil {
		t.Fatalf("browser_act: %v: %s", err, actOut)
	}
	var results []struct{ Action, Status, Result, URL string }
	if err := json.Unmarshal([]byte(actOut), &results); err != nil {
		t.Fatalf("browser_act JSON: %v: %s", err, actOut)
	}
	seenReport, seenScreenshot := false, false
	for _, result := range results {
		if result.Status == "error" {
			t.Fatalf("browser_act action failed: %s", actOut)
		}
		if result.Action == "evaluate" {
			assertBrowserRouteReport(t, result.Result, host, frameHost, 1, true)
			seenReport = true
		}
		if result.Action == "screenshot" && result.Status == "ok" {
			filename := strings.TrimPrefix(result.URL, "/screenshots/")
			if out, err := m.Exec(ctx, "test -s '"+root+"/screenshots/"+filename+"'", time.Second); err != nil {
				t.Fatalf("screenshot: %v: %s", err, out)
			}
			seenScreenshot = true
		}
	}
	if !seenReport || !seenScreenshot {
		t.Fatalf("missing browser results: %s", actOut)
	}
}

func assertBrowserRouteReport(t *testing.T, raw, host, frameHost string, counter int, auth bool) {
	t.Helper()
	var report struct {
		Host, Cookie, Websocket, FrameOrigin, FrameCookie, Origin, Path string
		Asset                                                           bool
		Counter                                                         int
	}
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		t.Fatalf("page report: %v: %s", err, raw)
	}
	if report.Host != host || report.Websocket != host || report.Origin != "http://"+host || report.Path != "/app" || !report.Asset || report.Counter != counter || report.FrameOrigin != "http://"+frameHost || report.FrameCookie != "" {
		t.Fatalf("routing/assets/redirect/iframe/WebSocket/interaction failed: %s", raw)
	}
	if auth && !strings.Contains(report.Cookie, "prism_session=fixture-cookie-only") {
		t.Fatalf("cookie not scoped to page host: %s", raw)
	}
	if !auth && report.Cookie != "" {
		t.Fatalf("unexpected browser state: %s", raw)
	}
}

const browserRoutingFixture = `import base64, hashlib, json, sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
frame_host = sys.argv[1]
class Handler(BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'
    def log_message(self, *args): pass
    def do_GET(self):
        if self.path == '/start':
            self.send_response(302); self.send_header('Location', '/app'); self.send_header('Content-Length', '0'); self.end_headers(); return
        if self.path == '/socket':
            accept = base64.b64encode(hashlib.sha1((self.headers['Sec-WebSocket-Key']+'258EAFA5-E914-47DA-95CA-C5AB0DC85B11').encode()).digest()).decode()
            self.send_response(101); self.send_header('Upgrade', 'websocket'); self.send_header('Connection', 'Upgrade'); self.send_header('Sec-WebSocket-Accept', accept); self.end_headers()
            msg=self.headers['Host'].encode(); self.wfile.write(bytes([0x81,len(msg)])+msg); self.wfile.flush(); self.close_connection=True; return
        kind='text/html'
        if self.path == '/api':
            kind='application/json'; content=json.dumps({'host':self.headers['Host'],'cookie':self.headers.get('Cookie','')})
        elif self.path == '/asset.js':
            kind='application/javascript'; content='window.report.Asset = true;'
        elif self.headers['Host'] == frame_host:
            content='<script>parent.postMessage({kind:"frame",cookie:document.cookie},"*")</script><p>Frame route works</p>'
        else:
            content='''<!doctype html><button id="increment" onclick="report.Counter++">Increment</button><iframe src="http://FRAME_HOST/app"></iframe>
<script>
window.report={Origin:location.origin,Path:location.pathname,Counter:0,Asset:false};
window.addEventListener('message',e=>{if(e.data.kind==='frame'){report.FrameOrigin=e.origin;report.FrameCookie=e.data.cookie}});
fetch('/api').then(r=>r.json()).then(r=>{report.Host=r.host;report.Cookie=r.cookie});
const socket=new WebSocket('ws://'+location.host+'/socket');socket.onmessage=e=>{report.Websocket=e.data};
</script><script src="/asset.js"></script>'''.replace('FRAME_HOST', frame_host)
        data=content.encode(); self.send_response(200); self.send_header('Content-Type',kind); self.send_header('Content-Length',str(len(data))); self.end_headers(); self.wfile.write(data)
ThreadingHTTPServer(('0.0.0.0',8080),Handler).serve_forever()
`
