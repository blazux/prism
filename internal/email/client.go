// Package email provides minimal IMAP (read/search) and SMTP (send) access for
// the agent's email tools. Connections are short-lived: each call dials, does
// its work and disconnects. Credentials come from the caller (the agent stores
// them in Prism's encrypted secrets + config).
package email

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/smtp"
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
	Date      time.Time `json:"date"`
	Seen      bool      `json:"seen"`
	MessageID string    `json:"messageId,omitempty"`
	Body      string    `json:"body,omitempty"`
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
	to := sel.NumMessages
	from := uint32(1)
	if to > uint32(limit) {
		from = to - uint32(limit) + 1
	}
	var seq imap.SeqSet
	seq.AddRange(from, to)

	msgs, err := cl.Fetch(seq, &imap.FetchOptions{Envelope: true, Flags: true, UID: true}).Collect()
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, toMessage(m))
	}
	// Newest first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
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
		m.Body = extractText(raw)
	}
	return m, nil
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
		msg.Date = m.Envelope.Date
		msg.MessageID = m.Envelope.MessageID
	}
	return msg
}

// extractText pulls the text/plain part out of a raw RFC822 message, falling
// back to the raw bytes if MIME parsing fails.
func extractText(raw []byte) string {
	mr, err := gomail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		return string(raw)
	}
	var text, htmlFallback strings.Builder
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		if h, ok := p.Header.(*gomail.InlineHeader); ok {
			ct, _, _ := h.ContentType()
			b, _ := io.ReadAll(p.Body)
			switch {
			case strings.HasPrefix(ct, "text/plain"):
				text.Write(b)
			case strings.HasPrefix(ct, "text/html"):
				htmlFallback.Write(b)
			}
		}
	}
	if text.Len() > 0 {
		return text.String()
	}
	if htmlFallback.Len() > 0 {
		return htmlFallback.String()
	}
	return string(raw)
}

// ─── SMTP ─────────────────────────────────────────────────────────────────────

// Send sends a plain-text email. inReplyTo (a Message-ID) is optional and, when
// set, threads the message as a reply.
func (c Config) Send(to, subject, body, inReplyTo string) error {
	if c.SMTPHost == "" {
		return fmt.Errorf("SMTP host not configured")
	}
	port := c.SMTPPort
	if port == 0 {
		port = 587
	}
	from := c.from()

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
	msg.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	msg.WriteString("\r\n")
	msg.WriteString(body)

	auth := smtp.PlainAuth("", c.User, c.Pass, c.SMTPHost)
	serverAddr := fmt.Sprintf("%s:%d", c.SMTPHost, port)

	switch {
	case port == 465:
		return c.sendImplicitTLS(serverAddr, auth, from, to, msg.Bytes())
	case c.Insecure || c.Security == "starttls":
		// STARTTLS, accepting a self-signed cert (Proton Mail Bridge).
		return c.sendStartTLS(serverAddr, auth, from, to, msg.Bytes())
	default:
		return smtp.SendMail(serverAddr, auth, from, []string{to}, msg.Bytes())
	}
}

// sendStartTLS opens a plain connection, upgrades it with STARTTLS (honouring
// c.Insecure for self-signed certs), authenticates and sends.
func (c Config) sendStartTLS(serverAddr string, auth smtp.Auth, from, to string, msg []byte) error {
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
	if err := cl.Auth(auth); err != nil {
		return fmt.Errorf("smtp auth: %w", err)
	}
	if err := cl.Mail(from); err != nil {
		return err
	}
	if err := cl.Rcpt(to); err != nil {
		return err
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

// sendImplicitTLS handles port 465 (TLS from the first byte), which net/smtp's
// SendMail (STARTTLS) does not cover.
func (c Config) sendImplicitTLS(serverAddr string, auth smtp.Auth, from, to string, msg []byte) error {
	conn, err := tls.Dial("tcp", serverAddr, &tls.Config{ServerName: c.SMTPHost})
	if err != nil {
		return fmt.Errorf("smtp tls dial: %w", err)
	}
	cl, err := smtp.NewClient(conn, c.SMTPHost)
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}
	defer cl.Quit()
	if err := cl.Auth(auth); err != nil {
		return fmt.Errorf("smtp auth: %w", err)
	}
	if err := cl.Mail(from); err != nil {
		return err
	}
	if err := cl.Rcpt(to); err != nil {
		return err
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
