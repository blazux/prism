package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"prism/internal/docker"
	"prism/internal/memory"
)

func TestSharedPreviewValidatesImages(t *testing.T) {
	var input bytes.Buffer
	png.Encode(&input, image.NewRGBA(image.Rect(0, 0, 480, 280)))
	data, err := sharedPreviewJPEG(input.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width != 480 || cfg.Height != 280 {
		t.Fatal(cfg, err)
	}
	if _, err := sharedPreviewJPEG([]byte("<svg onload='alert(1)'>")); err == nil {
		t.Fatal("non-PNG accepted")
	}
	input.Reset()
	png.Encode(&input, image.NewRGBA(image.Rect(0, 0, 1201, 1)))
	if _, err := sharedPreviewJPEG(input.Bytes()); err == nil {
		t.Fatal("unbounded dimensions accepted")
	}
}

// Run the actual workspace capture script in a disposable directory using a
// local execution capability. No real workspace, cookies or model is used.
func TestSharedPreviewBrowserCapture(t *testing.T) {
	python := os.Getenv("PRISM_TEST_BROWSER_PYTHON")
	if python == "" {
		t.Skip("isolated Playwright Python required")
	}
	root := t.TempDir()
	var reject atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/favicon.ico" {
			w.WriteHeader(404)
			return
		}
		c, err := r.Cookie("prism_session")
		if err != nil || c.Value != "fixture-cookie" {
			t.Error("preview was not authenticated")
		}
		if reject.Load() {
			w.WriteHeader(403)
			fmt.Fprint(w, "Forbidden")
			return
		}
		fmt.Fprint(w, `<html><body style="background:#143;color:white"><h1>Shared fixture</h1></body></html>`)
	}))
	defer upstream.Close()
	adapter := docker.WithExecution(func(ctx context.Context, command string, stdin []byte, env map[string]string) (string, error) {
		match := regexp.MustCompile(`BROWSER_ACT_INPUT=(\S+) BROWSER_ACT_SESSION=(\S+)`).FindStringSubmatch(command)
		if len(match) != 3 {
			return "", fmt.Errorf("unexpected command")
		}
		inputPath := strings.Replace(match[1], "/workspace", root, 1)
		data, err := os.ReadFile(inputPath)
		if err != nil {
			return "", err
		}
		var config map[string]any
		json.Unmarshal(data, &config)
		if config["url"] != "http://prism-server:8080/plugins/u7-board/weather.html" {
			t.Errorf("wrong source: %v", config["url"])
		}
		config["url"] = upstream.URL
		data, _ = json.Marshal(config)
		os.WriteFile(inputPath, data, 0600)
		script := strings.ReplaceAll(browserActScript, "/workspace", root)
		script = strings.Replace(script, "headless=True, args=args", "headless=True, executable_path=os.environ['PRISM_TEST_CHROMIUM'], args=args", 1)
		cmd := exec.CommandContext(ctx, python, "-")
		cmd.Stdin = strings.NewReader(script)
		cmd.Env = append(os.Environ(), "BROWSER_ACT_INPUT="+inputPath, "BROWSER_ACT_SESSION="+strings.Replace(match[2], "/workspace", root, 1))
		out, err := cmd.CombinedOutput()
		// Translate the fixture origin back to the workspace service origin.
		return strings.ReplaceAll(string(out), upstream.URL+"/", "http://prism-server:8080/plugins/u7-board/weather.html"), err
	})
	e := NewToolExecutor(adapter, root, filepath.Join(root, "plugins/u7-board"), "", "fixture-cookie")
	e.SetSessionID("u7-board")
	data, err := e.CaptureSharedPreview(t.Context(), memory.SharedWidget{ID: "weather", Cols: 2, Height: 320})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width != 760 || cfg.Height != 320 {
		t.Fatal(cfg, err)
	}
	reject.Store(true)
	if _, err := e.CaptureSharedPreview(t.Context(), memory.SharedWidget{ID: "weather", Cols: 2, Height: 320}); err == nil {
		t.Fatal("an HTTP error page was accepted as a widget preview")
	}
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".browser_session_") || strings.HasPrefix(entry.Name(), ".browser_act_") {
			t.Fatal("temporary preview state left behind", entry.Name())
		}
	}
	shots, _ := os.ReadDir(filepath.Join(root, ".screenshots"))
	if len(shots) != 0 {
		t.Fatal("temporary screenshots left behind")
	}
}

func TestAgentShareDescriptionAndNoBrowserFallback(t *testing.T) {
	ms := resourceDatabase(t)
	e, _, dir := newTestExecutorWithPlugins(t)
	e.docker = nil
	e.SetMemoryStore(ms)
	e.SetSharingContext(7, []memory.Membership{{GroupID: 1, GroupName: "Fixture"}})
	group, err := ms.CreateGroup(t.Context(), "Fixture")
	if err != nil {
		t.Fatal(err)
	}
	e.sharingGroups[0].GroupID = group.ID
	writeWidget(t, dir, "weather", "<p>Weather</p>")
	out, _, err := e.execute(t.Context(), "widget", []byte(`{"action":"share","id":"weather","description":"Weather at a glance"}`))
	if err != nil || !strings.Contains(out, "Preview unavailable") {
		t.Fatal(out, err)
	}
	items, err := ms.SharedItemsForGroups(t.Context(), []int64{group.ID}, "")
	if err != nil || len(items) != 1 || items[0].Description != "Weather at a glance" || items[0].HasPreview {
		t.Fatal(items, err)
	}
}
