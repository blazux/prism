// Package tasks abstracts where to-do tasks live behind a provider interface:
// the existing Postgres store ("local") or a CalDAV server ("caldav") exposing
// VTODO items (Apple Reminders, Nextcloud Tasks, …). IDs are opaque strings.
package tasks

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"prism/internal/caldav"
	"prism/internal/memory"
)

type Item struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Done      bool       `json:"done"`
	Priority  string     `json:"priority"`
	DueAt     *time.Time `json:"dueAt,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	// Cancelled is a task that was called off rather than finished: it holds no
	// commitment, and must not be counted as something still to do. Recurring
	// marks one occurrence of a repeating task, or a series the server would not
	// expand. Only the CalDAV provider fills these in, so "not Cancelled" must
	// not be read as "live" for another source.
	Cancelled bool `json:"cancelled,omitempty"`
	Recurring bool `json:"recurring,omitempty"`
}

type Provider interface {
	List(ctx context.Context, includeDone bool) ([]Item, error)
	Add(ctx context.Context, title, priority string, due *time.Time) (string, error)
	Update(ctx context.Context, id string, patch Patch) error
	SetDone(ctx context.Context, id string, done bool) error
	Delete(ctx context.Context, id string) error
	Kind() string
}

// KeyProvider holds the user's explicit tasks source choice
// (auto|local|caldav|todoist). "auto"/unset uses the precedence below.
const KeyProvider = "tasks_provider"

// ProviderFor returns the active provider. An explicit choice wins when its
// backend is available; otherwise (auto) the precedence is Todoist → CalDAV →
// local.
func ProviderFor(ctx context.Context, store *memory.Store, session string) Provider {
	if store != nil {
		choice, _, err := store.GetConfig(ctx, KeyProvider)
		if err != nil {
			// Falling back to local here sent the user's next task into the
			// local database instead of Todoist or their CalDAV server, where
			// it would never be seen again. Fail the call instead.
			return &unavailableProvider{err}
		}
		switch choice {
		case "local":
			return &DBProvider{Store: store, Session: session}
		case "todoist":
			if p := todoistProvider(ctx, store); p != nil {
				return p
			}
		case "caldav":
			if p := caldavProvider(ctx, store); p != nil {
				return p
			}
		}
	}
	if p := todoistProvider(ctx, store); p != nil {
		return p
	}
	if p := caldavProvider(ctx, store); p != nil {
		return p
	}
	return &DBProvider{Store: store, Session: session}
}

func todoistProvider(ctx context.Context, store *memory.Store) Provider {
	tok, err := todoistToken(ctx, store)
	if err != nil {
		return &unavailableProvider{err}
	}
	if tok != "" {
		return &TodoistProvider{token: tok}
	}
	return nil
}

func caldavProvider(ctx context.Context, store *memory.Store) Provider {
	cfg, ok, err := caldav.Load(ctx, store)
	if err != nil {
		return &unavailableProvider{err}
	}
	if ok {
		return &CalDAVProvider{cfg: cfg}
	}
	return nil
}

// unavailableProvider stands in when the configured source cannot be
// determined. Every call fails with the reason, which is the honest answer:
// silently using a different backend loses what the user writes.
type unavailableProvider struct{ err error }

func (p *unavailableProvider) fail() error {
	return fmt.Errorf("cannot tell which task source is configured, so nothing was read or written: %w", p.err)
}
func (p *unavailableProvider) Kind() string { return "unavailable" }
func (p *unavailableProvider) List(context.Context, bool) ([]Item, error) {
	return nil, p.fail()
}
func (p *unavailableProvider) Add(context.Context, string, string, *time.Time) (string, error) {
	return "", p.fail()
}
func (p *unavailableProvider) SetDone(context.Context, string, bool) error { return p.fail() }
func (p *unavailableProvider) Delete(context.Context, string) error        { return p.fail() }

// ─── Local (Postgres) provider ──────────────────────────────────────────────────

type DBProvider struct {
	Store   *memory.Store
	Session string
}

func (p *DBProvider) Kind() string { return "local" }

func (p *DBProvider) List(ctx context.Context, includeDone bool) ([]Item, error) {
	ts, err := p.Store.ListTasks(ctx, p.Session, includeDone)
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(ts))
	for _, t := range ts {
		out = append(out, Item{
			ID: strconv.FormatInt(t.ID, 10), Title: t.Title, Done: t.Done,
			Priority: t.Priority, DueAt: t.DueAt, CreatedAt: t.CreatedAt,
		})
	}
	return out, nil
}

func (p *DBProvider) Add(ctx context.Context, title, priority string, due *time.Time) (string, error) {
	id, err := p.Store.AddTask(ctx, p.Session, title, priority, due)
	return strconv.FormatInt(id, 10), err
}

func (p *DBProvider) SetDone(ctx context.Context, id string, done bool) error {
	iid, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return err
	}
	return p.Store.SetTaskDone(ctx, p.Session, iid, done)
}

func (p *DBProvider) Delete(ctx context.Context, id string) error {
	iid, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return err
	}
	return p.Store.DeleteTask(ctx, p.Session, iid)
}
