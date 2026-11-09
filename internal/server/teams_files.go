package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// User-supplied attachment URLs must not reach private hosts, or receive the
// bot's bearer token on storage URLs. Resolve and check every dial, including
// redirects, to avoid DNS rebinding of a superficially valid hostname.
func teamsFileHost(raw string) (host string, authenticated bool, ok bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Port() != "" && u.Port() != "443" {
		return "", false, false
	}
	h := strings.ToLower(u.Hostname())
	authenticated = strings.HasSuffix(h, ".asm.skype.com") || h == "smba.trafficmanager.net" || h == "smba.infra.teams.microsoft.com"
	storage := strings.HasSuffix(h, ".sharepoint.com") || strings.HasSuffix(h, ".sharepointonline.com")
	return h, authenticated, authenticated || storage
}

var teamsFileTransport = &http.Transport{
	Proxy: nil,
	DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if !ip.IP.IsGlobalUnicast() || ip.IP.IsPrivate() || ip.IP.IsLoopback() || ip.IP.IsLinkLocalUnicast() {
				continue
			}
			conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if err == nil {
				return conn, nil
			}
		}
		return nil, fmt.Errorf("attachment host has no reachable public address")
	},
	TLSHandshakeTimeout:   10 * time.Second,
	ResponseHeaderTimeout: 15 * time.Second,
}

func downloadTeamsFile(ctx context.Context, raw, token string, useBotToken bool) ([]byte, error) {
	_, authenticated, ok := teamsFileHost(raw)
	if !ok || useBotToken && !authenticated {
		return nil, fmt.Errorf("unsupported Teams attachment host")
	}
	client := &http.Client{Transport: teamsFileTransport, Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		_, auth, allowed := teamsFileHost(req.URL.String())
		if !allowed || len(via) >= 3 {
			return fmt.Errorf("unsupported Teams attachment redirect")
		}
		req.Header.Del("Authorization")
		// A redirect never grants the token to a different host.
		if useBotToken && auth && req.URL.Hostname() == via[0].URL.Hostname() {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	if useBotToken {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("attachment returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, attachmentMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > attachmentMaxBytes {
		return nil, fmt.Errorf("attachment exceeds 10 MB")
	}
	return data, nil
}

func teamsInlineImages(markup string) []string {
	var urls []string
	z := html.NewTokenizer(strings.NewReader(markup))
	for z.Next() != html.ErrorToken {
		t := z.Token()
		if t.Data != "img" {
			continue
		}
		for _, a := range t.Attr {
			if a.Key == "src" && len(urls) < 4 {
				urls = append(urls, a.Val)
			}
		}
	}
	return urls
}

func (m *teamsManager) attachments(ctx context.Context, cfg teamsConfig, secret string, a teamsActivity, text string) (string, []string) {
	var images []string
	type file struct {
		raw, name   string
		image, auth bool
	}
	var files []file
	for _, att := range a.Attachments {
		switch {
		case att.Type == "text/html":
			var markup string
			if json.Unmarshal(att.Content, &markup) != nil {
				markup = string(att.Content)
			}
			for _, raw := range teamsInlineImages(markup) {
				files = append(files, file{raw, "teams-image.png", true, true})
			}
		case strings.HasPrefix(att.Type, "image/"):
			files = append(files, file{att.URL, att.Name, true, true})
		case att.Type == "application/vnd.microsoft.teams.file.download.info" && a.Conversation.Type == "personal":
			var content struct {
				DownloadURL string `json:"downloadUrl"`
			}
			if json.Unmarshal(att.Content, &content) == nil {
				files = append(files, file{content.DownloadURL, att.Name, false, false})
			}
		default:
			text += "\n[This attachment was not imported. Stored files in Teams channels and group chats require a Microsoft Graph connection; ask the sender to paste the relevant content.]"
		}
	}
	seen := map[string]bool{}
	count := 0
	for _, f := range files {
		if seen[f.raw] {
			continue
		}
		seen[f.raw] = true
		count++
		if count > 4 {
			text += "\n[Only the first four attachments were imported.]"
			break
		}
		token := ""
		if f.auth {
			var err error
			token, err = m.accessToken(ctx, cfg, secret)
			if err != nil {
				text += "\n[An attached image could not be downloaded: Microsoft authentication failed.]"
				continue
			}
		}
		data, err := downloadTeamsFile(ctx, f.raw, token, f.auth)
		if err != nil {
			text += "\n[An attachment could not be downloaded. Ask the sender to resend it or paste its content.]"
			continue
		}
		name := sanitizeUploadName(f.name)
		if name == "" {
			name = "teams-attachment"
		}
		kind := http.DetectContentType(data)
		if strings.HasPrefix(kind, "image/") {
			encoded := base64.StdEncoding.EncodeToString(data)
			atts, err := m.s.saveChatImages([]string{encoded}, []string{name})
			if err != nil {
				text += "\n[An attached image could not be saved in the workspace.]"
				continue
			}
			for _, att := range atts {
				text += "\n" + attachmentPreamble(att)
			}
			images = append(images, encoded)
		} else if f.image {
			text += "\n[An image attachment did not contain supported image data.]"
		} else {
			text += "\n" + attachmentPreamble(m.s.ingestAttachment(data, name))
		}
	}
	return text, images
}
