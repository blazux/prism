// Package notes abstracts where notes live behind a small provider interface so
// the REST API and the agent's `note` tool work the same whether notes are
// stored in Postgres (the default "local" provider) or read/written directly
// from a folder of Markdown files — an Obsidian or Logseq vault ("vault"
// provider). IDs are opaque strings: a numeric row id for local, a vault-
// relative file path for vault.
package notes

import (
	"context"
	"strconv"
	"time"

	"prism/internal/memory"
)

// Config keys (stored in agent_config via memory.Store).
const (
	KeyProvider  = "notes_provider"   // "local" | "vault"
	KeyVaultPath = "notes_vault_path" // absolute dir reachable by the server
)

type Item struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Tags      string    `json:"tags"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type Provider interface {
	List(ctx context.Context) ([]Item, error)
	// Save creates (id == "") or updates a note and returns its id.
	Save(ctx context.Context, id, title, body, tags string) (string, error)
	Delete(ctx context.Context, id string) error
	Kind() string
}

// ProviderFor returns the configured provider, falling back to the local
// Postgres-backed one. session scopes the local provider's notes.
func ProviderFor(ctx context.Context, store *memory.Store, session string) Provider {
	if store != nil {
		if kind, _, _ := store.GetConfig(ctx, KeyProvider); kind == "vault" {
			if path, _, _ := store.GetConfig(ctx, KeyVaultPath); path != "" {
				return &VaultProvider{Dir: path}
			}
		}
	}
	return &DBProvider{Store: store, Session: session}
}

// ─── Local (Postgres) provider ──────────────────────────────────────────────────

type DBProvider struct {
	Store   *memory.Store
	Session string
}

func (p *DBProvider) Kind() string { return "local" }

func (p *DBProvider) List(ctx context.Context) ([]Item, error) {
	ns, err := p.Store.ListNotes(ctx, p.Session)
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(ns))
	for _, n := range ns {
		out = append(out, Item{
			ID: strconv.FormatInt(n.ID, 10), Title: n.Title, Body: n.Body, Tags: n.Tags,
			CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt,
		})
	}
	return out, nil
}

func (p *DBProvider) Save(ctx context.Context, id, title, body, tags string) (string, error) {
	if id == "" {
		nid, err := p.Store.AddNote(ctx, p.Session, title, body, tags)
		return strconv.FormatInt(nid, 10), err
	}
	iid, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return "", err
	}
	return id, p.Store.UpdateNote(ctx, p.Session, iid, title, body, tags)
}

func (p *DBProvider) Delete(ctx context.Context, id string) error {
	iid, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return err
	}
	return p.Store.DeleteNote(ctx, p.Session, iid)
}
