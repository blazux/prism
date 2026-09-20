package tasks

import (
	"fmt"
	"strings"
	"time"
)

// Filter is shared by list API and agent; dates use the deployment timezone.
func Filter(items []Item, query, filter string, now time.Time) ([]Item, error) {
	switch filter {
	case "", "all", "today", "overdue", "upcoming", "high":
	default:
		return nil, fmt.Errorf("filter must be all, today, overdue, upcoming or high")
	}
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	tomorrow := day.AddDate(0, 0, 1)
	out := []Item{}
	query = strings.ToLower(strings.TrimSpace(query))
	for _, it := range items {
		if !strings.Contains(strings.ToLower(it.Title), query) {
			continue
		}
		switch filter {
		case "high":
			if it.Priority != "high" {
				continue
			}
		case "today":
			if it.DueAt == nil || it.DueAt.Before(day) || !it.DueAt.Before(tomorrow) {
				continue
			}
		case "overdue":
			if it.Done || it.Cancelled || it.DueAt == nil || !it.DueAt.Before(day) {
				continue
			}
		case "upcoming":
			if it.DueAt == nil || it.DueAt.Before(tomorrow) {
				continue
			}
		}
		out = append(out, it)
	}
	return out, nil
}
