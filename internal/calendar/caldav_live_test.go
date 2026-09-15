package calendar

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"prism/internal/caldav"
)

// These run against a DISPOSABLE CalDAV server, never a real account:
//
//	docker run -d --name prism-caldav-test -p 127.0.0.1:5232:5232 … tomsquest/docker-radicale
//	PRISM_TEST_CALDAV_URL=http://127.0.0.1:5232 PRISM_TEST_CALDAV_USER=eval \
//	PRISM_TEST_CALDAV_PASS=eval go test ./internal/calendar/ -run Live
//
// The unit tests cover the parsing; only a real server can show whether it
// expands a recurrence set, and whether the read-modify-write really preserves
// what the server sent back.
func liveProvider(t *testing.T) (*CalDAVProvider, string) {
	t.Helper()
	url := os.Getenv("PRISM_TEST_CALDAV_URL")
	if url == "" {
		t.Skip("needs a disposable CalDAV server (PRISM_TEST_CALDAV_URL)")
	}
	user := os.Getenv("PRISM_TEST_CALDAV_USER")
	pass := os.Getenv("PRISM_TEST_CALDAV_PASS")
	collection := "/" + user + "/agenda/"
	cfg := caldav.Config{URL: url, User: user, Pass: pass, EventPath: collection, TaskPath: collection}
	return &CalDAVProvider{cfg: cfg}, collection
}

// put writes a raw calendar object, the way a phone or Thunderbird would.
func put(t *testing.T, path, body string) {
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

func TestLiveRecurringSeriesAndItsOverride(t *testing.T) {
	p, collection := liveProvider(t)
	ctx := context.Background()
	const uid = "prism-live-weekly"
	put(t, collection+uid+".ics", "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\n"+
		"BEGIN:VEVENT\r\nUID:"+uid+"\r\nDTSTAMP:20260101T000000Z\r\nDTSTART:20260105T090000Z\r\nDTEND:20260105T100000Z\r\n"+
		"SUMMARY:Reunion hebdo\r\nLOCATION:Salle A\r\nRRULE:FREQ=WEEKLY;COUNT=8\r\n"+
		"BEGIN:VALARM\r\nACTION:DISPLAY\r\nTRIGGER:-PT15M\r\nDESCRIPTION:Rappel\r\nEND:VALARM\r\nEND:VEVENT\r\n"+
		"BEGIN:VEVENT\r\nUID:"+uid+"\r\nRECURRENCE-ID:20260202T090000Z\r\nDTSTAMP:20260101T000000Z\r\n"+
		"DTSTART:20260202T140000Z\r\nDTEND:20260202T150000Z\r\nSUMMARY:Reunion hebdo (decalee)\r\nEND:VEVENT\r\n"+
		"END:VCALENDAR\r\n")
	t.Cleanup(func() { _ = p.Delete(ctx, collection+uid+".ics") })

	from := time.Date(2026, 1, 26, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 2, 9, 0, 0, 0, 0, time.UTC)
	items, err := p.List(ctx, &from, &to)
	if err != nil {
		t.Fatal(err)
	}
	var ours []Item
	for _, it := range items {
		if strings.Contains(it.ID, uid) {
			ours = append(ours, it)
		}
	}
	if len(ours) == 0 {
		t.Fatal("the series did not come back at all")
	}
	for _, it := range ours {
		t.Logf("id=%s start=%s recurring=%v title=%q", it.ID, it.StartAt.UTC().Format(time.RFC3339), it.Recurring, it.Title)
		if !it.Recurring {
			t.Errorf("%q is part of a series but is not flagged recurring", it.ID)
		}
		// Whatever the server does with expand, no occurrence may be handed out
		// under an id that would delete the whole object.
		if _, occ := caldav.SplitOccurrenceID(it.ID); occ == "" && it.StartAt.After(from) && it.StartAt.Before(to) {
			// Only the unexpanded master may keep the bare href, and its start
			// is the start of the series, outside the window asked for.
			t.Errorf("%q is an occurrence inside the window yet addressable as the whole object", it.ID)
		}
	}
	moved := false
	for _, it := range ours {
		if strings.Contains(it.Title, "decalee") {
			moved = true
			if h := it.StartAt.UTC().Hour(); h != 14 {
				t.Errorf("the overridden occurrence kept the wrong hour: %d", h)
			}
		}
	}
	if !moved {
		t.Error("the overridden occurrence is invisible — the evs[0] bug would look exactly like this")
	}
}

func TestLiveEditKeepsTheRuleAndTheAlarm(t *testing.T) {
	p, collection := liveProvider(t)
	ctx := context.Background()
	const uid = "prism-live-edit"
	path := collection + uid + ".ics"
	put(t, path, "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\n"+
		"BEGIN:VEVENT\r\nUID:"+uid+"\r\nDTSTAMP:20260101T000000Z\r\nDTSTART:20260105T090000Z\r\nDTEND:20260105T100000Z\r\n"+
		"SUMMARY:Point equipe\r\nDESCRIPTION:ordre du jour\r\nRRULE:FREQ=WEEKLY;COUNT=4\r\n"+
		"BEGIN:VALARM\r\nACTION:DISPLAY\r\nTRIGGER:-PT10M\r\nDESCRIPTION:Rappel\r\nEND:VALARM\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
	t.Cleanup(func() { _ = p.Delete(ctx, path) })

	start := time.Date(2026, 1, 5, 11, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	if err := p.Update(ctx, path, "Point equipe renomme", "", "Salle B", start, &end); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", os.Getenv("PRISM_TEST_CALDAV_URL")+path, nil)
	req.SetBasicAuth(os.Getenv("PRISM_TEST_CALDAV_USER"), os.Getenv("PRISM_TEST_CALDAV_PASS"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 8192)
	n, _ := resp.Body.Read(buf)
	raw := string(buf[:n])
	if !strings.Contains(raw, "RRULE") {
		t.Error("the recurrence rule was destroyed by the edit: the series became a one-off")
	}
	if !strings.Contains(raw, "VALARM") {
		t.Error("the alarm was destroyed by the edit")
	}
	if !strings.Contains(raw, "Point equipe renomme") {
		t.Error("the new title was not written")
	}
	if strings.Contains(raw, "ordre du jour") {
		t.Error("an explicitly emptied description should have been cleared")
	}
}

func TestLiveRefusesWhatWouldWipeTheCalendar(t *testing.T) {
	p, collection := liveProvider(t)
	ctx := context.Background()
	if err := p.Delete(ctx, collection); err == nil {
		t.Fatal("deleting the collection was accepted")
	}
	if err := p.Delete(ctx, collection+"x.ics"+caldav.OccurrenceSep+"20260202T090000Z"); err == nil {
		t.Fatal("deleting a single occurrence was accepted")
	}
	// The calendar is still there.
	from := time.Now().AddDate(0, -1, 0)
	to := time.Now().AddDate(0, 1, 0)
	if _, err := p.List(ctx, &from, &to); err != nil {
		t.Fatalf("the collection did not survive: %v", err)
	}
}
