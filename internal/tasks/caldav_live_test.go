package tasks

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"prism/internal/caldav"
)

// Same disposable server as the calendar live tests, never a real account:
//
//	PRISM_TEST_CALDAV_URL=http://127.0.0.1:5232 PRISM_TEST_CALDAV_USER=eval \
//	PRISM_TEST_CALDAV_PASS=eval go test ./internal/tasks/ -run Live
func liveProvider(t *testing.T) (*CalDAVProvider, string) {
	t.Helper()
	url := os.Getenv("PRISM_TEST_CALDAV_URL")
	if url == "" {
		t.Skip("needs a disposable CalDAV server (PRISM_TEST_CALDAV_URL)")
	}
	user, pass := os.Getenv("PRISM_TEST_CALDAV_USER"), os.Getenv("PRISM_TEST_CALDAV_PASS")
	collection := "/" + user + "/agenda/"
	return &CalDAVProvider{cfg: caldav.Config{URL: url, User: user, Pass: pass, EventPath: collection, TaskPath: collection}}, collection
}

func putTodo(t *testing.T, path, body string) {
	t.Helper()
	req, err := http.NewRequest("PUT", os.Getenv("PRISM_TEST_CALDAV_URL")+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth(os.Getenv("PRISM_TEST_CALDAV_USER"), os.Getenv("PRISM_TEST_CALDAV_PASS"))
	req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		t.Fatalf("PUT %s: HTTP %d", path, resp.StatusCode)
	}
}

func TestLiveCancelledTaskAndRepeatingTask(t *testing.T) {
	p, collection := liveProvider(t)
	ctx := context.Background()

	cancelled := collection + "prism-live-cancelled.ics"
	putTodo(t, cancelled, "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\n"+
		"BEGIN:VTODO\r\nUID:prism-live-cancelled\r\nDTSTAMP:20260101T000000Z\r\n"+
		"SUMMARY:Reserver la salle\r\nSTATUS:CANCELLED\r\nEND:VTODO\r\nEND:VCALENDAR\r\n")
	t.Cleanup(func() { _ = p.Delete(ctx, cancelled) })

	bins := collection + "prism-live-bins.ics"
	putTodo(t, bins, "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\n"+
		"BEGIN:VTODO\r\nUID:prism-live-bins\r\nDTSTAMP:20260101T000000Z\r\nDUE:20260106T080000Z\r\n"+
		"SUMMARY:Sortir les poubelles\r\nRRULE:FREQ=WEEKLY\r\nEND:VTODO\r\nEND:VCALENDAR\r\n")
	t.Cleanup(func() { _ = p.Delete(ctx, bins) })

	open, err := p.List(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range open {
		if strings.Contains(it.Title, "Reserver la salle") {
			t.Error("a cancelled task is listed as something still to do")
		}
	}
	all, err := p.List(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	var sawCancelled, sawRepeating bool
	for _, it := range all {
		if strings.Contains(it.Title, "Reserver la salle") {
			sawCancelled = it.Cancelled
		}
		if strings.Contains(it.Title, "poubelles") {
			sawRepeating = it.Recurring
		}
	}
	if !sawCancelled {
		t.Error("the cancelled task is not marked as such")
	}
	if !sawRepeating {
		t.Error("the repeating task is not marked as such")
	}

	// Ticking it would close the weekly reminder for good.
	if err := p.SetDone(ctx, bins, true); err == nil {
		t.Error("completing a repeating task was accepted")
	}
}

func TestLiveCompletingAPlainTaskKeepsTheRest(t *testing.T) {
	p, collection := liveProvider(t)
	ctx := context.Background()
	path := collection + "prism-live-plain.ics"
	putTodo(t, path, "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\n"+
		"BEGIN:VTODO\r\nUID:prism-live-plain\r\nDTSTAMP:20260101T000000Z\r\nDUE:20260106T080000Z\r\n"+
		"SUMMARY:Appeler la banque\r\nPRIORITY:1\r\nCATEGORIES:perso\r\nEND:VTODO\r\nEND:VCALENDAR\r\n")
	t.Cleanup(func() { _ = p.Delete(ctx, path) })

	if err := p.SetDone(ctx, path, true); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", os.Getenv("PRISM_TEST_CALDAV_URL")+path, nil)
	req.SetBasicAuth(os.Getenv("PRISM_TEST_CALDAV_USER"), os.Getenv("PRISM_TEST_CALDAV_PASS"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	raw := string(buf[:n])
	if !strings.Contains(raw, "COMPLETED") {
		t.Error("the task was not marked done on the server")
	}
	if !strings.Contains(raw, "CATEGORIES") || !strings.Contains(raw, "PRIORITY") {
		t.Error("completing the task dropped fields it does not model")
	}
}
