package server

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"prism/internal/agent"
	"prism/internal/workspace"
)

type responseAttachment struct {
	Name string
	URL  string
}

type responseAttachments []responseAttachment

func (a *responseAttachments) tap(ev agent.Event) {
	if ev.Type != "attachment" || ev.Path == "" {
		return
	}
	for _, file := range *a {
		if file.URL == ev.Path {
			return
		}
	}
	*a = append(*a, responseAttachment{Name: ev.Title, URL: ev.Path})
}

func telegramVisionImage(data []byte) bool {
	switch http.DetectContentType(data) {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	}
	return false
}

// Resolve only immutable add_attachment snapshots belonging to this turn's
// session. Never fetch an arbitrary URL or open a model-supplied workspace path.
func responseAttachmentPath(session, rawURL string) (string, string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.IsAbs() || u.Host != "" || u.Path != "/api/chat/attachment" || u.Fragment != "" {
		return "", "", fmt.Errorf("invalid response attachment URL")
	}
	q := u.Query()
	id, name := q.Get("id"), q.Get("name")
	decoded, err := hex.DecodeString(id)
	if q.Get("session") != session || err != nil || len(decoded) != 16 || name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\r\n\x00") {
		return "", "", fmt.Errorf("invalid response attachment reference")
	}
	return agent.AttachmentFilePath(session, id, name), name, nil
}

func (s *Server) tgSendResponse(ctx context.Context, api string, chatID int64, session, text string, files responseAttachments) error {
	if err := s.tgSendText(ctx, api, chatID, text); err != nil {
		return err
	}
	var failures []error
	for _, file := range files {
		if err := s.tgSendAttachment(ctx, api, chatID, session, file); err != nil {
			failures = append(failures, fmt.Errorf("attachment %q: %w", file.Name, err))
		}
	}
	return errors.Join(failures...)
}

func (s *Server) tgSendAttachment(ctx context.Context, api string, chatID int64, session string, file responseAttachment) error {
	path, name, err := responseAttachmentPath(session, file.URL)
	if err != nil {
		return err
	}
	f, err := workspace.Open(s.cfg.WorkspaceDir, path)
	if err != nil {
		return fmt.Errorf("could not open response attachment")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("response attachment is not a regular file")
	}
	// Telegram's hosted Bot API accepts multipart documents up to 50 MB.
	if info.Size() > 50_000_000 {
		return fmt.Errorf("file exceeds Telegram's 50 MB limit")
	}
	head := make([]byte, 512)
	n, err := f.Read(head)
	if err != nil && err != io.EOF {
		return fmt.Errorf("could not read response attachment")
	}
	upload := func(method, field string) error {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		var framing bytes.Buffer
		writer := multipart.NewWriter(&framing)
		if err := writer.WriteField("chat_id", strconv.FormatInt(chatID, 10)); err != nil {
			return err
		}
		if _, err := writer.CreateFormFile(field, name); err != nil {
			return err
		}
		prefix := bytes.Clone(framing.Bytes())
		if err := writer.Close(); err != nil {
			return err
		}
		suffix := framing.Bytes()[len(prefix):]
		body := io.MultiReader(bytes.NewReader(prefix), io.LimitReader(f, info.Size()), bytes.NewReader(suffix))
		return tgPost(ctx, api+"/"+method, writer.FormDataContentType(), body, int64(len(prefix)+len(suffix))+info.Size())
	}
	// Telegram displays JPEG/PNG inline. Other formats stay downloadable files.
	mimeType := http.DetectContentType(head[:n])
	if (mimeType == "image/jpeg" || mimeType == "image/png") && info.Size() <= 10_000_000 {
		err := upload("sendPhoto", "photo")
		var rejected *telegramAPIError
		if !errors.As(err, &rejected) || rejected.Code != 400 {
			return err
		}
		// A valid image can exceed Telegram's aspect ratio/dimension limits.
		// Preserve the original as a document if sendPhoto rejects it.
	}
	return upload("sendDocument", "document")
}

type telegramAPIError struct {
	Code int
}

func (e *telegramAPIError) Error() string {
	return fmt.Sprintf("Telegram rejected delivery (code %d)", e.Code)
}

// Never return an HTTP error containing the bot token embedded in the URL.
func tgPost(ctx context.Context, endpoint, contentType string, body io.Reader, size int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return fmt.Errorf("invalid Telegram delivery request")
	}
	req.Header.Set("Content-Type", contentType)
	req.ContentLength = size
	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		return fmt.Errorf("Telegram delivery request failed")
	}
	defer resp.Body.Close()
	var result struct {
		OK   bool `json:"ok"`
		Code int  `json:"error_code"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return fmt.Errorf("invalid Telegram delivery response")
	}
	if resp.StatusCode/100 != 2 || !result.OK {
		if result.Code == 0 {
			result.Code = resp.StatusCode
		}
		return &telegramAPIError{Code: result.Code}
	}
	return nil
}
