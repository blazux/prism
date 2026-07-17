package server

import "testing"

// The announcement room must never be a 1:1 conversation: a cron job posting
// there would land in someone's private chat with the bot. We ask the Webex API
// for type=group, but the filter is the guarantee — not the query string.
func TestFilterGroupRoomsDropsDirectConversations(t *testing.T) {
	in := []webexRoom{
		{ID: "a", Title: "Team CORE VOIP", Type: "group"},
		{ID: "b", Title: "Vincent", Type: "direct"},
		{ID: "c", Title: "Astreinte", Type: "group"},
		{ID: "d", Title: "", Type: ""}, // API oddity: no type at all
	}
	got := filterGroupRooms(in)
	if len(got) != 2 {
		t.Fatalf("kept %d rooms, want 2 (only the group spaces): %+v", len(got), got)
	}
	for _, r := range got {
		if r.Type != "group" {
			t.Errorf("room %q of type %q leaked through the filter", r.ID, r.Type)
		}
	}
	if got[0].ID != "a" || got[1].ID != "c" {
		t.Errorf("order or selection wrong: %+v", got)
	}
}

func TestFilterGroupRoomsEmpty(t *testing.T) {
	if got := filterGroupRooms(nil); len(got) != 0 {
		t.Errorf("nil input should yield an empty slice, got %+v", got)
	}
}
