package server

// Vox proxy (Vortex megazord).
//
// The telephony data lives in Vox, not here: call logs, outbound call tasks, SIP
// status, live stats, the directory. Rather than duplicate them, Cortex proxies
// Vox's own API — that is what VOX_URL is for. The Téléphonie app and the admin
// telephony settings both talk to /api/vox/<path>, which forwards to
// <VOX_URL>/api/<path> with Vox's credentials.
//
// Admin-only: call history, outbound dialling and trunk config are an admin
// surface. Unavailable (503) when this Cortex is not docked with a Vox.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"prism/internal/tasks"
)

var voxHTTP = &http.Client{Timeout: 30 * time.Second}

// voxProxyPrefix is the mount point; everything after it is the Vox API path.
const voxProxyPrefix = "/api/vox/"

// Outbound calls surfaced in the Tasks list carry a "call:" id so they can never be
// mistaken for a row of the tasks table. They are read-only there: a phone call is
// not something you tick off, and the agent owns the verbs (place_call/cancel_call).
const callTaskPrefix = "call:"

const callTaskReadOnlyMsg = "This is an outbound phone call, not a to-do. Ask the agent to cancel it (cancel_call)."

func isCallTaskID(id string) bool { return strings.HasPrefix(id, callTaskPrefix) }

// voxPendingCallTasks returns the outbound calls that are still going to happen —
// queued, or being dialled right now — rendered as task items.
//
// Vortex: an outbound call *is* a task ("call the plumber and book a slot"), so it
// belongs in the Tasks list next to the user's own, not in a telephony silo. They are
// read-only here: the agent owns the verbs (place_call / cancel_call). Finished calls
// are not tasks any more — they live in the call history.
//
// Never fatal: if the phone stack is undocked, slow or down, the user's real tasks
// must still load. Errors are swallowed and the list simply carries no calls.
func (s *Server) voxPendingCallTasks(ctx context.Context) []tasks.Item {
	if s.cfg.VoxURL == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(s.cfg.VoxURL, "/")+"/api/calls?limit=50", nil)
	if err != nil {
		return nil
	}
	if s.cfg.VoxUser != "" {
		req.SetBasicAuth(s.cfg.VoxUser, s.cfg.VoxPassword)
	}
	resp, err := voxHTTP.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil
	}
	var body struct {
		Items []struct {
			ID          int        `json:"id"`
			PhoneNumber string     `json:"phone_number"`
			ContactName *string    `json:"contact_name"`
			Mission     string     `json:"mission"`
			ScheduledAt *time.Time `json:"scheduled_at"`
			Status      string     `json:"status"`
		} `json:"items"`
	}
	if json.NewDecoder(resp.Body).Decode(&body) != nil {
		return nil
	}

	var out []tasks.Item
	for _, t := range body.Items {
		if t.Status != "pending" && t.Status != "calling" {
			continue
		}
		who := t.PhoneNumber
		if t.ContactName != nil && *t.ContactName != "" {
			who = *t.ContactName
		}
		title := fmt.Sprintf("📞 Call %s — %s", who, t.Mission)
		if t.Status == "calling" {
			title = fmt.Sprintf("📞 Calling %s now — %s", who, t.Mission)
		}
		out = append(out, tasks.Item{
			ID:       fmt.Sprintf("%s%d", callTaskPrefix, t.ID),
			Title:    title,
			Done:     false,
			Priority: "normal",
			DueAt:    t.ScheduledAt,
		})
	}
	return out
}

func (s *Server) handleVoxProxy(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil || !s.isAdminUser(r.Context(), u) {
		writeErr(w, http.StatusForbidden, "admin only")
		return
	}
	if s.cfg.VoxURL == "" {
		writeErr(w, http.StatusServiceUnavailable, "no telephony stack docked (VOX_URL unset)")
		return
	}

	path := strings.TrimPrefix(r.URL.Path, voxProxyPrefix)
	if path == "" || strings.Contains(path, "..") {
		writeErr(w, http.StatusBadRequest, "bad path")
		return
	}
	target := strings.TrimRight(s.cfg.VoxURL, "/") + "/api/" + path
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	// Vox's UI sits behind HTTP Basic; we hold its credentials, the browser doesn't.
	if s.cfg.VoxUser != "" {
		req.SetBasicAuth(s.cfg.VoxUser, s.cfg.VoxPassword)
	}

	resp, err := voxHTTP.Do(req)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "vox unreachable: "+err.Error())
		return
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body) //nolint:errcheck
}
