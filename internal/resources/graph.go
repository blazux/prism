// Package resources plans conservative cleanup. Provenance is not ownership:
// only dependencies explicitly declared dedicated to a widget are automatic
// cleanup candidates. Observed references can protect resources, never authorize
// their deletion. Data files always require an explicit selection.
package resources

import (
	"path/filepath"
	"sort"
	"sync"
)

var locks sync.Map

// Lock serializes resource and crontab mutations across executors and HTTP UI
// handlers attached to the same environment in this process.
func Lock(root string) func() {
	key, _ := filepath.Abs(root)
	v, _ := locks.LoadOrStore(key, &sync.Mutex{})
	m := v.(*sync.Mutex)
	m.Lock()
	return m.Unlock
}

type Node struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	Name         string   `json:"name"`
	Session      string   `json:"session,omitempty"`
	BoundSession string   `json:"boundSession,omitempty"`
	Tracked      bool     `json:"tracked"`
	Missing      bool     `json:"missing,omitempty"`
	Protected    bool     `json:"protected,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
	Dedicated    []string `json:"dedicated,omitempty"`
	UsedBy       []string `json:"usedBy"`
	Reason       string   `json:"reason,omitempty"`
}

type Plan struct {
	Remove []Node `json:"remove"`
	Keep   []Node `json:"keep"`
}

// PlanRemoval is a fixed-point graph cut: a retained consumer protects its
// dependencies, including cycles, shared backends, and shared-gallery copies.
// roots are the widgets explicitly being removed; selected are maintenance
// selections. Unknown/legacy resources never join the automatic cut.
func PlanRemoval(nodes []Node, roots, selected []string) Plan {
	byID := map[string]Node{}
	cut := map[string]bool{}
	explicit := map[string]bool{}
	reasons := map[string]string{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	for _, id := range roots {
		for _, dep := range byID[id].Dependencies {
			reasons[dep] = "Kept: usage is inferred; review before cleanup."
		}
		cut[id] = true
		explicit[id] = true
	}
	for _, id := range selected {
		cut[id] = true
		explicit[id] = true
	}
	for _, id := range roots {
		for _, dep := range byID[id].Dedicated {
			n, ok := byID[dep]
			if ok && n.Tracked && n.Kind != "file" && !n.Protected {
				cut[dep] = true
				delete(reasons, dep)
			} else {
				if n.Missing {
					reasons[dep] = "Already missing; no deletion needed."
				} else {
					reasons[dep] = "Kept: data or an untracked resource requires an explicit cleanup selection."
				}
			}
		}
	}
	for id := range cut {
		n, ok := byID[id]
		if !ok {
			delete(cut, id)
			continue
		}
		if n.Protected {
			delete(cut, id)
			reasons[id] = "Kept: protected or owned by another user."
		}
	}
	changed := true
	for changed {
		changed = false
		for _, n := range nodes {
			if cut[n.ID] {
				continue
			}
			for _, dep := range n.Dependencies {
				if cut[dep] {
					delete(cut, dep)
					reasons[dep] = "Kept: still used by another resource."
					changed = true
				}
			}
		}
	}
	p := Plan{Remove: []Node{}, Keep: []Node{}}
	for _, n := range nodes {
		if cut[n.ID] {
			p.Remove = append(p.Remove, n)
			continue
		}
		if reasons[n.ID] != "" {
			n.Reason = reasons[n.ID]
		}
		if explicit[n.ID] || reasons[n.ID] != "" {
			p.Keep = append(p.Keep, n)
		}
	}
	// Stop producers before deleting code/data. Widgets last: a failed backend
	// deletion leaves an actionable widget and metadata for the retry.
	rank := map[string]int{"cron": 0, "service": 1, "tool": 2, "file": 3, "widget": 4}
	sort.Slice(p.Remove, func(i, j int) bool {
		a, b := p.Remove[i], p.Remove[j]
		if rank[a.Kind] != rank[b.Kind] {
			return rank[a.Kind] < rank[b.Kind]
		}
		return a.ID < b.ID
	})
	sort.Slice(p.Keep, func(i, j int) bool { return p.Keep[i].ID < p.Keep[j].ID })
	return p
}
