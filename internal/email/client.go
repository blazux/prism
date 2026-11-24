// Package email provides minimal IMAP (read/search) and SMTP (send) access for
// the agent's email tools. Connections are short-lived: each call dials, does
// its work and disconnects. Credentials come from the caller (the agent stores
// them in Prism's encrypted secrets + config).
package email

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	stdmail "net/mail"
	"net/smtp"
	"net/textproto"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	gomessage "github.com/emersion/go-message"
	// Registers the decoders for every charset that is not UTF-8 or US-ASCII.
	// Without this import the library has no CharsetReader at all, so a plain
	// iso-8859-1 or windows-1252 message — ordinary French mail — failed to
	// decode and the whole body was lost.
	"github.com/emersion/go-message/charset"
	gomail "github.com/emersion/go-message/mail"
)

type Config struct {
	Folder   string // request scope; empty means INBOX
	Offset   int    // list pagination
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
	BodyIsHTML  bool         `json:"-"` // MIME format, used only for agent-readable conversion.
	Folder      string       `json:"folder"`
	UID         uint32       `json:"uid"`
	Subject     string       `json:"subject"`
	From        string       `json:"from"`
	To          string       `json:"to,omitempty"` // comma-joined recipients (for reply-all)
	Cc          string       `json:"cc,omitempty"`
	Date        time.Time    `json:"date"`
	Seen        bool         `json:"seen"`
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
	opts := &imapclient.Options{
		TLSConfig: c.tlsConfig(c.IMAPHost),
		// Without a CharsetReader, a subject encoded in anything but utf-8,
		// us-ascii or latin-1 came back as its raw =?windows-1252?Q?…?= form.
		WordDecoder: &mime.WordDecoder{CharsetReader: charset.Reader},
	}
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
	msgs, err := cl.Fetch(imap.UIDSetNum(uids...), &imap.FetchOptions{Envelope: true, InternalDate: true, Flags: true, UID: true}).Collect()
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
			msg := toMessage(m)
			msg.Folder = c.mailbox()
			out = append(out, msg)
		}
	}
	return out, nil
}

// List returns up to `limit` most recent messages in INBOX, newest first.
func (c Config) List(limit int) ([]Message, error) {
	if limit > 500 {
		limit = 500
	}
	if c.Offset < 0 {
		c.Offset = 0
	}
	if limit <= 0 {
		limit = 20
	}
	cl, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer cl.Close()

	sel, err := cl.Select(c.mailbox(), &imap.SelectOptions{ReadOnly: true}).Wait()
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
			if c.Offset >= len(nums) {
				return []Message{}, nil
			}
			nums = nums[c.Offset:]
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
	if c.Offset >= len(metas) {
		return []Message{}, nil
	}
	metas = metas[c.Offset:]
	if len(metas) > limit {
		metas = metas[:limit]
	}
	out := make([]Message, 0, len(metas))
	for _, m := range metas {
		msg := toMessage(m)
		msg.Folder = c.mailbox()
		out = append(out, msg)
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
	data, err := cl.Status(c.mailbox(), &imap.StatusOptions{NumUnseen: true}).Wait()
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
	if _, err := cl.Select(c.mailbox(), &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return Message{}, fmt.Errorf("select inbox: %w", err)
	}

	uidSet := imap.UIDSetNum(imap.UID(uid))
	opts := &imap.FetchOptions{
		Envelope:     true,
		InternalDate: true,
		Flags:        true,
		UID:          true,
		BodySection:  []*imap.FetchItemBodySection{{}},
	}
	msgs, err := cl.Fetch(uidSet, opts).Collect()
	if err != nil {
		return Message{}, fmt.Errorf("fetch: %w", err)
	}
	if len(msgs) == 0 {
		return Message{}, fmt.Errorf("message uid %d not found", uid)
	}
	m := toMessage(msgs[0])
	m.Folder = c.mailbox()
	if raw := msgs[0].FindBodySection(&imap.FetchItemBodySection{}); raw != nil {
		m.Body, m.Attachments, m.BodyIsHTML = parseBodyContent(raw)
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
	if _, err = cl.Select(c.mailbox(), &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
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
	if err != nil && !usablePart(err) {
		return "", "", nil, err
	}
	if mr == nil {
		return "", "", nil, fmt.Errorf("message could not be parsed")
	}
	n := 0
	for {
		p, e := mr.NextPart()
		if e == io.EOF {
			break
		}
		if e != nil && !usablePart(e) {
			break
		}
		if p == nil {
			break
		}
		if h, ok := p.Header.(*gomail.AttachmentHeader); ok {
			if n == index {
				fn, _ := h.Filename()
				ct, _, _ := h.ContentType()
				b, rerr := io.ReadAll(p.Body)
				if rerr != nil {
					return "", "", nil, fmt.Errorf("attachment %d could not be read in full: %w", index, rerr)
				}
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
	if _, err := cl.Select(c.mailbox(), &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
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
	// Imported messages can have newer UIDs but older dates. Sort lightweight
	// metadata before applying the limit, rather than keeping the highest UIDs.
	msgs, err := cl.Fetch(imap.UIDSetNum(uids...), &imap.FetchOptions{Envelope: true, InternalDate: true, Flags: true, UID: true}).Collect()
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		msg := toMessage(m)
		msg.Folder = c.mailbox()
		out = append(out, msg)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date.Equal(out[j].Date) {
			return out[i].UID > out[j].UID
		}
		return out[i].Date.After(out[j].Date)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func toMessage(m *imapclient.FetchMessageBuffer) Message {
	msg := Message{UID: uint32(m.UID), Seen: hasFlag(m.Flags, imap.FlagSeen), Date: msgDate(m)}
	if m.Envelope != nil {
		msg.Subject = m.Envelope.Subject
		msg.From = addr(m.Envelope.From)
		msg.To = addrList(m.Envelope.To)
		msg.Cc = addrList(m.Envelope.Cc)
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
	if uid == 0 {
		return fmt.Errorf("uid from email list is required")
	}
	cl, err := c.dial()
	if err != nil {
		return err
	}
	defer cl.Close()
	if _, err := cl.Select(c.mailbox(), nil).Wait(); err != nil {
		return fmt.Errorf("select inbox: %w", err)
	}
	op := imap.StoreFlagsAdd
	msgs, err := cl.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{UID: true}).Collect()
	if err != nil {
		return err
	}
	if len(msgs) == 0 {
		return fmt.Errorf("message no longer exists; refresh the folder")
	}
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
// usablePart reports an error the library documents as non-fatal: the part (or
// the reader) is still returned and must be used. Treating those as fatal threw
// away the rest of the message, attachments included.
func usablePart(err error) bool {
	return gomessage.IsUnknownCharset(err) || gomessage.IsUnknownEncoding(err)
}

func parseBody(raw []byte) (string, []Attachment) {
	body, attachments, _ := parseBodyContent(raw)
	return body, attachments
}

func parseBodyContent(raw []byte) (string, []Attachment, bool) {
	mr, err := gomail.CreateReader(bytes.NewReader(raw))
	if err != nil && !usablePart(err) {
		return string(raw), nil, false
	}
	if mr == nil {
		return string(raw), nil, false
	}
	var text, htmlFallback strings.Builder
	var atts []Attachment
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil && !usablePart(err) {
			break
		}
		if p == nil {
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
		return text.String(), atts, false
	}
	if htmlFallback.Len() > 0 {
		return htmlFallback.String(), atts, true
	}
	return string(raw), atts, false
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

// Send retains the original API for existing callers.
func (c Config) Send(to, subject, body, inReplyTo string, atts []OutAttachment) error {
	return c.SendTo(to, "", "", subject, body, inReplyTo, atts)
}

func recipientList(value string) ([]*stdmail.Address, error) {
	if strings.ContainsAny(value, "\r\n\x00") {
		return nil, fmt.Errorf("recipient addresses cannot contain line breaks")
	}
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	list, err := stdmail.ParseAddressList(value)
	if err != nil {
		return nil, fmt.Errorf("invalid recipient address: %w", err)
	}
	return list, nil
}

// prepareMessage keeps envelope recipients separate from visible MIME headers.
// In particular, blind-copy recipients must never appear in message headers.
func (c Config) prepareMessage(to, cc, bcc, subject, body, inReplyTo string, atts []OutAttachment) (string, []string, []byte, error) {
	if strings.ContainsAny(c.from(), "\r\n\x00") {
		return "", nil, nil, fmt.Errorf("invalid sender address")
	}
	sender, err := stdmail.ParseAddress(c.from())
	if err != nil {
		return "", nil, nil, fmt.Errorf("invalid sender address: %w", err)
	}
	var lists [3][]*stdmail.Address
	var rcpts []string
	seen := map[string]bool{}
	for i, value := range []string{to, cc, bcc} {
		lists[i], err = recipientList(value)
		if err != nil {
			return "", nil, nil, err
		}
		for _, a := range lists[i] {
			key := strings.ToLower(a.Address)
			if !seen[key] {
				seen[key] = true
				rcpts = append(rcpts, a.Address)
			}
		}
	}
	if len(rcpts) == 0 {
		return "", nil, nil, fmt.Errorf("at least one recipient is required")
	}
	header := func(list []*stdmail.Address) string {
		values := make([]string, len(list))
		for i, a := range list {
			values[i] = a.String()
		}
		return strings.Join(values, ", ")
	}
	subject = sanitizeHeaderVal(subject)
	inReplyTo = sanitizeHeaderVal(inReplyTo)
	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\n", sender.String())
	if len(lists[0]) > 0 {
		fmt.Fprintf(&msg, "To: %s\r\n", header(lists[0]))
	}
	if len(lists[1]) > 0 {
		fmt.Fprintf(&msg, "Cc: %s\r\n", header(lists[1]))
	}
	fmt.Fprintf(&msg, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", subject))
	fmt.Fprintf(&msg, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&msg, "Message-ID: <%d@%s>\r\n", time.Now().UnixNano(), sanitizeHeaderVal(c.SMTPHost))
	if inReplyTo != "" {
		fmt.Fprintf(&msg, "In-Reply-To: %s\r\nReferences: %s\r\n", inReplyTo, inReplyTo)
	}
	msg.WriteString("MIME-Version: 1.0\r\n")
	if len(atts) == 0 {
		msg.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
		msg.WriteString(body)
	} else {
		boundary := fmt.Sprintf("prism%d", time.Now().UnixNano())
		fmt.Fprintf(&msg, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n", boundary)
		fmt.Fprintf(&msg, "--%s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n", boundary)
		msg.WriteString(body)
		msg.WriteString("\r\n")
		for _, a := range atts {
			ct := a.ContentType
			if ct == "" {
				ct = "application/octet-stream"
			}
			media, params, err := mime.ParseMediaType(ct)
			if err != nil {
				return "", nil, nil, fmt.Errorf("invalid attachment content type")
			}
			params["name"] = sanitizeHeaderVal(a.Filename)
			fmt.Fprintf(&msg, "--%s\r\nContent-Type: %s\r\n", boundary, mime.FormatMediaType(media, params))
			fmt.Fprintf(&msg, "Content-Disposition: %s\r\n", mime.FormatMediaType("attachment", map[string]string{"filename": sanitizeHeaderVal(a.Filename)}))
			msg.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
			msg.WriteString(b64wrap(a.Data))
			msg.WriteString("\r\n")
		}
		fmt.Fprintf(&msg, "--%s--\r\n", boundary)
	}
	return sender.Address, rcpts, msg.Bytes(), nil
}

// SendTo sends plain-text mail with separate To, Cc and Bcc recipients.
func (c Config) SendTo(to, cc, bcc, subject, body, inReplyTo string, atts []OutAttachment) error {
	if c.SMTPHost == "" {
		return fmt.Errorf("SMTP host not configured")
	}
	from, rcpts, msg, err := c.prepareMessage(to, cc, bcc, subject, body, inReplyTo, atts)
	if err != nil {
		return err
	}
	port := c.SMTPPort
	if port == 0 {
		port = 587
	}
	auth := smtp.PlainAuth("", c.User, c.Pass, c.SMTPHost)
	serverAddr := net.JoinHostPort(c.SMTPHost, fmt.Sprint(port))
	switch {
	case port == 465:
		return c.sendImplicitTLS(serverAddr, auth, from, rcpts, msg)
	case c.Insecure || c.Security == "starttls":
		return c.sendStartTLS(serverAddr, auth, from, rcpts, msg)
	default:
		return c.sendSMTP(serverAddr, auth, from, rcpts, msg)
	}
}

type deliveryUncertainError struct{ err error }

func (e *deliveryUncertainError) Error() string {
	return "delivery could not be confirmed; check Sent or contact the recipient before retrying: " + e.err.Error()
}
func (e *deliveryUncertainError) Unwrap() error { return e.err }

// DeliveryUncertain reports a failure while transferring or acknowledging DATA,
// when automatically repeating the message could deliver it twice.
func DeliveryUncertain(err error) bool {
	var uncertain *deliveryUncertainError
	return errors.As(err, &uncertain)
}

func (c Config) sendSMTP(serverAddr string, auth smtp.Auth, from string, rcpts []string, msg []byte) error {
	conn, err := net.DialTimeout("tcp", serverAddr, 20*time.Second)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	cl, err := smtp.NewClient(conn, c.SMTPHost)
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}
	defer cl.Quit()
	if ok, _ := cl.Extension("STARTTLS"); ok {
		if err := cl.StartTLS(c.tlsConfig(c.SMTPHost)); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}
	return smtpDeliver(cl, auth, from, rcpts, msg)
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
		return &deliveryUncertainError{err}
	}
	if err := wc.Close(); err != nil {
		var rejection *textproto.Error
		if errors.As(err, &rejection) && rejection.Code >= 400 {
			return fmt.Errorf("smtp message rejected: %w", err)
		}
		return &deliveryUncertainError{err}
	}
	return nil
}

// sendStartTLS opens a plain connection, upgrades it with STARTTLS (honouring
// c.Insecure for self-signed certs), authenticates and sends.
func (c Config) sendStartTLS(serverAddr string, auth smtp.Auth, from string, rcpts []string, msg []byte) error {
	conn, err := net.DialTimeout("tcp", serverAddr, 20*time.Second)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
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
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 20 * time.Second}, "tcp", serverAddr, c.tlsConfig(c.SMTPHost))
	if err != nil {
		return fmt.Errorf("smtp tls dial: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	cl, err := smtp.NewClient(conn, c.SMTPHost)
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}
	defer cl.Quit()
	return smtpDeliver(cl, auth, from, rcpts, msg)
}
