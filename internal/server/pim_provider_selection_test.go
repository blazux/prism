package server

import (
	"prism/internal/calendar"
	"prism/internal/tasks"
	"testing"
	"time"
)

func TestExplicitPIMSourceCannotSilentlyWriteLocal(t *testing.T) {
	ms := securityStore(t)
	ctx := t.Context()
	for _, source := range []string{"google", "microsoft", "caldav", "unknown"} {
		if err := ms.SetConfig(ctx, calendar.KeyProvider, source); err != nil {
			t.Fatal(err)
		}
		p := calendar.ProviderFor(ctx, ms, "global")
		if p.Kind() != "unavailable" {
			t.Fatalf("%s unexpectedly selected %s", source, p.Kind())
		}
		if _, err := p.Add(ctx, "must not be stored", "", "", time.Now(), nil); err == nil {
			t.Fatal("unavailable calendar accepted a write")
		}
	}
	for _, source := range []string{"todoist", "caldav", "unknown"} {
		if err := ms.SetConfig(ctx, tasks.KeyProvider, source); err != nil {
			t.Fatal(err)
		}
		p := tasks.ProviderFor(ctx, ms, "global")
		if p.Kind() != "unavailable" {
			t.Fatalf("%s unexpectedly selected %s", source, p.Kind())
		}
		if _, err := p.Add(ctx, "must not be stored", "normal", nil); err == nil {
			t.Fatal("unavailable task source accepted a write")
		}
	}
	items, err := ms.ListTasks(ctx, "global", true)
	if err != nil || len(items) != 0 {
		t.Fatal(items, err)
	}
	events, err := ms.ListEvents(ctx, "global", nil, nil)
	if err != nil || len(events) != 0 {
		t.Fatal(events, err)
	}
	// Local and automatic defaults remain usable when no source was configured.
	for _, source := range []string{"", "auto", "local"} {
		if err := ms.SetConfig(ctx, calendar.KeyProvider, source); err != nil {
			t.Fatal(err)
		}
		if err := ms.SetConfig(ctx, tasks.KeyProvider, source); err != nil {
			t.Fatal(err)
		}
		if calendar.ProviderFor(ctx, ms, "global").Kind() != "local" || tasks.ProviderFor(ctx, ms, "global").Kind() != "local" {
			t.Fatal("local default changed", source)
		}
	}
	if err := ms.SetSecret(ctx, tasks.TodoistTokenSecret, "fixture-token"); err != nil {
		t.Fatal(err)
	}
	if err := ms.SetConfig(ctx, tasks.KeyProvider, "todoist"); err != nil {
		t.Fatal(err)
	}
	if tasks.ProviderFor(ctx, ms, "global").Kind() != "todoist" {
		t.Fatal("configured source no longer selected")
	}
}
