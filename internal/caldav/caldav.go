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
func Load(ctx context.Context, store *memory.Store) (Config, bool) {
	var c Config
	if store == nil {
		return c, false
	}
	raw, ok, _ := store.GetConfig(ctx, KeyConfig)
	if !ok || raw == "" {
		return c, false
	}
	_ = json.Unmarshal([]byte(raw), &c)
	c.Pass, _, _ = store.GetSecret(ctx, PasswordSecret)
	if c.URL == "" || c.User == "" || c.Pass == "" {
		return c, false
	}
	return c, true
}

// Enabled reports whether CalDAV is configured.
func Enabled(ctx context.Context, store *memory.Store) bool {
	_, ok := Load(ctx, store)
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

// WrapCalendar wraps a single component into a VCALENDAR ready to PUT.
func WrapCalendar(comp *ical.Component) *ical.Calendar {
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//Prism//CalDAV//EN")
	cal.Children = append(cal.Children, comp)
	return cal
}
