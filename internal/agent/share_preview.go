package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"image/png"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"prism/internal/memory"
	"prism/workspaceexec"
)

// CaptureSharedPreview needs no model or vision provider. The returned JPEG is
// stored with the publication, rather than linking to the author's workspace.
// A separate browser session avoids changing the agent's cookies/input files.
func (e *ToolExecutor) CaptureSharedPreview(ctx context.Context, widget memory.SharedWidget) ([]byte, error) {
	if e.docker == nil || widget.ID == "" {
		return nil, fmt.Errorf("preview browser unavailable")
	}
	ctx, cancel := context.WithTimeout(workspaceexec.WithScope(ctx, e.sessionID), 25*time.Second)
	defer cancel()
	runner := NewToolExecutor(e.docker, e.workspaceDir, e.pluginDir, "", e.prismToken)
	runner.sessionID = e.sessionID + "-share-" + workspaceexec.ID()[:12]
	defer runner.removeManaged(filepath.Join(e.workspaceDir, ".browser_session_"+runner.sessionID+".json"))
	width := 480
	if widget.Cols == 2 {
		width = 760
	} else if widget.Cols >= 3 {
		width = 1000
	}
	height := max(180, min(widget.Height, 720))
	widgetURL := "http://prism-server:8080/plugins/" + url.PathEscape(e.sessionID) + "/" + url.PathEscape(widget.ID) + ".html"
	result, err := runner.browserActAuthViewport(ctx, widgetURL, []interface{}{
		map[string]interface{}{"type": "wait_idle", "ms": 4000},
		map[string]interface{}{"type": "wait_ms", "ms": 300},
		map[string]interface{}{"type": "screenshot"},
	}, e.prismToken, map[string]int{"width": width, "height": height})
	if err != nil {
		return nil, err
	}
	var actions []struct {
		Action     string `json:"action"`
		Status     string `json:"status"`
		URL        string `json:"url"`
		HTTPStatus int    `json:"http_status"`
	}
	if json.Unmarshal([]byte(result), &actions) != nil {
		return nil, fmt.Errorf("preview capture failed")
	}
	reachedWidget := false
	for _, a := range actions {
		if a.Action == "navigate" {
			reachedWidget = a.Status == "ok" && a.URL == widgetURL && a.HTTPStatus >= 200 && a.HTTPStatus < 400
		}
	}
	for _, a := range actions {
		if a.Action != "screenshot" || a.Status != "ok" || !strings.HasPrefix(a.URL, "/screenshots/") {
			continue
		}
		name := strings.TrimPrefix(a.URL, "/screenshots/")
		if name == "" || filepath.Base(name) != name {
			return nil, fmt.Errorf("invalid preview path")
		}
		path := filepath.Join(e.workspaceDir, ".screenshots", name)
		defer runner.removeManaged(path)
		if !reachedWidget {
			return nil, fmt.Errorf("preview did not reach the widget")
		}
		data, err := runner.readManagedFile(path)
		if err != nil {
			return nil, err
		}
		return sharedPreviewJPEG(data)
	}
	return nil, fmt.Errorf("preview capture unavailable")
}

func sharedPreviewJPEG(data []byte) ([]byte, error) {
	if len(data) > 8<<20 {
		return nil, fmt.Errorf("preview image too large")
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 1200 || cfg.Height > 900 {
		return nil, fmt.Errorf("invalid preview image")
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 80}); err != nil {
		return nil, err
	}
	if out.Len() > memory.MaxSharedPreviewBytes {
		return nil, fmt.Errorf("preview image too large")
	}
	return out.Bytes(), nil
}
