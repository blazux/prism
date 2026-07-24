package agent

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// isPrivateHost reports whether host (an IP literal or hostname) resolves to
// an RFC1918/loopback/link-local address — i.e. it's on the local network
// rather than the public internet. Lab and IoT devices on such addresses
// routinely serve self-signed certs with no IP SANs, which every Go and
// browser TLS stack rejects by default. We skip verification ONLY for these
// addresses: an LLM-driven tool that fetches arbitrary URLs (from web search
// results, a user's paste, etc.) must not silently disable TLS verification
// for the public internet, where it would enable a real MITM.
func isPrivateHost(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		ips, err := net.LookupIP(host)
		if err != nil || len(ips) == 0 {
			return false
		}
		ip = ips[0]
	}
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()
}

func (e *ToolExecutor) httpRequest(ctx context.Context, method, rawURL string, headers map[string]string, body string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("invalid URL: must start with http:// or https://")
	}

	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(method), rawURL, bodyReader)
	if err != nil {
		return "", err
	}

	if method == "GET" || method == "" {
		req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36")
		req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.9")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	if u.Scheme == "https" && isPrivateHost(u.Hostname()) {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		client.Transport = tr
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	isHTML := strings.Contains(ct, "html")

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4000))
		return fmt.Sprintf("HTTP %d %s\n%s", resp.StatusCode, resp.Status, string(raw)), nil
	}

	// Surface framing restrictions so the model can check whether a site can be
	// embedded in a widget iframe before trying.
	var framing string
	if xfo := resp.Header.Get("X-Frame-Options"); xfo != "" {
		framing = fmt.Sprintf("[X-Frame-Options: %s — this site CANNOT be embedded in an iframe]\n", xfo)
	}
	if csp := resp.Header.Get("Content-Security-Policy"); strings.Contains(csp, "frame-ancestors") {
		framing += "[CSP frame-ancestors set — iframe embedding restricted]\n"
	}

	if isHTML && (method == "GET" || method == "") {
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
		if err != nil {
			return "", fmt.Errorf("read body: %w", err)
		}
		doc, err := html.Parse(strings.NewReader(string(raw)))
		if err != nil {
			return "", fmt.Errorf("parse HTML: %w", err)
		}
		text := extractText(doc)
		if len(text) > 8000 {
			text = text[:8000] + "\n...[truncated]"
		}
		return framing + text, nil
	}

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8000))
	result := fmt.Sprintf("HTTP %d\n%s%s", resp.StatusCode, framing, string(raw))
	return result, nil
}

func (e *ToolExecutor) webSearch(ctx context.Context, query string) (string, error) {
	if e.searxngURL == "" {
		return "web_search unavailable: SEARXNG_URL not configured", nil
	}

	searchURL := e.searxngURL + "/search?q=" + url.QueryEscape(query) + "&format=json&categories=general"
	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("search failed: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	if len(result.Results) == 0 {
		return "No results found", nil
	}

	var sb strings.Builder
	for i, r := range result.Results {
		if i >= 5 {
			break
		}
		fmt.Fprintf(&sb, "%d. %s\n   %s\n   %s\n\n", i+1, r.Title, r.URL, r.Content)
	}
	return sb.String(), nil
}

// browserScript is written to /workspace and run inside the workspace container.
const browserScript = `import sys, json, re
from playwright.sync_api import sync_playwright

url = sys.argv[1]
js_expr = sys.argv[2] if len(sys.argv) > 2 and sys.argv[2] else None
ignore_https_errors = len(sys.argv) > 3 and sys.argv[3] == '1'

with sync_playwright() as p:
    browser = p.chromium.launch(
        headless=True,
        args=['--no-sandbox', '--disable-dev-shm-usage', '--disable-gpu']
    )
    page = browser.new_page(ignore_https_errors=ignore_https_errors)
    page.goto(url, wait_until='domcontentloaded', timeout=30000)
    page.wait_for_timeout(800)

    if js_expr:
        result = page.evaluate(js_expr)
        if result is None:
            # Playwright turns undefined into None, so a typo in a global name or a
            # function body without a return both come back as a silent "null".
            print("[script returned null/undefined. If you expected data: check the "
                  "global exists (evaluate 'Object.keys(window)') and that an arrow "
                  "function body returns a value.]")
        else:
            print(json.dumps(result, default=str))
    else:
        page.evaluate(
            "() => document.querySelectorAll('script,style,nav,footer,header,aside,noscript').forEach(e=>e.remove())"
        )
        try:
            text = page.inner_text('body')
        except Exception:
            text = page.content()
        text = re.sub(r'\n{3,}', '\n\n', text).strip()
        print(text[:8000])

    browser.close()
`

// validateBrowserURL accepts http/https, plus file:// URLs that stay inside the
// workspace. A chat attachment is saved there, and rendering it in the headless
// browser is the only way the agent gets to *see* a page rather than read its
// markup — an HTML export whose content is built by scripts has no readable text
// until it runs.
//
// file:// grants nothing new: the same container already runs arbitrary shell
// commands for the agent. The workspace prefix keeps a stray model from turning
// the browser into a reader for /etc anyway.
func validateBrowserURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: must start with http://, https:// or file:///workspace/")
	}
	switch u.Scheme {
	case "http", "https":
		return nil
	case "file":
		// Reject a host component ("file://evil/…") and any traversal out of /workspace.
		if u.Host != "" || !strings.HasPrefix(path.Clean(u.Path)+"/", "/workspace/") {
			return fmt.Errorf("invalid file URL: must be under file:///workspace/")
		}
		return nil
	default:
		return fmt.Errorf("invalid URL: must start with http://, https:// or file:///workspace/")
	}
}

func (e *ToolExecutor) browserExec(ctx context.Context, rawURL, jsExpr string) (string, error) {
	if err := validateBrowserURL(rawURL); err != nil {
		return "", err
	}

	scriptPath := filepath.Join(e.workspaceDir, ".browser_exec.py")
	if err := os.WriteFile(scriptPath, []byte(browserScript), 0644); err != nil {
		return "", fmt.Errorf("write script: %w", err)
	}
	defer os.Remove(scriptPath)

	ignoreHTTPS := "0"
	if u, err := url.Parse(rawURL); err == nil && u.Scheme == "https" && isPrivateHost(u.Hostname()) {
		ignoreHTTPS = "1"
	}
	cmd := fmt.Sprintf("python3 /workspace/.browser_exec.py %q %q %q 2>&1", rawURL, jsExpr, ignoreHTTPS)

	out, err := e.docker.Exec(ctx, cmd, 60*time.Second)
	if err != nil {
		return fmt.Sprintf("browser_get failed: %v\n%s", err, out), nil
	}
	if len(out) > 8000 {
		out = out[:8000] + "\n...[truncated]"
	}
	return out, nil
}

const browserActScript = `import json, os, time
from urllib.parse import urlparse
from playwright.sync_api import sync_playwright

INPUT_FILE = os.environ['BROWSER_ACT_INPUT']
SESSION_FILE = os.environ['BROWSER_ACT_SESSION']
SCREENSHOT_DIR = '/workspace/.screenshots'
os.makedirs(SCREENSHOT_DIR, exist_ok=True)

with open(INPUT_FILE) as f:
    config = json.load(f)

start_url = config.get('url')
actions = config.get('actions') or []
results = []

with sync_playwright() as p:
    browser = p.chromium.launch(headless=True,
        args=['--no-sandbox','--disable-dev-shm-usage','--disable-gpu'])
    ctx_opts = {}
    if os.path.exists(SESSION_FILE):
        ctx_opts['storage_state'] = SESSION_FILE
    if config.get('ignore_https_errors'):
        ctx_opts['ignore_https_errors'] = True
    context = browser.new_context(**ctx_opts)
    # Authenticated widget preview: attach the Prism service cookie, scoped to the
    # start URL's host but every path on it (path='/') — a widget calls /api/...
    # and /data/... from the SAME page, not just the /plugins/<id>.html path it
    # was loaded from. Passing 'url' instead of 'domain'+'path' here silently
    # scopes the cookie to that one page's path (e.g. /plugins/default/),
    # so every other same-origin fetch the widget makes goes out with no
    # cookie at all and gets a 401.
    auth_cookie = config.get('auth_cookie')
    if auth_cookie and start_url:
        try:
            context.add_cookies([{'name': 'prism_session', 'value': auth_cookie, 'domain': urlparse(start_url).hostname, 'path': '/'}])
        except Exception:
            pass
    page = context.new_page()
    console_msgs = []
    page.on('console', lambda msg: console_msgs.append({'level': msg.type, 'text': msg.text}))
    page.on('pageerror', lambda err: console_msgs.append({'level': 'error', 'text': str(err)}))

    if start_url:
        page.goto(start_url, wait_until='domcontentloaded', timeout=30000)
        page.wait_for_timeout(500)
        results.append({'action':'navigate','status':'ok','url':page.url})

    for action in actions:
        atype = action.get('type','')
        try:
            if atype == 'navigate':
                page.goto(action['url'], wait_until='domcontentloaded', timeout=30000)
                page.wait_for_timeout(500)
                results.append({'action':'navigate','status':'ok','url':page.url})
            elif atype == 'click':
                page.click(action['selector'], timeout=10000)
                page.wait_for_timeout(300)
                results.append({'action':'click','selector':action['selector'],'status':'ok'})
            elif atype == 'type':
                page.fill(action['selector'], action['text'], timeout=10000)
                results.append({'action':'type','selector':action['selector'],'status':'ok'})
            elif atype == 'clear':
                page.fill(action['selector'], '', timeout=10000)
                results.append({'action':'clear','selector':action['selector'],'status':'ok'})
            elif atype == 'select':
                page.select_option(action['selector'], action['value'], timeout=10000)
                results.append({'action':'select','selector':action['selector'],'status':'ok'})
            elif atype == 'wait_for':
                page.wait_for_selector(action['selector'], timeout=15000)
                results.append({'action':'wait_for','selector':action['selector'],'status':'ok'})
            elif atype == 'screenshot':
                fname = str(int(time.time()*1000)) + '.png'
                path = os.path.join(SCREENSHOT_DIR, fname)
                page.screenshot(path=path, full_page=bool(action.get('full_page')))
                results.append({'action':'screenshot','status':'ok','url':'/screenshots/'+fname})
            elif atype == 'evaluate':
                result = page.evaluate(action['expression'])
                results.append({'action':'evaluate','status':'ok','result':result})
            elif atype == 'get_text':
                sel = action.get('selector','body')
                text = page.inner_text(sel, timeout=10000)
                results.append({'action':'get_text','selector':sel,'status':'ok','text':text[:4000]})
            elif atype == 'wait_ms':
                ms = int(action.get('ms') or action.get('timeMs') or action.get('value') or action.get('duration') or 1000)
                page.wait_for_timeout(ms)
                results.append({'action':'wait_ms','ms':ms,'status':'ok'})
            else:
                results.append({'action':atype,'status':'error','error':'unknown action type'})
        except Exception as e:
            results.append({'action':atype,'status':'error','error':str(e)})

    context.storage_state(path=SESSION_FILE)
    context.close()
    browser.close()

if console_msgs:
    # Deduplicate repeated messages (some pages log the same warning dozens of
    # times) and cap the list so the output stays readable.
    seen = {}
    deduped = []
    for m in console_msgs:
        key = (m['level'], m['text'][:200])
        if key in seen:
            seen[key]['count'] += 1
        else:
            entry = {'level': m['level'], 'text': m['text'][:500], 'count': 1}
            seen[key] = entry
            deduped.append(entry)
    deduped = deduped[:30]
    for m in deduped:
        if m['count'] == 1:
            del m['count']
    results.append({'action': 'console', 'messages': deduped})
print(json.dumps(results, default=str, indent=2))
`

func (e *ToolExecutor) browserAct(ctx context.Context, rawURL string, rawActions interface{}) (string, error) {
	return e.browserActAuth(ctx, rawURL, rawActions, "")
}

// browserActAuth is browserAct with an optional Prism service token. When set,
// the preview browser adds a `prism_session` cookie scoped to the start URL's
// origin only — used for authenticated widget previews on multi-user Prism,
// without ever sending the token to any other site the agent browses.
func (e *ToolExecutor) browserActAuth(ctx context.Context, rawURL string, rawActions interface{}, authToken string) (string, error) {
	if rawURL != "" {
		if err := validateBrowserURL(rawURL); err != nil {
			return "", err
		}
	}

	sessionID := e.sessionID
	if sessionID == "" {
		sessionID = "default"
	}

	type actInput struct {
		URL               string      `json:"url,omitempty"`
		Actions           interface{} `json:"actions"`
		AuthCookie        string      `json:"auth_cookie,omitempty"`
		IgnoreHTTPSErrors bool        `json:"ignore_https_errors,omitempty"`
	}
	ignoreHTTPS := false
	if u, err := url.Parse(rawURL); err == nil && u.Scheme == "https" && isPrivateHost(u.Hostname()) {
		ignoreHTTPS = true
	}
	inputJSON, err := json.Marshal(actInput{URL: rawURL, Actions: rawActions, AuthCookie: authToken, IgnoreHTTPSErrors: ignoreHTTPS})
	if err != nil {
		return "", fmt.Errorf("marshal actions: %w", err)
	}

	inputFile := filepath.Join(e.workspaceDir, ".browser_act_"+sessionID+".json")
	sessionFile := "/workspace/.browser_session_" + sessionID + ".json"

	if err := os.WriteFile(inputFile, inputJSON, 0644); err != nil {
		return "", fmt.Errorf("write input: %w", err)
	}
	defer os.Remove(inputFile)

	scriptPath := filepath.Join(e.workspaceDir, ".browser_act.py")
	if err := os.WriteFile(scriptPath, []byte(browserActScript), 0644); err != nil {
		return "", fmt.Errorf("write script: %w", err)
	}

	containerInput := "/workspace/.browser_act_" + sessionID + ".json"
	cmd := fmt.Sprintf(
		"BROWSER_ACT_INPUT=%s BROWSER_ACT_SESSION=%s python3 /workspace/.browser_act.py 2>&1",
		containerInput, sessionFile,
	)
	out, err := e.docker.Exec(ctx, cmd, 90*time.Second)
	if err != nil {
		return fmt.Sprintf("browser_act failed: %v\n%s", err, out), nil
	}
	if len(out) > 12000 {
		out = out[:12000] + "\n...[truncated]"
	}
	return out, nil
}

// ─── Cron tools ───────────────────────────────────────────────────────────────

var skipTags = map[string]bool{
	"script": true, "style": true, "nav": true, "footer": true,
	"header": true, "aside": true, "noscript": true, "iframe": true,
	"form": true, "button": true, "svg": true, "img": true, "figure": true,
}

var blockTags = map[string]bool{
	"p": true, "div": true, "article": true, "section": true, "main": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"li": true, "dt": true, "dd": true, "tr": true,
	"blockquote": true, "pre": true, "br": true, "hr": true,
}

func extractText(n *html.Node) string {
	var buf strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			t := strings.TrimSpace(n.Data)
			if t != "" {
				buf.WriteString(t)
				buf.WriteByte(' ')
			}
			return
		}
		if n.Type != html.ElementNode {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
			return
		}
		if skipTags[n.Data] {
			return
		}
		if blockTags[n.Data] {
			buf.WriteByte('\n')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if blockTags[n.Data] {
			buf.WriteByte('\n')
		}
	}
	walk(n)

	result := buf.String()
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(result)
}
