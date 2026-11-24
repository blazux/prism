package notes

import (
	"context"
	"errors"
	"strconv"
	"time"
)

var ErrRecoveryConflict = errors.New("the destination already exists; move or rename it before restoring")

type Version struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Body      string    `json:"body,omitempty"`
	Tags      string    `json:"tags"`
	CreatedAt time.Time `json:"createdAt"`
}

// Recovery is optional so external providers can retain the ordinary note
// interface. Both built-in sources preserve edits and deleted notes.
type Recovery interface {
	History(context.Context, string, string) ([]Version, error)
	ReadVersion(context.Context, string, string) (Version, error)
	Trash(context.Context) ([]Item, error)
	Restore(context.Context, string, string) (string, error)
}

func (p *DBProvider) History(ctx context.Context, id, before string) ([]Version, error) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return nil, err
	}
	var cursor int64
	if before != "" {
		cursor, err = strconv.ParseInt(before, 10, 64)
		if err != nil || cursor < 1 {
			return nil, errors.New("invalid history cursor")
		}
	}
	versions, err := p.Store.NoteHistory(ctx, p.Session, n, cursor)
	if err != nil {
		return nil, err
	}
	out := []Version{}
	for _, v := range versions {
		out = append(out, Version{ID: strconv.FormatInt(v.ID, 10), Title: v.Title, Body: v.Body, Tags: v.Tags, CreatedAt: v.CreatedAt})
	}
	return out, nil
}
func (p *DBProvider) ReadVersion(ctx context.Context, id, version string) (Version, error) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return Version{}, err
	}
	v, err := strconv.ParseInt(version, 10, 64)
	if err != nil || v < 1 {
		return Version{}, errors.New("invalid note version")
	}
	item, err := p.Store.ReadNoteVersion(ctx, p.Session, n, v)
	return Version{ID: strconv.FormatInt(item.ID, 10), Title: item.Title, Body: item.Body, Tags: item.Tags, CreatedAt: item.CreatedAt}, err
}
func (p *DBProvider) Trash(ctx context.Context) ([]Item, error) {
	items, err := p.Store.TrashedNotes(ctx, p.Session)
	if err != nil {
		return nil, err
	}
	out := []Item{}
	for _, n := range items {
		out = append(out, Item{ID: strconv.FormatInt(n.ID, 10), Title: n.Title, Body: n.Body, Tags: n.Tags, CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt, Aliases: n.Aliases, DeletedAt: n.DeletedAt})
	}
	return out, nil
}
func (p *DBProvider) Restore(ctx context.Context, id, version string) (string, error) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return "", err
	}
	var v int64
	if version != "" {
		v, err = strconv.ParseInt(version, 10, 64)
		if err != nil || v < 1 {
			return "", errors.New("invalid note version")
		}
	}
	return id, p.Store.RestoreNote(ctx, p.Session, n, v)
}
