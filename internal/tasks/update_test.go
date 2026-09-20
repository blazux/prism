package tasks

import (
	"github.com/emersion/go-ical"
	"testing"
	"time"
)

func textPtr(s string) *string { return &s }
func TestPartialTaskEditPreservesStatusRecurrenceAndAlarms(t *testing.T) {
	todo := ical.NewComponent(ical.CompToDo)
	todo.Props.SetText(ical.PropSummary, "Old")
	todo.Props.SetText(ical.PropStatus, "COMPLETED")
	todo.Props.SetText(ical.PropRecurrenceRule, "FREQ=WEEKLY")
	todo.Children = append(todo.Children, ical.NewComponent(ical.CompAlarm))
	due := time.Now()
	todo.Props.SetDateTime(ical.PropDue, due)
	if err := applyTaskPatch(todo, Patch{Title: textPtr("New")}); err != nil {
		t.Fatal(err)
	}
	status, _ := todo.Props.Text(ical.PropStatus)
	if status != "COMPLETED" || todo.Props.Get(ical.PropRecurrenceRule) == nil || len(todo.Children) != 1 || todo.Props.Get(ical.PropDue) == nil {
		t.Fatal("lost untouched properties")
	}
	if err := applyTaskPatch(todo, Patch{Due: textPtr("")}); err != nil {
		t.Fatal(err)
	}
	if todo.Props.Get(ical.PropDue) != nil {
		t.Fatal("due not cleared")
	}
	if err := applyTaskPatch(todo, Patch{Due: textPtr("tomorrowish")}); err == nil {
		t.Fatal("invalid date accepted")
	}
}
func TestTaskFilters(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	y := now.AddDate(0, 0, -1)
	tom := now.AddDate(0, 0, 1)
	items := []Item{{Title: "Invoice", DueAt: &y}, {Title: "Call", DueAt: &now}, {Title: "Trip", DueAt: &tom}, {Title: "Done", DueAt: &y, Done: true}}
	for f, want := range map[string]string{"overdue": "Invoice", "today": "Call", "upcoming": "Trip"} {
		out, err := Filter(items, "", f, now)
		if err != nil || len(out) != 1 || out[0].Title != want {
			t.Fatalf("%s: %+v %v", f, out, err)
		}
	}
}
