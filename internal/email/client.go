// Package email provides minimal IMAP (read/search) and SMTP (send) access for
// the agent's email tools. Connections are short-lived: each call dials, does
// its work and disconnects. Credentials come from the caller (the agent stores
// them in Prism's encrypted secrets + config).
package email

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/smtp"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	gomail "github.com/emersion/go-message/mail"
)

type Config struct {
	IMAPHost string
	IMAPPort int
	SMTPHost string
	SMTPPort int
	User     string
	Pass     string
	From     string // From address; defaults to User
	// Security selects the IMAP/SMTP transport: "" / "ssl" = implicit TLS
	// (default, ports 993/465); "starttls" = upgrade a plain connection
	// (ports 143/1143/587/1025). Insecure accepts a self-signed certificate —
	// required for Proton Mail Bridge.
	Security string
	Insecure bool
}

func (c Config) tlsConfig(host string) *tls.Config {
	return &tls.Config{ServerName: host, InsecureSkipVerify: c.Insecure}
}

func (c Config) from() string {
	if c.From != "" {
		return c.From
	}
	return c.User
}

type Message struct {
	UID       uint32    `json:"uid"`
	Subject   string    `json:"subject"`
	From      string    `json:"from"`
	To        string    `json:"to,omitempty"`  // comma-joined recipients (for reply-all)
	Cc        string    `json:"cc,omitempty"`
	Date      time.Time `json:"date"`
	Seen      bool      `json:"seen"`
	MessageID   string       `json:"messageId,omitempty"`
	Body        string       `json:"body,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Attachment is one file attached to a received message (metadata only; bytes
// are fetched on demand via Config.Attachment).
type Attachment struct {
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	Size        int    `json:"size"`
}

// OutAttachment is a file to attach to an outgoing message.
type OutAttachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// ─── IMAP ─────────────────────────────────────────────────────────────────────

func (c Config) dial() (*imapclient.Client, error) {
	if c.IMAPHost == "" {
		return nil, fmt.Errorf("IMAP host not configured")
	}
	port := c.IMAPPort
	if port == 0 {
		port = 993
	}
	addr := fmt.Sprintf("%s:%d", c.IMAPHost, port)
	opts := &imapclient.Options{TLSConfig: c.tlsConfig(c.IMAPHost)}
	var cl *imapclient.Client
	var err error
	if c.Security == "starttls" {
		cl, err = imapclient.DialStartTLS(addr, opts)
	} else {
		cl, err = imapclient.DialTLS(addr, opts)
	}
	if err != nil {
		return nil, fmt.Errorf("imap dial: %w", err)
	}
	if err := cl.Login(c.User, c.Pass).Wait(); err != nil {
		cl.Close()
		return nil, fmt.Errorf("imap login: %w", err)
	}
	return cl, nil
}

func addr(list []imap.Address) string {
	if len(list) == 0 {
		return ""
	}
	a := list[0]
	mail := a.Mailbox + "@" + a.Host
	if a.Name != "" {
		return fmt.Sprintf("%s <%s>", a.Name, mail)
	}
	return mail
}

func hasFlag(flags []imap.Flag, f imap.Flag) bool {
	for _, x := range flags {
		if x == f {
			return true
		}
	}
	return false
}

// msgDate prefers the envelope (header) date, falling back to the server's
// internal date.
func msgDate(m *imapclient.FetchMessageBuffer) time.Time {
	if m.Envelope != nil && !m.Envelope.Date.IsZero() {
		return m.Envelope.Date
	}
	return m.InternalDate
}

// fetchByUIDs loads the given UIDs and returns Messages in the same order as the
// uids slice (FETCH returns them in arbitrary order).
func (c Config) fetchByUIDs(cl *imapclient.Client, uids []imap.UID) ([]Message, error) {
	if len(uids) == 0 {
		return nil, nil
	}
	msgs, err := cl.Fetch(imap.UIDSetNum(uids...), &imap.FetchOptions{Envelope: true, Flags: true, UID: true}).Collect()
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	byUID := make(map[imap.UID]*imapclient.FetchMessageBuffer, len(msgs))
	for _, m := range msgs {
		byUID[m.UID] = m
	}
	out := make([]Message, 0, len(uids))
	for _, u := range uids {
		if m := byUID[u]; m != nil {
			out = append(out, toMessage(m))
		}
	}
	return out, nil
}

// List returns up to `limit` most recent messages in INBOX, newest first.
func (c Config) List(limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 20
	}
	cl, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer cl.Close()

	sel, err := cl.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return nil, fmt.Errorf("select inbox: %w", err)
	}
	if sel.NumMessages == 0 {
		return nil, nil
	}

	// Preferred: server-side SORT by date, newest first (when supported).
	if cl.Caps().Has(imap.CapSort) {
		nums, serr := cl.UIDSort(&imapclient.SortOptions{
			SearchCriteria: &imap.SearchCriteria{},
			SortCriteria:   []imapclient.SortCriterion{{Key: imapclient.SortKeyDate, Reverse: true}},
		}).Wait()
		if serr == nil && len(nums) > 0 {
			if len(nums) > limit {
				nums = nums[:limit]
			}
			uids := make([]imap.UID, len(nums))
			for i, n := range nums {
				uids[i] = imap.UID(n)
			}
			if out, ferr := c.fetchByUIDs(cl, uids); ferr == nil {
				return out, nil
			}
		}
	}

	// Fallback (Proton Bridge lacks SORT): IMAP sequence order is import order,
	// NOT date order — so the last N by sequence can be the OLDEST mails. Fetch
	// every message's date cheaply, sort by date, and keep the newest N.
	var all imap.SeqSet
	all.AddRange(1, sel.NumMessages)
	metas, err := cl.Fetch(all, &imap.FetchOptions{UID: true, InternalDate: true, Envelope: true, Flags: true}).Collect()
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	sort.Slice(metas, func(i, j int) bool { return msgDate(metas[i]).After(msgDate(metas[j])) })
	if len(metas) > limit {
		metas = metas[:limit]
	}
	out := make([]Message, 0, len(metas))
	for _, m := range metas {
		out = append(out, toMessage(m))
	}
	return out, nil
}

// UnreadCount returns the number of unseen messages in INBOX (a cheap STATUS,
// no message fetch).
func (c Config) UnreadCount() (int, error) {
	cl, err := c.dial()
	if err != nil {
		return 0, err
	}
	defer cl.Close()
	data, err := cl.Status("INBOX", &imap.StatusOptions{NumUnseen: true}).Wait()
	if err != nil {
		return 0, err
	}
	if data.NumUnseen != nil {
		return int(*data.NumUnseen), nil
	}
	return 0, nil
}

// Read fetches a single message (by UID) including its plain-text body.
func (c Config) Read(uid uint32) (Message, error) {
	cl, err := c.dial()
	if err != nil {
		return Message{}, err
	}
	defer cl.Close()
	if _, err := cl.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return Message{}, fmt.Errorf("select inbox: %w", err)
	}

	uidSet := imap.UIDSetNum(imap.UID(uid))
	opts := &imap.FetchOptions{
		Envelope:    true,
		Flags:       true,
		UID:         true,
		BodySection: []*imap.FetchItemBodySection{{}},
	}
	msgs, err := cl.Fetch(uidSet, opts).Collect()
	if err != nil {
		return Message{}, fmt.Errorf("fetch: %w", err)
	}
	if len(msgs) == 0 {
		return Message{}, fmt.Errorf("message uid %d not found", uid)
	}
	m := toMessage(msgs[0])
	if raw := msgs[0].FindBodySection(&imap.FetchItemBodySection{}); raw != nil {
		m.Body, m.Attachments = parseBody(raw)
	}
	return m, nil
}

// Attachment fetches the bytes of the index-th attachment of a message.
func (c Config) Attachment(uid uint32, index int) (filename, contentType string, data []byte, err error) {
	cl, err := c.dial()
	if err != nil {
		return "", "", nil, err
	}
	defer cl.Close()
	if _, err = cl.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return "", "", nil, fmt.Errorf("select inbox: %w", err)
	}
	uidSet := imap.UIDSetNum(imap.UID(uid))
	msgs, err := cl.Fetch(uidSet, &imap.FetchOptions{BodySection: []*imap.FetchItemBodySection{{}}}).Collect()
	if err != nil {
		return "", "", nil, fmt.Errorf("fetch: %w", err)
	}
	if len(msgs) == 0 {
		return "", "", nil, fmt.Errorf("message uid %d not found", uid)
	}
	raw := msgs[0].FindBodySection(&imap.FetchItemBodySection{})
	if raw == nil {
		return "", "", nil, fmt.Errorf("no body")
	}
	mr, err := gomail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		return "", "", nil, err
	}
	n := 0
	for {
		p, e := mr.NextPart()
		if e == io.EOF || e != nil {
			break
		}
		if h, ok := p.Header.(*gomail.AttachmentHeader); ok {
			if n == index {
				fn, _ := h.Filename()
				ct, _, _ := h.ContentType()
				b, _ := io.ReadAll(p.Body)
				if fn == "" {
					fn = "attachment"
				}
				if ct == "" {
					ct = "application/octet-stream"
				}
				return fn, ct, b, nil
			}
			n++
		}
	}
	return "", "", nil, fmt.Errorf("attachment %d not found", index)
}

// Search runs a server-side text search and returns matching messages (newest
// first), capped at `limit`.
func (c Config) Search(query string, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 20
	}
	cl, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer cl.Close()
	if _, err := cl.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return nil, fmt.Errorf("select inbox: %w", err)
	}

	data, err := cl.UIDSearch(&imap.SearchCriteria{Text: []string{query}}, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	uids := data.AllUIDs()
	if len(uids) == 0 {
		return nil, nil
	}
	if len(uids) > limit {
		uids = uids[len(uids)-limit:] // most recent UIDs
	}
	msgs, err := cl.Fetch(imap.UIDSetNum(uids...), &imap.FetchOptions{Envelope: true, Flags: true, UID: true}).Collect()
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, toMessage(m))
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func toMessage(m *imapclient.FetchMessageBuffer) Message {
	msg := Message{UID: uint32(m.UID), Seen: hasFlag(m.Flags, imap.FlagSeen)}
	if m.Envelope != nil {
		msg.Subject = m.Envelope.Subject
		msg.From = addr(m.Envelope.From)
		msg.To = addrList(m.Envelope.To)
		msg.Cc = addrList(m.Envelope.Cc)
		msg.Date = m.Envelope.Date
		msg.MessageID = m.Envelope.MessageID
	}
	return msg
}

// addrList joins a list of addresses as bare "user@host", comma-separated.
func addrList(list []imap.Address) string {
	parts := make([]string, 0, len(list))
	for _, a := range list {
		if a.Mailbox != "" && a.Host != "" {
			parts = append(parts, a.Mailbox+"@"+a.Host)
		}
	}
	return strings.Join(parts, ", ")
}

// emailRE extracts bare addresses from a "Name <a@b>, c@d" header value.
var emailRE = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)

func extractEmails(s string) []string {
	found := emailRE.FindAllString(s, -1)
	seen := map[string]bool{}
	var out []string
	for _, e := range found {
		le := strings.ToLower(e)
		if !seen[le] {
			seen[le] = true
			out = append(out, e)
		}
	}
	return out
}

// SetSeen marks a message read (\Seen) or unread on the server.
func (c Config) SetSeen(uid uint32, seen bool) error {
	cl, err := c.dial()
	if err != nil {
		return err
	}
	defer cl.Close()
	if _, err := cl.Select("INBOX", nil).Wait(); err != nil {
		return fmt.Errorf("select inbox: %w", err)
	}
	op := imap.StoreFlagsAdd
	if !seen {
		op = imap.StoreFlagsDel
	}
	return cl.Store(imap.UIDSetNum(imap.UID(uid)), &imap.StoreFlags{
		Op:    op,
		Flags: []imap.Flag{imap.FlagSeen},
	}, nil).Close()
}

// extractText pulls the text/plain part out of a raw RFC822 message, falling
// back to the raw bytes if MIME parsing fails.
// parseBody walks a message's MIME parts, returning the best text body and the
// list of attachments (metadata only).
func parseBody(raw []byte) (string, []Attachment) {
	mr, err := gomail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		return string(raw), nil
	}
	var text, htmlFallback strings.Builder
	var atts []Attachment
	for {
		p, err := mr.NextPart()
		if err == io.EOF || err != nil {
			break
		}
		switch h := p.Header.(type) {
		case *gomail.InlineHeader:
			ct, _, _ := h.ContentType()
			b, _ := io.ReadAll(p.Body)
			switch {
			case strings.HasPrefix(ct, "text/plain"):
				text.Write(b)
			case strings.HasPrefix(ct, "text/html"):
				htmlFallback.Write(b)
			}
		case *gomail.AttachmentHeader:
			fn, _ := h.Filename()
			ct, _, _ := h.ContentType()
			b, _ := io.ReadAll(p.Body)
			if fn == "" {
				fn = "attachment"
			}
			atts = append(atts, Attachment{Filename: fn, ContentType: ct, Size: len(b)})
		}
	}
	if text.Len() > 0 {
		return text.String(), atts
	}
	if htmlFallback.Len() > 0 {
		return htmlFallback.String(), atts
	}
	return string(raw), atts
}

// ─── SMTP ─────────────────────────────────────────────────────────────────────

// Send sends a plain-text email. inReplyTo (a Message-ID) is optional and, when
// set, threads the message as a reply.
// b64wrap base64-encodes data into 76-char CRLF-wrapped lines (RFC 2045).
func b64wrap(data []byte) string {
	enc := base64.StdEncoding.EncodeToString(data)
	var sb strings.Builder
	for i := 0; i < len(enc); i += 76 {
		end := i + 76
		if end > len(enc) {
			end = len(enc)
		}
		sb.WriteString(enc[i:end])
		sb.WriteString("\r\n")
	}
	return sb.String()
}

// sanitizeHeaderVal strips characters that could break a MIME header (CR/LF and
// double quotes) from a value such as a filename.
func sanitizeHeaderVal(s string) string {
	return strings.NewReplacer("\r", "", "\n", "", "\"", "'").Replace(s)
}

func (c Config) Send(to, subject, body, inReplyTo string, atts []OutAttachment) error {
	if c.SMTPHost == "" {
		return fmt.Errorf("SMTP host not configured")
	}
	port := c.SMTPPort
	if port == 0 {
		port = 587
	}
	from := c.from()

	// CRLF-strip every header value built from a caller-supplied string, same
	// as attachment filenames already are (sanitizeHeaderVal) — otherwise a
	// "\r\n" in a subject/recipient/reply-id injects arbitrary extra headers
	// into the outgoing message.
	to = sanitizeHeaderVal(to)
	subject = sanitizeHeaderVal(subject)
	inReplyTo = sanitizeHeaderVal(inReplyTo)

	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\n", from)
	fmt.Fprintf(&msg, "To: %s\r\n", to)
	fmt.Fprintf(&msg, "Subject: %s\r\n", subject)
	fmt.Fprintf(&msg, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&msg, "Message-ID: <%d@%s>\r\n", time.Now().UnixNano(), c.SMTPHost)
	if inReplyTo != "" {
		fmt.Fprintf(&msg, "In-Reply-To: %s\r\n", inReplyTo)
		fmt.Fprintf(&msg, "References: %s\r\n", inReplyTo)
	}
	msg.WriteString("MIME-Version: 1.0\r\n")
	if len(atts) == 0 {
		msg.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
		msg.WriteString(body)
	} else {
		boundary := fmt.Sprintf("prism%d", time.Now().UnixNano())
		fmt.Fprintf(&msg, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n", boundary)
		fmt.Fprintf(&msg, "--%s\r\n", boundary)
		msg.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
		msg.WriteString(body)
		msg.WriteString("\r\n")
		for _, a := range atts {
			ct := a.ContentType
			if ct == "" {
				ct = "application/octet-stream"
			}
			fn := sanitizeHeaderVal(a.Filename)
			fmt.Fprintf(&msg, "--%s\r\n", boundary)
			fmt.Fprintf(&msg, "Content-Type: %s; name=%q\r\n", ct, fn)
			fmt.Fprintf(&msg, "Content-Disposition: attachment; filename=%q\r\n", fn)
			msg.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
			msg.WriteString(b64wrap(a.Data))
			msg.WriteString("\r\n")
		}
		fmt.Fprintf(&msg, "--%s--\r\n", boundary)
	}

	auth := smtp.PlainAuth("", c.User, c.Pass, c.SMTPHost)
	serverAddr := fmt.Sprintf("%s:%d", c.SMTPHost, port)

	// `to` may be a "Name <a@b>, c@d" list — Rcpt needs the bare addresses.
	rcpts := extractEmails(to)
	if len(rcpts) == 0 {
		return fmt.Errorf("no valid recipient address in %q", to)
	}

	switch {
	case port == 465:
		return c.sendImplicitTLS(serverAddr, auth, from, rcpts, msg.Bytes())
	case c.Insecure || c.Security == "starttls":
		// STARTTLS, accepting a self-signed cert (Proton Mail Bridge).
		return c.sendStartTLS(serverAddr, auth, from, rcpts, msg.Bytes())
	default:
		return smtp.SendMail(serverAddr, auth, from, rcpts, msg.Bytes())
	}
}

func smtpDeliver(cl *smtp.Client, auth smtp.Auth, from string, rcpts []string, msg []byte) error {
	if err := cl.Auth(auth); err != nil {
		return fmt.Errorf("smtp auth: %w", err)
	}
	if err := cl.Mail(from); err != nil {
		return err
	}
	for _, r := range rcpts {
		if err := cl.Rcpt(r); err != nil {
			return err
		}
	}
	wc, err := cl.Data()
	if err != nil {
		return err
	}
	if _, err := wc.Write(msg); err != nil {
		return err
	}
	return wc.Close()
}

// sendStartTLS opens a plain connection, upgrades it with STARTTLS (honouring
// c.Insecure for self-signed certs), authenticates and sends.
func (c Config) sendStartTLS(serverAddr string, auth smtp.Auth, from string, rcpts []string, msg []byte) error {
	conn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	cl, err := smtp.NewClient(conn, c.SMTPHost)
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}
	defer cl.Quit()
	if err := cl.StartTLS(c.tlsConfig(c.SMTPHost)); err != nil {
		return fmt.Errorf("smtp starttls: %w", err)
	}
	return smtpDeliver(cl, auth, from, rcpts, msg)
}

// sendImplicitTLS handles port 465 (TLS from the first byte), which net/smtp's
// SendMail (STARTTLS) does not cover.
func (c Config) sendImplicitTLS(serverAddr string, auth smtp.Auth, from string, rcpts []string, msg []byte) error {
	conn, err := tls.Dial("tcp", serverAddr, c.tlsConfig(c.SMTPHost))
	if err != nil {
		return fmt.Errorf("smtp tls dial: %w", err)
	}
	cl, err := smtp.NewClient(conn, c.SMTPHost)
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}
	defer cl.Quit()
	return smtpDeliver(cl, auth, from, rcpts, msg)
}
