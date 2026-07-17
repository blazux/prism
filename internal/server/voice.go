package server

// Voice-channel identity (Vortex megazord).
//
// A phone call docks in through Vox using the service token, and in auth.go that
// token authenticates as a *global admin*. Left alone, that means anyone who dials
// the number gets an admin-scoped agent: exec_command, docker_*, email, secrets,
// write_file — plus the owner's entire knowledge base and personal profile. So a
// voice connection is re-identified here.
//
// Until the caller is identified (caller-ID → user, next slice), a call is a
// GUEST: deny-by-default tools, an isolated RAG scope, a switchboard personality,
// and none of the owner's personal context injected into the prompt.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"prism/internal/agent"
	"prism/internal/memory"
)

// The phone is a voice channel: NO tool that touches code, the host, files, mail
// or secrets is ever allowed on it, whoever is calling. Identity only widens the
// *read/memory* surface, never toward danger — so a misheard command or a spoofed
// number can never run something destructive.

// Telephony actions are the switchboard's core job — every caller, known or not,
// may be transferred, leave a message, or have the call ended. Vox performs them.
var voiceTelephonyTools = map[string]bool{
	"transfer_call": true,
	"take_message":  true,
	"end_call":      true,
}

// voiceGuestAllowedTools — an unidentified caller (switchboard). Read-only public
// knowledge + telephony actions. Never anything touching the host/files/secrets.
var voiceGuestAllowedTools = union(map[string]bool{
	"rag_search": true,
}, voiceTelephonyTools)

// voiceKnownAllowedTools — a caller identified by their profile phone. Adds
// remembering a new fact (persisted to their personal scope, so it carries across
// calls and to the dashboard) on top of search + telephony. Still nothing
// dangerous: this is continuity, not host access. (search_history joins once voice
// calls use a stable per-user session — today they don't, so it would recall nothing.)
var voiceKnownAllowedTools = union(map[string]bool{
	"rag_search":     true,
	"save_user_info": true,
}, voiceTelephonyTools)

// union merges tool allow-list maps into a new set.
func union(sets ...map[string]bool) map[string]bool {
	out := map[string]bool{}
	for _, s := range sets {
		for k, v := range s {
			if v {
				out[k] = true
			}
		}
	}
	return out
}

// normalizePhone strips a caller/profile number to bare digits for matching.
func normalizePhone(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

const (
	// voiceGuestScope isolates a caller from every personal/group RAG scope. It
	// resolves to no collections by default, so a stranger reads nothing. Point the
	// switchboard at a real (public/company) scope with the voice_rag_scope key.
	voiceGuestScope = "voice"

	// voiceChannelName is asserted by Vox as ?channel=voice on the WS connection.
	voiceChannelName = "voice"

	cfgVoicePersonality = "voice_personality"
	cfgVoiceRAGScope    = "voice_rag_scope"
)

// defaultVoicePersonality is the switchboard persona used when no
// `voice_personality` is configured. It is deliberately NOT the owner's personal
// assistant: a stranger on the phone must not meet an agent that manages the
// owner's mail, calendar and servers.
const defaultVoicePersonality = `Tu es la standardiste téléphonique de l'entreprise. Tu réponds à des appelants extérieurs que tu ne connais pas.

Ton rôle, strictement :
- accueillir poliment et comprendre la demande ;
- répondre aux questions générales à partir de la base de connaissance publique ;
- orienter l'appelant vers la bonne personne, ou proposer de prendre un message.

Tu n'es PAS un assistant personnel : tu n'as accès ni aux fichiers, ni aux serveurs, ni aux mails, ni à l'agenda de qui que ce soit, et tu ne dois jamais prétendre le contraire ni proposer ce genre de service. Si on te demande quelque chose hors de ton rôle, dis simplement que tu ne peux pas et propose de transmettre un message.

Ne divulgue jamais d'informations personnelles ou internes. Reste courtoise, brève et naturelle.`

// isServiceIdentity reports whether the request authenticated with the service
// token (PRISM_TOKEN) rather than as a real user. Only that identity is trusted
// to assert, via query params, which channel it speaks for.
func isServiceIdentity(u *memory.User) bool {
	return u != nil && u.ID == 0 && u.Email == serviceUser.Email
}

// voiceGuard denies every tool outside the given allow-list.
func voiceGuard(allowed map[string]bool) agent.ToolGuard {
	return func(name string, args map[string]interface{}) error {
		if allowed[name] {
			return nil
		}
		return fmt.Errorf("permission denied: %q is not available on a phone call", name)
	}
}

// voiceHiddenTools hides every built-in outside the allow-list, so the model never
// even offers them — cheaper than letting it call one and burn a turn on a denial.
func voiceHiddenTools(allowed map[string]bool) map[string]bool {
	hidden := make(map[string]bool, len(agent.ToolDefinitions))
	for _, t := range agent.ToolDefinitions {
		if n := t.Function.Name; !allowed[n] {
			hidden[n] = true
		}
	}
	return hidden
}

// resolveVoiceCaller maps a caller's phone number to a known user (identified by
// the phone on their profile), or nil for an anonymous caller. Empty number or no
// match → guest.
func (s *Server) resolveVoiceCaller(ctx context.Context, caller string) *memory.User {
	digits := normalizePhone(caller)
	if digits == "" {
		return nil
	}
	ms := s.store()
	if ms == nil {
		return nil
	}
	u, err := ms.UserByPhone(ctx, digits)
	if err != nil {
		return nil
	}
	return u
}

// voiceDirectory returns the phone directory (approved users with a number) — the
// live annuaire the switchboard can transfer to. Empty on error / no DB.
func (s *Server) voiceDirectory(ctx context.Context) []memory.DirEntry {
	ms := s.store()
	if ms == nil {
		return nil
	}
	entries, err := ms.DirectoryEntries(ctx)
	if err != nil {
		return nil
	}
	return entries
}

// voiceDirectoryText renders the directory for the agent's prompt so it knows who
// it can transfer to (and only names real people). Empty if the directory is empty.
func voiceDirectoryText(entries []memory.DirEntry) string {
	if len(entries) == 0 {
		return ""
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name
	}
	return "\n\n## Annuaire — personnes que tu peux joindre\n" +
		"Tu peux transférer l'appel (transfer_call) uniquement vers : " + strings.Join(names, ", ") + ".\n" +
		"Utilise le nom exact. Si l'appelant demande quelqu'un qui n'est pas dans cette liste, ne transfère pas : propose de prendre un message."
}

// resolveTransferName matches a name the agent passed to transfer_call against the
// directory and returns the canonical name + phone. Match is exact (case/accent-
// insensitive) then substring, so "Vincent" finds "Vincent Dupont".
func resolveTransferName(entries []memory.DirEntry, query string) (name, phone string, ok bool) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return "", "", false
	}
	for _, e := range entries { // exact display name
		if strings.ToLower(e.Name) == q {
			return e.Name, e.Phone, true
		}
	}
	for _, e := range entries { // query is a word of the name (first name, etc.)
		n := strings.ToLower(e.Name)
		if strings.Contains(n, q) || strings.Contains(q, n) {
			return e.Name, e.Phone, true
		}
	}
	return "", "", false
}

// voiceKnownPersonaNote is prepended to an identified caller's persona so the
// agent greets them by name and stays in spoken form. Their own personality and
// memory carry the rest (inter-channel continuity).
func voiceKnownPersonaNote(name string) string {
	return fmt.Sprintf(`Tu es au téléphone avec %s, que tu connais : c'est une personne identifiée par son numéro d'appel. Salue-la par son prénom, naturellement, et reprends le fil de vos échanges. Tu peux consulter ses informations et l'historique de vos conversations pour l'aider. Réponds à voix haute, en phrases courtes.

`, name)
}

// voicePersonality returns the configured switchboard persona, or the default.
func (s *Server) voicePersonality(ctx context.Context) string {
	if ms := s.store(); ms != nil {
		if v, ok, err := ms.GetConfig(ctx, cfgVoicePersonality); err == nil && ok && v != "" {
			return v
		}
	}
	return defaultVoicePersonality
}

// voiceRAGScope returns the RAG scope a caller may read, defaulting to an
// isolated one (no collections).
func (s *Server) voiceRAGScope(ctx context.Context) string {
	if ms := s.store(); ms != nil {
		if v, ok, err := ms.GetConfig(ctx, cfgVoiceRAGScope); err == nil && ok && v != "" {
			return v
		}
	}
	return voiceGuestScope
}

// handleVoiceConfig (GET/PUT /api/voice) manages the switchboard persona shown to
// unknown callers and the public RAG scope they may read. Admin-only; it shapes
// what every anonymous caller experiences. Lives behind the Téléphonie app, which
// only appears in Vortex mode.
func (s *Server) handleVoiceConfig(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil || !s.isAdminUser(r.Context(), u) {
		writeErr(w, http.StatusForbidden, "admin only")
		return
	}
	ms := s.store()
	if ms == nil {
		writeErr(w, http.StatusServiceUnavailable, "no database")
		return
	}

	switch r.Method {
	case http.MethodGet:
		rawScope, _, _ := ms.GetConfig(r.Context(), cfgVoiceRAGScope)
		writeJSON(w, map[string]any{
			"personality":        s.voicePersonality(r.Context()), // effective (stored or default)
			"defaultPersonality": defaultVoicePersonality,
			"ragScope":           rawScope, // "" = isolated (callers read nothing)
			"vortexMode":         s.cfg.VoxURL != "",
		})

	case http.MethodPut:
		var b struct {
			Personality string `json:"personality"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			writeErr(w, http.StatusBadRequest, "bad body")
			return
		}
		// The switchboard's knowledge base is a fixed reserved scope (voiceGuestScope),
		// managed via the RAG endpoints — no scope to set here, only the persona.
		if err := ms.SetConfig(r.Context(), cfgVoicePersonality, strings.TrimSpace(b.Personality)); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})

	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
