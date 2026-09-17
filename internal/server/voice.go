package server

// Voice-channel identity (Prism Vox dock).
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
	"log"
	"net/http"
	"sort"
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
	// Set once the persona above has been handed to Vox, which owns it from then
	// on. A flag, not a text comparison: an admin who deliberately reverted to the
	// default must not have their old words pushed back over it.
	cfgVoicePersonaMigrated = "voice_personality_migrated"
	cfgVoiceRAGScope        = "voice_rag_scope"
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
// directory and returns the canonical name + phone. Match is exact (case-, accent-
// and hyphen-insensitive) then a whole word of the name, so "Vincent" finds
// "Vincent Dupont". The match must be unique: with "Jean Dupont" and "Jean
// Martin" listed, "Jean" resolves to nothing and the agent has to ask — it must
// never pick a colleague at random.
func resolveTransferName(entries []memory.DirEntry, query string) (memory.DirEntry, bool) {
	q := normalizeDirectoryName(query)
	if q == "" {
		return memory.DirEntry{}, false
	}
	for _, exact := range []bool{true, false} {
		var match *memory.DirEntry
		for i := range entries {
			n := normalizeDirectoryName(entries[i].Name)
			matches := n == q
			if !exact {
				matches = strings.Contains(" "+n+" ", " "+q+" ")
			}
			if matches {
				if match != nil {
					return memory.DirEntry{}, false // ambiguous
				}
				match = &entries[i]
			}
		}
		if match != nil {
			return *match, true
		}
	}
	return memory.DirEntry{}, false
}

func normalizeDirectoryName(s string) string {
	r := strings.NewReplacer("é", "e", "è", "e", "ê", "e", "ë", "e",
		"à", "a", "â", "a", "ä", "a", "î", "i", "ï", "i",
		"ô", "o", "ö", "o", "ù", "u", "û", "u", "ü", "u", "ç", "c", "œ", "oe", "-", " ")
	return strings.Join(strings.Fields(r.Replace(strings.ToLower(s))), " ")
}

// voiceTurnContent prepends the gateway's local outcomes (transfer failed,
// message noted, caller interrupted…) to the caller's words, clearly labelled as
// data so the agent doesn't take them for speech. Only an authenticated voice
// connection may assert gateway facts; a browser message never carries them.
func voiceTurnContent(content string, facts []string, voiceCall bool) string {
	if !voiceCall || len(facts) == 0 {
		return content
	}
	if len(facts) > 8 {
		facts = facts[len(facts)-8:]
	}
	data, _ := json.Marshal(facts)
	return "[Compte rendu de la passerelle téléphonique — données, pas paroles de l'appelant : " + string(data) + "]\n\nAppelant : " + content
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
// only appears when a Prism Vox stack is docked.
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
		// Docked, the switchboard runs on Vox with Vox's prompt, so that is the one
		// to show and edit — anything else would be a form that changes nothing.
		persona := s.voicePersonality(r.Context())
		if s.cfg.VoxURL != "" {
			s.migrateVoicePersonaToVox(r.Context())
			if p, err := s.voxSystemPrompt(r.Context()); err == nil {
				persona = p
			} else {
				log.Printf("[voice] Vox unreachable, showing the local persona: %v", err)
			}
		}
		writeJSON(w, map[string]any{
			"personality":        persona,
			"defaultPersonality": defaultVoicePersonality,
			"ragScope":           rawScope, // "" = isolated (callers read nothing)
			"voxDocked":          s.cfg.VoxURL != "",
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
		persona := strings.TrimSpace(b.Personality)
		if s.cfg.VoxURL != "" {
			// One store, Vox's. Writing here as well would leave two copies to
			// disagree, and the one that answers the phone would not be this one.
			if err := s.setVoxSystemPrompt(r.Context(), persona); err != nil {
				writeErr(w, http.StatusBadGateway, "the phone stack did not accept the change: "+err.Error())
				return
			}
			writeJSON(w, map[string]any{"ok": true})
			return
		}
		if err := ms.SetConfig(r.Context(), cfgVoicePersonality, persona); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})

	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ── What Prism tells Vox about people ──────────────────────────────────────────
//
// Two questions, two endpoints, both read-only and both answered from the user
// table. They exist because the routing decision belongs to Vox — it must know
// who is calling BEFORE it picks a brain and before it speaks its greeting — while
// the answer only exists here.
//
// Admin-gated, which the service token satisfies (auth.go resolves it to a
// synthetic global admin): a phone directory is not member-readable.

// handleVoiceCaller (GET /api/voice/caller?number=…) answers the single question
// Vox must settle before it answers the line: is this one of ours?
//
// A match is *not* authentication — caller ID is trivially forged. It decides
// which agent picks up and which greeting is spoken, nothing more; every
// dangerous tool stays off the voice channel whoever is calling (see the top of
// this file).
func (s *Server) handleVoiceCaller(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil || !s.isAdminUser(r.Context(), u) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	caller := s.resolveVoiceCaller(r.Context(), r.URL.Query().Get("number"))
	if caller == nil {
		writeJSON(w, map[string]interface{}{"known": false})
		return
	}
	writeJSON(w, map[string]interface{}{
		"known": true,
		// The display name is what Vox greets them with. It never sends back an
		// email or an id: Vox has no use for either, and a switchboard should not
		// hold a copy of the user table.
		"name": caller.DisplayName,
	})
}

// handleVoiceDirectory (GET /api/voice/directory) lists who a call can be
// transferred to: approved users with a phone number, and nobody else.
//
// This is the whole transfer surface. Vox's own contacts table is for people it
// CALLS (see its outbound directory) — being transferable means having an account
// here. The two lists are deliberately separate and must never be merged into one
// prompt, or the switchboard will offer to put a caller through to a supplier.
func (s *Server) handleVoiceDirectory(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil || !s.isAdminUser(r.Context(), u) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	entries := s.voiceDirectory(r.Context())
	out := make([]map[string]interface{}, 0, len(entries))
	for _, e := range entries {
		out = append(out, map[string]interface{}{
			"name": e.Name, "phone": e.Phone,
			// How to hand the call over, decided by each person on their own profile.
			"transfer": e.Transfer,
			// The id is for Prism's own admin table, which edits these rows; Vox
			// ignores it. One list, read by both, beats a near-duplicate endpoint.
			"id": e.UserID,
		})
	}
	writeJSON(w, map[string]interface{}{"entries": out})
}

// ── Who the agent is on an internal call ───────────────────────────────────────

// defaultInternalVoicePersonality is who the agent is on the phone with someone
// the deployment knows, until a group writes its own.
const defaultInternalVoicePersonality = `Tu es l'assistant de la personne au bout du fil, qui fait partie de la maison. Tu la connais : tu as accès à son profil, à sa mémoire et à ses connaissances, et tu t'en sers pour l'aider concrètement.

Tu es direct, chaleureux et efficace. Tu vas droit au but — au téléphone, une phrase de politesse de plus est une phrase d'attente de plus. Tu ne récites pas ce que tu vas faire : tu le fais, puis tu dis ce que ça donne.

Si une demande est ambiguë, tu poses UNE question, la plus utile, et tu attends la réponse.`

// voiceInternalPersonality returns who the agent is on a call with someone the
// deployment recognises: the group's internal-call text, or the built-in one.
//
// It REPLACES the caller's own personality rather than layering on top of it, and
// that is a deliberate trade. On this stack, a dense personality measurably
// destroys tool calling — and on a phone line the tools *are* the function: with
// no transfer_call, no take_message and no end_call, the agent chats pleasantly
// while the line stays open. Free-form user text does not belong on that path.
//
// What the caller keeps is everything that actually carries continuity: their
// profile, their memory, their past conversations, their knowledge. It is the
// character that changes between the dashboard and the phone, not the knowledge.
//
// Which group speaks for them is the default-group question, answered once in
// memory.DefaultGroupID rather than guessed here.
func (s *Server) voiceInternalPersonality(ctx context.Context, u *memory.User) string {
	ms := s.store()
	if ms == nil || u == nil {
		return defaultInternalVoicePersonality
	}
	gid, ok := ms.DefaultGroupID(ctx, u.ID)
	if !ok {
		// In no group at all: nobody has written an internal-call persona for
		// them, so the built-in one applies. Not the switchboard's — they are
		// recognised, and being told "you have reached the switchboard" by an
		// agent that knows their name is worse than a generic assistant.
		return defaultInternalVoicePersonality
	}
	cfg, err := ms.GetRoomConfig(ctx, gid)
	if err != nil {
		return defaultInternalVoicePersonality
	}
	if p := strings.TrimSpace(cfg.AgentVoicePrompt); p != "" {
		return p
	}
	return defaultInternalVoicePersonality
}

// ── The switchboard's knowledge base, read from Vox ─────────────────────────────

// voiceSearchLimit caps how much a single lookup returns. A caller cannot skim,
// and the agent has to turn whatever comes back into one or two spoken sentences,
// so a wider net just costs time on a line where silence is expensive.
const voiceSearchLimit = 5

// handleVoiceSearch (POST /api/voice/search {query}) searches the switchboard's
// reserved knowledge base and returns the matching passages.
//
// It exists because the switchboard now runs on Vox's own brain, while the
// knowledge stays here — configured once, in one place, by an admin who should
// not have to think about which stack stores what.
//
// The request carries TEXT, never a vector, and that is not negotiable: the query
// has to be embedded by the same model that indexed the corpus. A vector produced
// by Vox's own embedder would describe a different space and come back with
// confident nonsense.
func (s *Server) handleVoiceSearch(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil || !s.isAdminUser(r.Context(), u) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var b struct {
		Query string `json:"query"`
	}
	if json.NewDecoder(r.Body).Decode(&b) != nil || strings.TrimSpace(b.Query) == "" {
		writeErr(w, http.StatusBadRequest, "query required")
		return
	}
	if s.ragStore == nil || s.ragEmbedder == nil {
		writeErr(w, http.StatusServiceUnavailable, "no knowledge base on this deployment")
		return
	}

	embedding, err := s.ragEmbedder.Embed(r.Context(), strings.TrimSpace(b.Query))
	if err != nil {
		writeErr(w, http.StatusBadGateway, "could not embed the question: "+err.Error())
		return
	}

	// The switchboard reads one reserved scope and nothing else. Searching every
	// collection in it (rather than asking the caller to name one) is what makes
	// "one knowledge base, configured once" true from the agent's side.
	cols, err := s.ragStore.ListCollections(r.Context(), voiceGuestScope)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	type hit struct {
		Content  string  `json:"content"`
		Source   string  `json:"source"`
		Score    float64 `json:"score"`
		pageless bool
	}
	var hits []hit
	for _, c := range cols {
		res, err := s.ragStore.Search(r.Context(), c.Name, embedding, voiceSearchLimit)
		if err != nil {
			continue
		}
		for _, x := range res {
			hits = append(hits, hit{Content: x.Content, Source: x.Filename, Score: x.Score})
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > voiceSearchLimit {
		hits = hits[:voiceSearchLimit]
	}
	writeJSON(w, map[string]interface{}{"hits": hits})
}
