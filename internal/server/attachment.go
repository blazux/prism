package server

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"prism/internal/rag"
)

// attachmentMaxBytes caps a download, matching the 10 MB web upload limit — a
// chat channel handing the agent a 500 MB file helps no one and risks the box.
const attachmentMaxBytes = 10 << 20

// downloadAttachment fetches a file a chat channel was handed as a URL. Every
// channel authenticates the same way (a bearer token) and differs only in how it
// names the file, so the caller passes a fallback name and we upgrade it from the
// response's Content-Disposition when the server offers one (Webex does; it
// returns opaque content URLs with the real filename only in that header).
func downloadAttachment(ctx context.Context, url, token, fallbackName string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, "", err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, "", fmt.Errorf("download %s: status %d", url, resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, attachmentMaxBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > attachmentMaxBytes {
		return nil, "", fmt.Errorf("attachment exceeds %d MB", attachmentMaxBytes>>20)
	}

	name := fallbackName
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if _, params, perr := mime.ParseMediaType(cd); perr == nil {
			if fn := params["filename"]; fn != "" {
				name = fn
			}
		}
	}
	if name == "" {
		name = "attachment"
	}
	return data, name, nil
}

// Shared attachment handling for every channel. The web chat, Webex, Slack and
// Telegram all end up doing the same thing with a file: extract its text if we
// can, save the real bytes into the workspace, and hand the agent a preamble
// that says what it's looking at and how to open it. This used to live inline in
// the web upload handler and the WebSocket send path; the chat channels need the
// exact same behaviour, so it lives here once.

const attachmentMaxChars = 100_000

// attachment is a file offered to the agent: its name, the text we could pull
// out (empty when the agent should open the real file instead), and the
// workspace-relative path where the bytes were saved.
type attachment struct {
	Name string
	Text string
	Path string
}

// ingestAttachment turns raw bytes into an attachment: parse for inlineable
// text, blank it for the cases the agent must open itself (HTML pages, unknown
// binaries), cap it, and always persist the file so a tool can reach it.
func (s *Server) ingestAttachment(data []byte, filename string) attachment {
	ext := strings.ToLower(filepath.Ext(filename))

	tmp, err := os.CreateTemp("", "prism-attach-*"+ext)
	if err != nil {
		return attachment{Name: filename}
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return attachment{Name: filename}
	}
	tmp.Close()

	text, err := rag.ParseFile(tmp.Name())
	if err != nil {
		text = ""
	}
	// HTML: ParseFile "succeeds" by handing back markup, so a script-drawn page
	// inlines as minified JS. Blank it — the agent renders the saved file instead.
	if ext == ".html" || ext == ".htm" {
		text = ""
	}
	// Any binary ParseFile doesn't know reads whole via its default branch and
	// comes back as noise. Blank it so it's saved, not inlined.
	if looksBinary(text) {
		text = ""
	}
	if len(text) > attachmentMaxChars {
		text = text[:attachmentMaxChars] + "\n[... truncated at 100 000 characters ...]"
	}

	return attachment{
		Name: filename,
		Text: text,
		Path: s.saveChatUpload(tmp.Name(), filename),
	}
}

// attachmentPreamble is the block prepended to a message for one file: a header
// naming it, then either the inlined text or an instruction telling the agent how
// to open the real file (render an HTML page, run the pcap tool, read a scan…).
func attachmentPreamble(a attachment) string {
	hdr := "=== Attached file: " + a.Name
	if a.Path != "" {
		hdr += fmt.Sprintf(" (saved in the workspace at %s — open the real file when the text below is not enough, e.g. to OCR a scanned PDF or parse a spreadsheet)", a.Path)
	}
	hdr += " ==="

	body := a.Text
	if strings.TrimSpace(body) == "" {
		switch ext := strings.ToLower(filepath.Ext(a.Name)); {
		case a.Path == "":
			body = "[No text could be extracted from this file.]"
		case ext == ".pcap" || ext == ".pcapng":
			// The `pcap` custom tool exists for exactly this; without the pointer
			// the agent reaches for shell hexdumps.
			body = "[This is a network capture, saved at " + a.Path + ". Use the `pcap` tool on that path — " +
				"start with mode=calls to list the SIP calls, then mode=messages with a call_id to read them verbatim. " +
				"Never dump it with the shell.]"
		case ext == ".html" || ext == ".htm":
			// A script-drawn page has no readable source, so render it — and the
			// rendered text is truncated and hides tooltip/JS state, so the grep and
			// script routes are spelled out too. The agent, told only to render, once
			// concluded the file "loads its data dynamically" and gave up.
			url := "file:///workspace/" + a.Path
			path := "/workspace/" + a.Path
			body = "[This is an HTML page. It is self-contained: never say you need a server.\n" +
				"- To see it: `browser_get` with url \"" + url + "\" returns the page as a reader sees it (truncated at 8000 chars).\n" +
				"- For detail the page hides in tooltips or JS state: `browser_get` on the same url with a `script` reading a global " +
				"(an exported report keeps its dataset in one; `Object.keys(window)` finds it).\n" +
				"- To search or extract exhaustively: it is still a file at " + path + ". `grep -c Diversion \"" + path + "\"` answers " +
				"\"is this header present\" in one call, and a JS literal like `var data = ({...})` parses with a few lines of python. " +
				"Prefer this over guessing regexes against rendered HTML.]"
		default:
			body = "[No text was extracted automatically — read the file at " + a.Path + " to process it yourself.]"
		}
	}
	return hdr + "\n" + body
}

// prependAttachments folds a set of ingested files into the message text a chat
// channel is about to send to the agent, newest-first like the web path.
func prependAttachments(text string, atts []attachment) string {
	for _, a := range atts {
		text = attachmentPreamble(a) + "\n\n" + text
	}
	return text
}
