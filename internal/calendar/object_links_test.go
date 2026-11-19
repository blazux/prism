package calendar

import (
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"prism/internal/caldav"
	"prism/internal/timeprefs"
)

func TestGetCalDAVLinksReadOccurrencesAndUserTimezone(t *testing.T) {
	const objectPath = "/cal/weekly.ics"
	expanded := strings.ReplaceAll(seriesICS, "20260302T090000Z", "20260309T090000Z")
	stored := seriesICS
	reports := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			w.Header().Set("Content-Type", "text/calendar")
			w.Write([]byte(stored))
		case "REPORT":
			reports++
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusMultiStatus)
			w.Write([]byte(`<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:response><d:href>` + objectPath + `</d:href><d:propstat><d:prop><c:calendar-data>` + html.EscapeString(expanded) + `</c:calendar-data></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(500)
		}
	}))
	defer remote.Close()
	p := &CalDAVProvider{cfg: caldav.Config{URL: remote.URL, User: "fixture", Pass: "fixture", EventPath: "/cal/", TaskPath: "/cal/"}}
	for _, date := range []string{"20260302T090000Z", "20260309T090000Z"} {
		id := objectPath + caldav.OccurrenceSep + date
		item, err := Get(t.Context(), p, id)
		if err != nil || item.ID != id || item.Title != "Reunion hebdo (decalee)" {
			t.Fatalf("%s: %+v %v", id, item, err)
		}
		if err := Update(t.Context(), p, id, Patch{}); err == nil || !strings.Contains(err.Error(), "single occurrence") {
			t.Fatalf("individual occurrence became editable: %v", err)
		}
	}
	if reports != 1 {
		t.Fatalf("expansion requests: %d", reports)
	}
	stored = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:floating\r\nDTSTART:20260615T090000\r\nDTEND:20260615T100000\r\nSUMMARY:Floating time\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	zone := time.FixedZone("fixture", -4*60*60)
	item, err := Get(timeprefs.WithLocation(t.Context(), zone), p, objectPath)
	if err != nil || item.StartAt.Hour() != 9 || item.StartAt.UTC().Hour() != 13 {
		t.Fatalf("user timezone: %+v %v", item, err)
	}
}
