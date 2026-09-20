package server

// Prism Vox proxy.
//
// The telephony data lives in Vox, not here: call logs, outbound call tasks, SIP
// status, live stats, the directory. Rather than duplicate them, Prism proxies
// Vox's own API — that is what VOX_URL is for. The Téléphonie app and the admin
// telephony settings both talk to /api/vox/<path>, which forwards to
// <VOX_URL>/api/<path> with Vox's credentials.
//
// Admin-only: call history, outbound dialling and trunk config are an admin
// surface. Unavailable (503) when this Prism is not docked with a Vox.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
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
// An outbound call *is* a task ("call the plumber and book a slot"), so it
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

// ── The switchboard persona lives in Vox ───────────────────────────────────────
//
// Vox's database is the store; Prism is the editor. That is already how the
// greeting, the spoken phrases, the dictionary, the voice and the trunk work —
// the persona was the odd one out, kept here from when Prism was the brain for
// every call. It no longer is: a stranger is answered by Vox, with Vox's prompt.
//
// Which means a deployment that had written its own persona here would otherwise
// find the switchboard back on the factory text. So the first read migrates it,
// once, and after that this is a pass-through.

// voxSystemPrompt returns the switchboard persona as Vox holds it.
func (s *Server) voxSystemPrompt(ctx context.Context) (string, error) {
	var cfg map[string]string
	if err := s.voxJSON(ctx, http.MethodGet, "/api/config", nil, &cfg); err != nil {
		return "", err
	}
	return cfg["system_prompt"], nil
}

// setVoxSystemPrompt writes the switchboard persona to Vox.
func (s *Server) setVoxSystemPrompt(ctx context.Context, persona string) error {
	body := map[string]any{"values": map[string]string{"system_prompt": persona}}
	return s.voxJSON(ctx, http.MethodPut, "/api/config", body, nil)
}

// migrateVoicePersonaToVox copies a persona written here, back when Prism
// answered every call, into Vox — once. Guarded by a flag rather than by
// comparing texts: an admin who deliberately went back to the default must not
// have their own words pushed over it on the next page load.
func (s *Server) migrateVoicePersonaToVox(ctx context.Context) {
	ms := s.store()
	if ms == nil || s.cfg.VoxURL == "" {
		return
	}
	if done, ok, _ := ms.GetConfig(ctx, cfgVoicePersonaMigrated); ok && done == "1" {
		return
	}
	stored, ok, err := ms.GetConfig(ctx, cfgVoicePersonality)
	if err != nil {
		return // no database answer: try again next time rather than lose the text
	}
	if ok && strings.TrimSpace(stored) != "" {
		if err := s.setVoxSystemPrompt(ctx, stored); err != nil {
			log.Printf("[voice] could not hand the switchboard persona to Vox: %v", err)
			return // Vox may be down; stay unmigrated and retry
		}
		log.Printf("[voice] switchboard persona handed over to Vox, which now owns it")
	}
	ms.SetConfig(ctx, cfgVoicePersonaMigrated, "1")
}

// voxJSON performs one authenticated JSON call against Vox's API.
func (s *Server) voxJSON(ctx context.Context, method, path string, in, out any) error {
	if s.cfg.VoxURL == "" {
		return fmt.Errorf("no telephony stack docked")
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()

	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(s.cfg.VoxURL, "/")+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if s.cfg.VoxUser != "" {
		req.SetBasicAuth(s.cfg.VoxUser, s.cfg.VoxPassword)
	}
	resp, err := voxHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("vox %s %s: HTTP %d", method, path, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
