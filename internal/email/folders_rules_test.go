package email

import (
	"context"
	"crypto/tls"
	"net"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

func testMailbox(t *testing.T, move bool) Config {
	t.Helper()
	cert := httptest.NewTLSServer(nil)
	tlsCfg := cert.TLS.Clone()
	tlsCfg.NextProtos = []string{"imap"}
	cert.Close()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", tlsCfg)
	if err != nil {
		t.Fatal(err)
	}
	mem := imapmemserver.New()
	u := imapmemserver.NewUser("test", "test")
	mem.AddUser(u)
	for _, f := range []string{"INBOX", "Archive", "Trash"} {
		if err := u.Create(f, nil); err != nil {
			t.Fatal(err)
		}
	}
	caps := imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapUIDPlus: {}}
	if move {
		caps[imap.CapMove] = struct{}{}
	}
	srv := imapserver.New(&imapserver.Options{NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
		return mem.NewSession(), nil, nil
	}, Caps: caps})
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close(); ln.Close() })
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	pn, _ := strconv.Atoi(port)
	return Config{IMAPHost: host, IMAPPort: pn, User: "test", Pass: "test", Insecure: true}
}
func appendMail(t *testing.T, c Config, folder, subject string) uint32 {
	t.Helper()
	cl, err := c.dial()
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	body := "From: news@example.org\r\nTo: me@example.org\r\nSubject: " + subject + "\r\nDate: Sun, 20 Sep 2026 12:00:00 +0000\r\n\r\nHello\r\n"
	cmd := cl.Append(folder, int64(len(body)), nil)
	if _, err = cmd.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err = cmd.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := cmd.Wait()
	if err != nil {
		t.Fatal(err)
	}
	return uint32(data.UID)
}
func TestFolderUIDScopeAndMove(t *testing.T) {
	for _, move := range []bool{true, false} {
		t.Run(strconv.FormatBool(move), func(t *testing.T) {
			c := testMailbox(t, move)
			uid := appendMail(t, c, "INBOX", "Inbox message")
			other := appendMail(t, c, "Archive", "Unrelated message")
			arc := c
			arc.Folder = "Archive"
			m, err := arc.Read(other)
			if err != nil || m.Subject != "Unrelated message" || m.Folder != "Archive" {
				t.Fatalf("wrong folder: %+v %v", m, err)
			}
			// A rejected destination must not delete the source (including COPY fallback).
			if err = c.Move(uid, "Missing"); err == nil {
				t.Fatal("accepted missing folder")
			}
			if _, err = c.Read(uid); err != nil {
				t.Fatalf("lost source on failed move: %v", err)
			}
			if err = c.Move(uid, "Archive"); err != nil {
				t.Fatal(err)
			}
			if _, err = c.Read(uid); err == nil {
				t.Fatal("source remained after move")
			}
			rows, err := arc.List(20)
			if err != nil || len(rows) != 2 {
				t.Fatalf("destination: %+v %v", rows, err)
			}
			if err = arc.ManageFolder("delete_folder", "Archive", ""); err == nil {
				t.Fatal("deleted nonempty folder")
			}
			if err = c.ManageFolder("create_folder", "Work", ""); err != nil {
				t.Fatal(err)
			}
			if err = c.ManageFolder("rename_folder", "Work", "Projects"); err != nil {
				t.Fatal(err)
			}
			if err = c.ManageFolder("delete_folder", "Projects", ""); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestRulesPreviewApplyAndRerun(t *testing.T) {
	c := testMailbox(t, true)
	appendMail(t, c, "INBOX", "Newsletter")
	appendMail(t, c, "INBOX", "Invoice")
	rs := []Rule{{Name: "News", Field: "subject", Contains: "Newsletter", Action: "move", Target: "Archive", Enabled: true}}
	preview, err := c.RunRules(context.Background(), rs, true)
	if err != nil || len(preview) != 1 || preview[0].Matched != 1 || preview[0].Applied != 0 {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	inbox, _ := c.List(20)
	if len(inbox) != 2 {
		t.Fatal("preview mutated inbox")
	}
	result, err := c.RunRules(context.Background(), rs, false)
	if err != nil || result[0].Applied != 1 {
		t.Fatalf("apply: %+v %v", result, err)
	}
	result, err = c.RunRules(context.Background(), rs, false)
	if err != nil || result[0].Matched != 0 {
		t.Fatalf("rerun: %+v %v", result, err)
	}
	rs[0] = Rule{Name: "Read", Field: "from", Contains: "news@example.org", Action: "mark_read", Enabled: true}
	result, err = c.RunRules(context.Background(), rs, false)
	if err != nil || result[0].Applied != 1 {
		t.Fatalf("mark: %+v %v", result, err)
	}
	result, _ = c.RunRules(context.Background(), rs, false)
	if result[0].Matched != 0 {
		t.Fatal("mark-read rule does not drain")
	}
	rs[0].Enabled = false
	result, _ = c.RunRules(context.Background(), rs, false)
	if len(result) != 0 {
		t.Fatal("disabled rule ran")
	}
}

type ruleMemory map[string]string

func (m ruleMemory) GetConfig(_ context.Context, k string) (string, bool, error) {
	v, ok := m[k]
	return v, ok, nil
}
func (m ruleMemory) SetConfig(_ context.Context, k, v string) error { m[k] = v; return nil }
func TestRuleStoreAndValidation(t *testing.T) {
	ctx := context.Background()
	m := ruleMemory{}
	r := Rule{Name: "News", Field: "from", Contains: "example.org", Action: "move", Target: "Archive", Enabled: true}
	if err := SaveRule(ctx, m, r); err != nil {
		t.Fatal(err)
	}
	r.Enabled = false
	if err := SaveRule(ctx, m, r); err != nil {
		t.Fatal(err)
	}
	rules, _ := LoadRules(ctx, m)
	if len(rules) != 1 || rules[0].Enabled {
		t.Fatal(rules)
	}
	r.Contains = " "
	if err := SaveRule(ctx, m, r); err == nil {
		t.Fatal("empty matcher accepted")
	}
	if err := DeleteRule(ctx, m, "News"); err != nil {
		t.Fatal(err)
	}
	m[RulesKey] = "broken"
	if _, err := LoadRules(ctx, m); err == nil || !strings.Contains(err.Error(), "saved") {
		t.Fatal(err)
	}
}
