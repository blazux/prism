// Package tasks abstracts where to-do tasks live behind a provider interface:
// the existing Postgres store ("local") or a CalDAV server ("caldav") exposing
// VTODO items (Apple Reminders, Nextcloud Tasks, …). IDs are opaque strings.
package tasks

import (
	"context"
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
}

type Provider interface {
	List(ctx context.Context, includeDone bool) ([]Item, error)
	Add(ctx context.Context, title, priority string, due *time.Time) (string, error)
	SetDone(ctx context.Context, id string, done bool) error
	Delete(ctx context.Context, id string) error
	Kind() string
}

// ProviderFor returns the configured provider, preferring CalDAV when set up.
func ProviderFor(ctx context.Context, store *memory.Store, session string) Provider {
	if cfg, ok := caldav.Load(ctx, store); ok {
		return &CalDAVProvider{cfg: cfg}
	}
	return &DBProvider{Store: store, Session: session}
}

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
