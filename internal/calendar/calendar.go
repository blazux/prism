// Package calendar abstracts where calendar events live behind a provider
// interface: the existing Postgres store ("local") or a CalDAV server ("caldav")
// such as Apple iCloud, Nextcloud or Fastmail. IDs are opaque strings — a row id
// for local, the object href for CalDAV.
package calendar

import (
	"context"
	"strconv"
	"time"

	"prism/internal/caldav"
	"prism/internal/memory"
	"prism/internal/oauthx"
)

type Item struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	StartAt     time.Time  `json:"startAt"`
	EndAt       *time.Time `json:"endAt,omitempty"`
	Location    string     `json:"location"`
	CreatedAt   time.Time  `json:"createdAt"`
}

type Provider interface {
	List(ctx context.Context, from, to *time.Time) ([]Item, error)
	Add(ctx context.Context, title, description, location string, start time.Time, end *time.Time) (string, error)
	Delete(ctx context.Context, id string) error
	Kind() string
}

// ProviderFor returns the configured provider. Google (OAuth) wins when
// connected, then CalDAV, otherwise the local Postgres store.
func ProviderFor(ctx context.Context, store *memory.Store, session string) Provider {
	if store != nil && oauthx.Connected(ctx, store, "google") {
		if c, err := oauthx.HTTPClient(ctx, store, "google"); err == nil {
			return &GoogleProvider{client: c}
		}
	}
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

func (p *DBProvider) List(ctx context.Context, from, to *time.Time) ([]Item, error) {
	evs, err := p.Store.ListEvents(ctx, p.Session, from, to)
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(evs))
	for _, e := range evs {
		out = append(out, Item{
			ID: strconv.FormatInt(e.ID, 10), Title: e.Title, Description: e.Description,
			StartAt: e.StartAt, EndAt: e.EndAt, Location: e.Location, CreatedAt: e.CreatedAt,
		})
	}
	return out, nil
}

func (p *DBProvider) Add(ctx context.Context, title, description, location string, start time.Time, end *time.Time) (string, error) {
	id, err := p.Store.AddEvent(ctx, p.Session, title, description, location, start, end)
	return strconv.FormatInt(id, 10), err
}

func (p *DBProvider) Delete(ctx context.Context, id string) error {
	iid, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return err
	}
	return p.Store.DeleteEvent(ctx, p.Session, iid)
}
