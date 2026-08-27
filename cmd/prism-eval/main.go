// prism-eval replays a fixed set of real agent tasks against a running Prism
// and scores the result the way the agent experiences it: did the task
// succeed, how many tool calls did it take, how many of them failed, how long
// it ran. It is the safety net for every change that touches what the model
// sees — prompt, tool schemas, tool results, the executor. See eval/README.md.
//
//	go run ./cmd/prism-eval -url http://localhost:48080 -token "$PRISM_TOKEN"
//	go run ./cmd/prism-eval -baseline eval/baseline.json -out eval/results.json
//
// Each task talks to Prism over the same WebSocket the dashboard uses, so the
// whole stack is exercised — widget previews included — not a headless
// shortcut. Verification runs through /api/builtin/<tool>, the dispatcher
// widgets and custom tools already rely on.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// Task is one scenario from eval/tasks.json.
type Task struct {
	Name string   `json:"name"`
	Tags []string `json:"tags,omitempty"`
	// Prompt is sent to the agent verbatim, as a chat message.
	Prompt string `json:"prompt"`
	// Setup and Cleanup are builtin tool calls run before/after the turn,
	// outside the agent (fixtures, teardown). Failures here abort the task.
	Setup   []ToolCall `json:"setup,omitempty"`
	Cleanup []ToolCall `json:"cleanup,omitempty"`
	// Checks decide success. Every check must pass.
	Checks []Check `json:"checks"`
	// MaxToolCalls is a soft budget: exceeding it does not fail the task but
	// is reported, since "succeeded in 30 calls" is not comfort.
	MaxToolCalls int `json:"max_tool_calls,omitempty"`
	// Timeout for the agent turn (default: -timeout flag).
	TimeoutSec int `json:"timeout_sec,omitempty"`
}

// ToolCall is a builtin tool invocation: POST /api/builtin/<tool>.
type ToolCall struct {
	Tool string                 `json:"tool"`
	Args map[string]interface{} `json:"args,omitempty"`
}

// Check verifies the outcome. Exactly one target: a tool call (its result is
// inspected) or the agent's final response.
type Check struct {
	ToolCall
	Response bool `json:"response,omitempty"` // inspect the agent's answer instead of a tool result
	// Assertions on the inspected text (all that are set must hold).
	Contains    string   `json:"contains,omitempty"`
	ContainsAll []string `json:"contains_all,omitempty"`
	ContainsAny []string `json:"contains_any,omitempty"`
	NotContains string   `json:"not_contains,omitempty"`
	Regex       string   `json:"regex,omitempty"`
	MinLen      int      `json:"min_len,omitempty"`
	NoError     bool     `json:"no_error,omitempty"` // the tool call itself must not error
}

// Result is what one task run produced.
type Result struct {
	Name       string         `json:"name"`
	Tags       []string       `json:"tags,omitempty"`
	Success    bool           `json:"success"`
	Failures   []string       `json:"failures,omitempty"`
	ToolCalls  int            `json:"tool_calls"`
	ToolErrors int            `json:"tool_errors"`
	Tools      map[string]int `json:"tools,omitempty"`
	OverBudget bool           `json:"over_budget,omitempty"`
	AgentError string         `json:"agent_error,omitempty"`
	DurationMS int64          `json:"duration_ms"`
	Response   string         `json:"response,omitempty"`
}

// Report is the file written by -out and compared by -baseline.
type Report struct {
	When        string   `json:"when"`
	URL         string   `json:"url"`
	Model       string   `json:"model,omitempty"`
	Runs        int      `json:"runs"`
	Results     []Result `json:"results"`
	SuccessRate float64  `json:"success_rate"`
	MeanCalls   float64  `json:"mean_tool_calls"`
	MeanErrors  float64  `json:"mean_tool_errors"`
	MeanMS      float64  `json:"mean_duration_ms"`
}

type client struct {
	base    string
	token   string
	model   string
	http    *http.Client
	timeout time.Duration
	verbose bool
}

func main() {
	var (
		urlFlag  = flag.String("url", envOr("PRISM_URL", "http://localhost:48080"), "Prism base URL")
		token    = flag.String("token", os.Getenv("PRISM_TOKEN"), "PRISM_TOKEN (Bearer)")
		model    = flag.String("model", "", "model override for every turn (default: deployment default)")
		tasks    = flag.String("tasks", "eval/tasks.json", "task file")
		only     = flag.String("only", "", "run only tasks whose name or tag contains this")
		runs     = flag.Int("runs", 1, "runs per task (averaged)")
		out      = flag.String("out", "", "write the JSON report here")
		baseline = flag.String("baseline", "", "compare against this earlier report and fail on regression")
		timeout  = flag.Duration("timeout", 8*time.Minute, "per-turn timeout")
		keep     = flag.Bool("keep", false, "keep eval sessions and fixtures (skip cleanup)")
		verbose  = flag.Bool("v", false, "print every tool call as it happens")
	)
	flag.Parse()

	var list []Task
	raw, err := os.ReadFile(*tasks)
	if err != nil {
		fatal("read tasks: %v", err)
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		fatal("parse tasks: %v", err)
	}
	if *only != "" {
		var f []Task
		for _, t := range list {
			if strings.Contains(t.Name, *only) || hasTag(t, *only) {
				f = append(f, t)
			}
		}
		list = f
	}
	if len(list) == 0 {
		fatal("no tasks selected")
	}

	c := &client{base: strings.TrimRight(*urlFlag, "/"), token: *token, model: *model,
		http: &http.Client{Timeout: 5 * time.Minute}, timeout: *timeout, verbose: *verbose}
	if err := c.ping(); err != nil {
		fatal("prism unreachable at %s: %v", c.base, err)
	}

	rep := Report{When: time.Now().Format(time.RFC3339), URL: c.base, Model: *model, Runs: *runs}
	for _, t := range list {
		for i := 0; i < *runs; i++ {
			r := c.runTask(t, i, *keep)
			rep.Results = append(rep.Results, r)
			printResult(r)
		}
	}
	summarize(&rep)
	fmt.Println()
	fmt.Printf("success %.0f%%  ·  mean tool calls %.1f  ·  mean tool errors %.2f  ·  mean %.0fs\n",
		rep.SuccessRate*100, rep.MeanCalls, rep.MeanErrors, rep.MeanMS/1000)

	if *out != "" {
		b, _ := json.MarshalIndent(rep, "", "  ")
		if err := os.WriteFile(*out, b, 0644); err != nil {
			fatal("write report: %v", err)
		}
		fmt.Println("report written to", *out)
	}
	if *baseline != "" {
		if regressed := compare(*baseline, rep); regressed {
			os.Exit(2)
		}
	}
	if rep.SuccessRate < 1 {
		os.Exit(1)
	}
}

func (c *client) runTask(t Task, run int, keep bool) Result {
	session := fmt.Sprintf("eval-%s", t.Name)
	res := Result{Name: t.Name, Tags: t.Tags, Tools: map[string]int{}}
	fail := func(f string, a ...interface{}) { res.Failures = append(res.Failures, fmt.Sprintf(f, a...)) }

	// Fresh conversation every run: an eval must not benefit from a previous
	// attempt's history.
	c.deleteSession(session)
	for _, sc := range t.Setup {
		if _, err := c.builtin(session, sc); err != nil {
			fail("setup %s: %v", sc.Tool, err)
			return res
		}
	}

	timeout := c.timeout
	if t.TimeoutSec > 0 {
		timeout = time.Duration(t.TimeoutSec) * time.Second
	}
	started := time.Now()
	resp, err := c.chatWS(session, t.Prompt, timeout, &res)
	res.DurationMS = time.Since(started).Milliseconds()
	res.Response = resp
	if err != nil {
		res.AgentError = err.Error()
		fail("agent: %v", err)
	}

	for _, ck := range t.Checks {
		var text string
		var callErr error
		switch {
		case ck.Response:
			text = resp
		default:
			text, callErr = c.builtin(session, ck.ToolCall)
			if callErr != nil {
				if ck.NoError {
					fail("check %s errored: %v", ck.Tool, callErr)
					continue
				}
				text = "Error: " + callErr.Error()
			}
		}
		for _, f := range assert(ck, text) {
			fail("check %s: %s", ck.label(), f)
		}
	}
	if t.MaxToolCalls > 0 && res.ToolCalls > t.MaxToolCalls {
		res.OverBudget = true
	}
	if !keep {
		for _, cc := range t.Cleanup {
			c.builtin(session, cc)
		}
		c.deleteSession(session)
	}
	res.Success = len(res.Failures) == 0
	return res
}

func (ck Check) label() string {
	if ck.Response {
		return "response"
	}
	return ck.Tool
}

func assert(ck Check, text string) []string {
	var fails []string
	if ck.Contains != "" && !strings.Contains(text, ck.Contains) {
		fails = append(fails, fmt.Sprintf("missing %q", ck.Contains))
	}
	for _, s := range ck.ContainsAll {
		if !strings.Contains(text, s) {
			fails = append(fails, fmt.Sprintf("missing %q", s))
		}
	}
	if len(ck.ContainsAny) > 0 {
		ok := false
		for _, s := range ck.ContainsAny {
			if strings.Contains(text, s) {
				ok = true
				break
			}
		}
		if !ok {
			fails = append(fails, fmt.Sprintf("none of %v", ck.ContainsAny))
		}
	}
	if ck.NotContains != "" && strings.Contains(text, ck.NotContains) {
		fails = append(fails, fmt.Sprintf("unexpected %q", ck.NotContains))
	}
	if ck.Regex != "" {
		re, err := regexp.Compile("(?is)" + ck.Regex)
		if err != nil {
			fails = append(fails, "bad regex: "+err.Error())
		} else if !re.MatchString(text) {
			fails = append(fails, fmt.Sprintf("no match for /%s/", ck.Regex))
		}
	}
	if ck.MinLen > 0 && len(strings.TrimSpace(text)) < ck.MinLen {
		fails = append(fails, fmt.Sprintf("shorter than %d chars", ck.MinLen))
	}
	if len(fails) > 0 && len(text) > 0 {
		fails[len(fails)-1] += " — got: " + snippet(text)
	}
	return fails
}

// chatWS runs one turn over /ws and collects the final answer plus counters.
func (c *client) chatWS(session, prompt string, timeout time.Duration, res *Result) (string, error) {
	u, _ := url.Parse(c.base)
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	default:
		u.Scheme = "ws"
	}
	u.Path = "/ws"
	u.RawQuery = "session=" + url.QueryEscape(session)
	hdr := http.Header{}
	if c.token != "" {
		hdr.Set("Authorization", "Bearer "+c.token)
	}
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), hdr)
	if err != nil {
		return "", fmt.Errorf("ws dial: %w", err)
	}
	defer conn.Close()

	msg := map[string]interface{}{"type": "chat", "content": prompt}
	if c.model != "" {
		msg["model"] = c.model
	}
	if err := conn.WriteJSON(msg); err != nil {
		return "", fmt.Errorf("ws send: %w", err)
	}
	deadline := time.Now().Add(timeout)
	var answer strings.Builder
	inThink := false
	for {
		conn.SetReadDeadline(deadline)
		var ev struct {
			Type    string `json:"type"`
			Content string `json:"content"`
			Tool    string `json:"tool"`
			Output  string `json:"output"`
			IsError bool   `json:"is_error"`
		}
		if err := conn.ReadJSON(&ev); err != nil {
			return answer.String(), fmt.Errorf("turn did not complete: %w", err)
		}
		switch ev.Type {
		case "tool_use":
			res.ToolCalls++
			res.Tools[ev.Tool]++
			answer.Reset()
			inThink = false
			if c.verbose {
				fmt.Printf("      ↳ %s\n", ev.Tool)
			}
		case "tool_result":
			if ev.IsError {
				res.ToolErrors++
				if c.verbose {
					fmt.Printf("        ✗ %s\n", snippet(ev.Output))
				}
			}
		case "stream":
			switch ev.Content {
			case "<think>":
				inThink = true
			case "</think>":
				inThink = false
			default:
				if !inThink {
					answer.WriteString(ev.Content)
				}
			}
		case "error":
			// Loop detection, backend failures, context overflow: the turn is
			// over and the agent could not finish.
			return answer.String(), fmt.Errorf("%s", ev.Content)
		case "turn_complete":
			return answer.String(), nil
		}
	}
}

func (c *client) builtin(session string, tc ToolCall) (string, error) {
	body, _ := json.Marshal(tc.Args)
	if tc.Args == nil {
		body = []byte("{}")
	}
	req, _ := http.NewRequest("POST", c.base+"/api/builtin/"+url.PathEscape(tc.Tool)+"?session="+url.QueryEscape(session), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	c.auth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var out struct {
		Result interface{} `json:"result"`
		Error  string      `json:"error"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return string(raw), fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet(string(raw)))
	}
	if out.Error != "" {
		return "", fmt.Errorf("%s", out.Error)
	}
	switch v := out.Result.(type) {
	case string:
		return v, nil
	default:
		b, _ := json.Marshal(v)
		return string(b), nil
	}
}

func (c *client) deleteSession(session string) {
	req, _ := http.NewRequest("DELETE", c.base+"/api/sessions/"+url.PathEscape(session), nil)
	c.auth(req)
	if resp, err := c.http.Do(req); err == nil {
		resp.Body.Close()
	}
}

func (c *client) ping() error {
	req, _ := http.NewRequest("GET", c.base+"/api/sessions", nil)
	c.auth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return fmt.Errorf("HTTP %d — is -token / PRISM_TOKEN right?", resp.StatusCode)
	}
	return nil
}

func (c *client) auth(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

func summarize(rep *Report) {
	n := float64(len(rep.Results))
	if n == 0 {
		return
	}
	var ok, calls, errs, ms float64
	for _, r := range rep.Results {
		if r.Success {
			ok++
		}
		calls += float64(r.ToolCalls)
		errs += float64(r.ToolErrors)
		ms += float64(r.DurationMS)
	}
	rep.SuccessRate, rep.MeanCalls, rep.MeanErrors, rep.MeanMS = ok/n, calls/n, errs/n, ms/n
}

// compare fails the run when the new report is worse than the baseline on the
// agent's own terms: fewer successes, or the same successes bought with
// noticeably more tool calls or more tool errors.
func compare(path string, cur Report) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		fatal("read baseline: %v", err)
	}
	var base Report
	if err := json.Unmarshal(raw, &base); err != nil {
		fatal("parse baseline: %v", err)
	}
	fmt.Printf("\nbaseline %s: success %.0f%% · calls %.1f · errors %.2f\n", base.When,
		base.SuccessRate*100, base.MeanCalls, base.MeanErrors)
	regressed := false
	if cur.SuccessRate < base.SuccessRate {
		fmt.Printf("REGRESSION: success rate %.0f%% → %.0f%%\n", base.SuccessRate*100, cur.SuccessRate*100)
		regressed = true
	}
	if base.MeanCalls > 0 && cur.MeanCalls > base.MeanCalls*1.15 {
		fmt.Printf("REGRESSION: mean tool calls %.1f → %.1f (+%.0f%%)\n", base.MeanCalls, cur.MeanCalls, (cur.MeanCalls/base.MeanCalls-1)*100)
		regressed = true
	}
	if cur.MeanErrors > base.MeanErrors+0.25 {
		fmt.Printf("REGRESSION: mean tool errors %.2f → %.2f\n", base.MeanErrors, cur.MeanErrors)
		regressed = true
	}
	// Per-task: newly failing tasks are named, so the regression is actionable.
	was := map[string]bool{}
	for _, r := range base.Results {
		was[r.Name] = was[r.Name] || r.Success
	}
	var newlyFailing []string
	for _, r := range cur.Results {
		if !r.Success && was[r.Name] {
			newlyFailing = append(newlyFailing, r.Name)
		}
	}
	sort.Strings(newlyFailing)
	if len(newlyFailing) > 0 {
		fmt.Println("newly failing:", strings.Join(newlyFailing, ", "))
	}
	if !regressed {
		fmt.Println("no regression against baseline")
	}
	return regressed
}

func printResult(r Result) {
	mark := "✓"
	if !r.Success {
		mark = "✗"
	}
	extra := ""
	if r.OverBudget {
		extra = "  (over budget)"
	}
	fmt.Printf("%s %-32s %2d calls  %d err  %5.0fs%s\n", mark, r.Name, r.ToolCalls, r.ToolErrors, float64(r.DurationMS)/1000, extra)
	for _, f := range r.Failures {
		fmt.Printf("      %s\n", f)
	}
}

func hasTag(t Task, tag string) bool {
	for _, x := range t.Tags {
		if x == tag {
			return true
		}
	}
	return false
}

func snippet(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > 160 {
		return s[:160] + "…"
	}
	return s
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func fatal(f string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "prism-eval: "+f+"\n", a...)
	os.Exit(3)
}
