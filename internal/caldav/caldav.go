// Package caldav holds the shared CalDAV connection used by the calendar and
// tasks providers. It wraps emersion/go-webdav with basic-auth, discovers the
// user's calendar collections, and offers small helpers to map to/from
// iCalendar. Credentials follow the same pattern as email: a JSON config in
// agent_config plus a password in the encrypted secrets store.
package caldav

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	xcaldav "github.com/emersion/go-webdav/caldav"

	"prism/internal/memory"
)

const (
	KeyConfig      = "caldav_config"
	PasswordSecret = "caldav_password"
)

type Config struct {
	URL       string `json:"url"`
	User      string `json:"user"`
	EventPath string `json:"eventPath,omitempty"` // pinned events collection (optional)
	TaskPath  string `json:"taskPath,omitempty"`  // pinned tasks collection (optional)
	Pass      string `json:"-"`
}

// Load returns the stored config (with password) and whether it is usable.
// Load reports (config, configured, error). A read failure is NOT "not
// configured": telling the two apart is what stops a database hiccup from
// silently routing the user's writes to a different backend.
func Load(ctx context.Context, store *memory.Store) (Config, bool, error) {
	var c Config
	if store == nil {
		return c, false, nil
	}
	raw, ok, err := store.GetConfig(ctx, KeyConfig)
	if err != nil {
		return c, false, err
	}
	if !ok || raw == "" {
		return c, false, nil
	}
	_ = json.Unmarshal([]byte(raw), &c)
	var perr error
	c.Pass, _, perr = store.GetSecret(ctx, PasswordSecret)
	if perr != nil {
		return c, false, perr
	}
	if c.URL == "" || c.User == "" || c.Pass == "" {
		return c, false, nil
	}
	return c, true, nil
}

// Enabled reports whether CalDAV is configured.
func Enabled(ctx context.Context, store *memory.Store) bool {
	_, ok, _ := Load(ctx, store)
	return ok
}

func (c Config) client() (*xcaldav.Client, error) {
	httpc := webdav.HTTPClientWithBasicAuth(&http.Client{Timeout: 20 * time.Second}, c.User, c.Pass)
	return xcaldav.NewClient(httpc, c.URL)
}

// Discover lists the calendar collections in the user's calendar home set.
func (c Config) Discover(ctx context.Context) ([]xcaldav.Calendar, error) {
	cl, err := c.client()
	if err != nil {
		return nil, err
	}
	return discover(ctx, cl)
}

func discover(ctx context.Context, cl *xcaldav.Client) ([]xcaldav.Calendar, error) {
	principal, err := cl.FindCurrentUserPrincipal(ctx)
	if err != nil {
		return nil, fmt.Errorf("find principal: %w", err)
	}
	home, err := cl.FindCalendarHomeSet(ctx, principal)
	if err != nil {
		return nil, fmt.Errorf("find calendar home: %w", err)
	}
	return cl.FindCalendars(ctx, home)
}

// Conn is a live client plus the resolved collection paths for events and tasks.
type Conn struct {
	Client    *xcaldav.Client
	EventPath string
	TaskPath  string
}

// Connect dials the server and resolves which collection holds events and which
// holds tasks (auto-picking by supported component when not pinned in config).
func (c Config) Connect(ctx context.Context) (*Conn, error) {
	cl, err := c.client()
	if err != nil {
		return nil, err
	}
	ev, task := c.EventPath, c.TaskPath
	if ev == "" || task == "" {
		cals, err := discover(ctx, cl)
		if err != nil {
			return nil, err
		}
		if ev == "" {
			ev = pick(cals, ical.CompEvent)
		}
		if task == "" {
			task = pick(cals, ical.CompToDo)
		}
	}
	return &Conn{Client: cl, EventPath: ev, TaskPath: task}, nil
}

// PickPaths chooses default collections for events and tasks from a calendar list.
func PickPaths(cals []xcaldav.Calendar) (eventPath, taskPath string) {
	return pick(cals, ical.CompEvent), pick(cals, ical.CompToDo)
}

// pick returns the path of the first calendar advertising support for comp (or
// the first calendar at all if none advertise a component set).
func pick(cals []xcaldav.Calendar, comp string) string {
	for _, cal := range cals {
		for _, c := range cal.SupportedComponentSet {
			if c == comp {
				return cal.Path
			}
		}
	}
	for _, cal := range cals {
		if len(cal.SupportedComponentSet) == 0 {
			return cal.Path
		}
	}
	if len(cals) > 0 {
		return cals[0].Path
	}
	return ""
}

// ─── iCalendar helpers ──────────────────────────────────────────────────────────

// NewUID returns a fresh unique id for a new object.
func NewUID() string { return fmt.Sprintf("prism-%d", time.Now().UnixNano()) }

// ObjectPath builds the href for an object with the given uid in a collection.
func ObjectPath(calPath, uid string) string {
	if !strings.HasSuffix(calPath, "/") {
		calPath += "/"
	}
	return calPath + uid + ".ics"
}

// ── Recurring occurrences ─────────────────────────────────────────────────────
//
// A calendar object holds one component per stored item, but a repeating item
// is several: the master plus the occurrences that override it, or, when the
// server expands the series, one component per instance. They all share the
// object href, so an id has to say which one it means — and a mutation aimed at
// a single occurrence has to be refused, because the href designates them all.

// OccurrenceSep separates an object href from the RECURRENCE-ID of one
// instance: "/cal/weekly.ics#20260302T090000Z".
const OccurrenceSep = "#"

// occurrenceKeyRe is the exact shape CompactUTC produces. Requiring it means an
// href that genuinely contains "#" is still addressed whole, unless what
// follows its last "#" happens to look like a UTC timestamp.
var occurrenceKeyRe = regexp.MustCompile(`^\d{8}T\d{6}Z$`)

func CompactUTC(t time.Time) string { return t.UTC().Format("20060102T150405Z") }

// RecurrenceKey identifies the instance an overriding component replaces, or ""
// when the component is not one. An unparsable RECURRENCE-ID still marks it as
// an instance: its own start distinguishes it just as well.
func RecurrenceKey(comp *ical.Component, start time.Time) string {
	if comp == nil || comp.Props.Get(ical.PropRecurrenceID) == nil {
		return ""
	}
	if t, err := comp.Props.DateTime(ical.PropRecurrenceID, time.UTC); err == nil && !t.IsZero() {
		return CompactUTC(t)
	}
	return CompactUTC(start)
}

// SplitOccurrenceID separates the object href from the occurrence key. Only the
// last separator counts, and the suffix must have the exact CompactUTC shape.
func SplitOccurrenceID(id string) (objectPath, occurrence string) {
	i := strings.LastIndex(id, OccurrenceSep)
	if i <= 0 || !occurrenceKeyRe.MatchString(id[i+1:]) {
		return id, ""
	}
	return id[:i], id[i+1:]
}

// ErrSingleOccurrence refuses to touch one instance of a repeating item.
// Deleting it would remove the object, so the whole series; changing it would
// mean writing an EXDATE or an override component. Refusing out loud beats
// destroying a series on the user's real calendar without saying so.
func ErrSingleOccurrence(verb string) error {
	return fmt.Errorf("this is a single occurrence of a repeating item and cannot be %sd on its own; "+
		"use the id without its %q suffix to %s the whole series, or change this one occurrence in your calendar app",
		verb, OccurrenceSep+"<date>", verb)
}

// ObjectIn checks that id addresses ONE object inside collection, and returns
// it unchanged when it does. A DELETE on a collection href removes every object
// the collection holds, and these ids come from a model that can invent one, so
// anything that is not a single object directly below the collection is refused
// before it reaches the server. A wrongly refused delete is an annoyance; a
// wrongly accepted one erases a calendar.
func ObjectIn(collection, id string) (string, error) {
	if strings.TrimSpace(collection) == "" {
		return "", fmt.Errorf("no calendar collection resolved for this account")
	}
	raw := strings.TrimSpace(id)
	if raw == "" {
		return "", fmt.Errorf("empty id")
	}
	if strings.HasSuffix(raw, "/") {
		return "", fmt.Errorf("%q addresses a whole calendar, not one item: refusing, it would delete everything in it", id)
	}
	base := strings.TrimSuffix(path.Clean("/"+collection), "/")
	full := path.Clean("/" + strings.TrimPrefix(raw, "/"))
	rest := strings.TrimPrefix(full, base+"/")
	// An object resource is a direct child of its collection (RFC 4791 §5.2).
	if full == base || rest == full || rest == "" || strings.Contains(rest, "/") {
		return "", fmt.Errorf("%q is not an item of this calendar (%s): refusing to touch it", id, collection)
	}
	return raw, nil
}

// WrapCalendar wraps a single component into a VCALENDAR ready to PUT.
func WrapCalendar(comp *ical.Component) *ical.Calendar {
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//Prism//CalDAV//EN")
	cal.Children = append(cal.Children, comp)
	return cal
}
