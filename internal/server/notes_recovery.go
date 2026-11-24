package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"

	"github.com/jackc/pgx/v5"
	"prism/internal/notes"
)

func noteRecoveryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, pgx.ErrNoRows) || errors.Is(err, os.ErrNotExist):
		writeErr(w, 404, "Note or version not found or unavailable.")
	case errors.Is(err, notes.ErrRecoveryConflict):
		writeErr(w, 409, err.Error())
	default:
		writeErr(w, 500, "Could not recover this note: "+err.Error())
	}
}

// Recovery may reveal content removed from a published page. It is therefore
// limited to the same author/admin who may edit the page, including history GETs.
func (s *Server) recoveryProvider(w http.ResponseWriter, r *http.Request, id, scope string) (notes.Provider, string, bool) {
	if !s.pimStore(w) {
		return nil, "", false
	}
	u := currentUser(r)
	if s.cfg.MultiUser && (u == nil || u.ID < 0) {
		writeErr(w, 403, "A personal account is required to recover notes.")
		return nil, "", false
	}
	if scope != "" && scope != "personal" && scope != "group" {
		writeErr(w, 400, "Invalid note scope.")
		return nil, "", false
	}
	if scope == "group" {
		gscope, gid, ok := s.pimGroupScopeFor(r)
		if !ok {
			writeErr(w, 404, "Group notes are unavailable.")
			return nil, "", false
		}
		nid, err := strconv.ParseInt(id, 10, 64)
		if err != nil || nid < 1 {
			writeErr(w, 400, "Invalid note id.")
			return nil, "", false
		}
		n, err := s.store().GetNoteIncludingTrash(r.Context(), gscope, nid)
		if err != nil {
			noteRecoveryError(w, err)
			return nil, "", false
		}
		if !s.isGroupAdminOf(r.Context(), u, gid) && (u == nil || u.ID != n.OwnerID) {
			writeErr(w, 403, "Only the author or a group admin can recover this shared note.")
			return nil, "", false
		}
		return &notes.DBProvider{Store: s.store(), Session: gscope}, gscope, true
	}
	return notes.ProviderFor(r.Context(), s.userStore(r), s.pimScopeFor(r)), s.pimScopeFor(r), true
}

func (s *Server) handleNoteHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		writeErr(w, 405, "GET required.")
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		writeErr(w, 400, "A note id is required.")
		return
	}
	prov, _, ok := s.recoveryProvider(w, r, id, r.URL.Query().Get("scope"))
	if !ok {
		return
	}
	recovery, ok := prov.(notes.Recovery)
	if !ok {
		writeErr(w, 503, "Recovery is unavailable for this note source.")
		return
	}
	if version := r.URL.Query().Get("version"); version != "" {
		v, err := recovery.ReadVersion(r.Context(), id, version)
		if err != nil {
			noteRecoveryError(w, err)
			return
		}
		writeJSON(w, map[string]any{"version": v, "source": prov.Kind()})
		return
	}
	versions, err := recovery.History(r.Context(), id, r.URL.Query().Get("before"))
	if err != nil {
		noteRecoveryError(w, err)
		return
	}
	out := map[string]any{"versions": versions, "source": prov.Kind()}
	if len(versions) == 50 {
		out["nextBefore"] = versions[len(versions)-1].ID
	}
	writeJSON(w, out)
}

func (s *Server) handleNoteTrash(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		writeErr(w, 405, "GET required.")
		return
	}
	prov, _, ok := s.recoveryProvider(w, r, "", "personal")
	if !ok {
		return
	}
	recovery, ok := prov.(notes.Recovery)
	if !ok {
		writeErr(w, 503, "Recovery is unavailable for this note source.")
		return
	}
	items, err := recovery.Trash(r.Context())
	if err != nil {
		noteRecoveryError(w, err)
		return
	}
	out := []map[string]any{}
	for _, n := range items {
		j := noteJSON(n.ID, n.Title, "", n.Tags, n.CreatedAt, n.UpdatedAt, "personal", true, 0, 0, n.Aliases)
		j["deletedAt"] = n.DeletedAt
		out = append(out, j)
	}
	if gscope, gid, ok := s.pimGroupScopeFor(r); ok {
		u := currentUser(r)
		admin := s.isGroupAdminOf(r.Context(), u, gid)
		items, err := s.store().TrashedNotes(r.Context(), gscope)
		if err != nil {
			noteRecoveryError(w, err)
			return
		}
		for _, n := range items {
			if !admin && (u == nil || u.ID != n.OwnerID) {
				continue
			}
			j := noteJSON(strconv.FormatInt(n.ID, 10), n.Title, "", n.Tags, n.CreatedAt, n.UpdatedAt, "group", true, n.OwnerID, n.OriginID, n.Aliases)
			j["deletedAt"] = n.DeletedAt
			out = append(out, j)
		}
	}
	writeJSON(w, map[string]any{"notes": out, "source": prov.Kind()})
}

func (s *Server) handleNoteRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "POST required.")
		return
	}
	var b struct {
		ID      string `json:"id"`
		Scope   string `json:"scope"`
		Version string `json:"version"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&b) != nil || b.ID == "" {
		writeErr(w, 400, "A note id and optional version are required.")
		return
	}
	prov, scope, ok := s.recoveryProvider(w, r, b.ID, b.Scope)
	if !ok {
		return
	}
	recovery, ok := prov.(notes.Recovery)
	if !ok {
		writeErr(w, 503, "Recovery is unavailable for this note source.")
		return
	}
	id, err := recovery.Restore(r.Context(), b.ID, b.Version)
	if err != nil {
		noteRecoveryError(w, err)
		return
	}
	s.emitDataChange(scope, "notes")
	writeJSON(w, map[string]any{"id": id})
}
