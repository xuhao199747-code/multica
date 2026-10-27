package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Project squad role template: a project may reference a squad as its role
// configuration template. The squad only fills prd / developer / reviewer
// bindings (source='squad'); the Mika coordinator binding stays project-local
// and never participates in sync or restore. The effective configuration
// always lives in project_agent_binding, so requireProjectWorkflowRole and
// the workflow dispatch code need no awareness of squads.

var squadTemplateRoles = []string{"prd", "developer", "reviewer"}

type SquadBindingRef struct {
	ID        string `json:"id"`
	AgentID   string `json:"agent_id"`
	AgentName string `json:"agent_name,omitempty"`
	Archived  bool   `json:"archived,omitempty"`
}

// squadRoleTemplateMapping loads the squad's standard role mapping and
// validates that every template role is covered by an unarchived agent
// member. It returns ok=false without writing a response when the squad is
// archived or the mapping is incomplete; callers map that to a 409 with the
// list of missing roles via writeSquadTemplateConflict.
func (h *Handler) squadRoleTemplateMapping(r *http.Request, squadID pgtype.UUID, workspaceID pgtype.UUID) (map[string]db.SquadMember, []string, bool) {
	members, err := h.Queries.ListSquadStandardRoleMembers(r.Context(), squadID)
	if err != nil {
		return nil, nil, false
	}
	mapping := make(map[string]db.SquadMember, len(members))
	for _, m := range members {
		if _, exists := mapping[m.StandardRole.String]; !exists {
			mapping[m.StandardRole.String] = m
		}
	}
	var missing []string
	for _, role := range squadTemplateRoles {
		m, covered := mapping[role]
		if !covered {
			missing = append(missing, role)
			continue
		}
		agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
			ID: m.MemberID, WorkspaceID: workspaceID,
		})
		if err != nil || agent.ArchivedAt.Valid {
			missing = append(missing, role)
		}
	}
	return mapping, missing, len(missing) == 0
}

func (h *Handler) writeSquadTemplateConflict(w http.ResponseWriter, r *http.Request, missing []string) {
	// Name the agents that already cover template roles so the inline error
	// can point at what is missing next to what exists.
	type conflict struct {
		MissingRoles []string `json:"missing_roles"`
	}
	writeErrorCode(w, http.StatusConflict, "squad_role_mapping_incomplete", string(mustJSON(conflict{MissingRoles: missing})))
}

func mustJSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("{}")
	}
	return raw
}

// squadTemplateDiff compares the project's current non-mika bindings against
// the squad mapping and computes the per-role change / remove / add preview
// shared by GET role-source and POST sync-squad.
func (h *Handler) squadTemplateDiff(r *http.Request, project db.Project, mapping map[string]db.SquadMember) map[string]any {
	bindings, err := h.Queries.ListProjectAgentBindings(r.Context(), db.ListProjectAgentBindingsParams{
		ProjectID: project.ID, WorkspaceID: project.WorkspaceID,
	})
	if err != nil {
		return map[string]any{"error": "failed to load project agent bindings"}
	}
	byRole := make(map[string]db.ProjectAgentBinding, len(bindings))
	for _, b := range bindings {
		if b.Role != "mika" {
			byRole[b.Role] = b
		}
	}
	changes := make([]map[string]any, 0)
	removals := make([]map[string]any, 0)
	adds := make([]map[string]any, 0)
	unchanged := make([]string, 0)
	for _, role := range squadTemplateRoles {
		current, hasCurrent := byRole[role]
		member, hasMember := mapping[role]
		switch {
		case hasCurrent && hasMember && uuidToString(current.AgentID) != uuidToString(member.MemberID):
			changes = append(changes, map[string]any{
				"role": role,
				"from": h.squadBindingRef(r, current),
				"to":   h.squadBindingRef(r, db.ProjectAgentBinding{AgentID: member.MemberID}),
			})
		case hasCurrent && !hasMember:
			removals = append(removals, map[string]any{
				"role":     role,
				"removing": h.squadBindingRef(r, current),
			})
		case !hasCurrent && hasMember:
			adds = append(adds, map[string]any{
				"role": role,
				"to":   h.squadBindingRef(r, db.ProjectAgentBinding{AgentID: member.MemberID}),
			})
		default:
			unchanged = append(unchanged, role)
		}
	}
	diff := map[string]any{
		"changes":   changes,
		"removals":  removals,
		"adds":      adds,
		"unchanged": unchanged,
	}
	diff["diff_hash"] = squadDiffHash(mapping, byRole)
	return diff
}

func (h *Handler) squadBindingRef(r *http.Request, binding db.ProjectAgentBinding) SquadBindingRef {
	ref := SquadBindingRef{ID: uuidToString(binding.ID), AgentID: uuidToString(binding.AgentID)}
	agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
		ID: binding.AgentID, WorkspaceID: binding.WorkspaceID,
	})
	if err == nil {
		ref.AgentName = agent.Name
		ref.Archived = agent.ArchivedAt.Valid
	}
	return ref
}

// squadDiffHash fingerprints the preview inputs so a sync can prove the squad
// mapping and the local bindings have not moved since the user confirmed the
// diff. sha over a canonical serialization keeps it independent of map order.
func squadDiffHash(mapping map[string]db.SquadMember, byRole map[string]db.ProjectAgentBinding) string {
	type pair struct{ Role, Agent string }
	pairs := make([]pair, 0, len(squadTemplateRoles))
	for _, role := range squadTemplateRoles {
		agent := ""
		if m, ok := mapping[role]; ok {
			agent = uuidToString(m.MemberID)
		}
		pairs = append(pairs, pair{Role: role, Agent: agent})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].Role < pairs[j].Role })
	var b strings.Builder
	for _, p := range pairs {
		b.WriteString(p.Role)
		b.WriteByte('=')
		b.WriteString(p.Agent)
		b.WriteByte(';')
	}
	for _, role := range squadTemplateRoles {
		b.WriteString("local:")
		b.WriteString(role)
		b.WriteByte('=')
		if cur, ok := byRole[role]; ok {
			b.WriteString(uuidToString(cur.AgentID))
		}
		b.WriteByte(';')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// requireSquadTemplateWriter gates the squad template write endpoints the same
// way the per-role binding writes are gated: workspace members pass, agents
// need the mika or prd project binding.
func (h *Handler) requireSquadTemplateWriter(w http.ResponseWriter, r *http.Request, project db.Project) bool {
	userID, ok := requireUserID(w, r)
	if !ok {
		return false
	}
	actorType, _ := h.resolveActor(r, userID, uuidToString(project.WorkspaceID))
	if actorType == "member" {
		return true
	}
	return h.requireProjectWorkflowRole(w, r, project, "mika", "prd")
}

type SetProjectCollaborationSquadRequest struct {
	SquadID *string `json:"squad_id"`
}

// SetProjectCollaborationSquad selects or clears the squad role template.
// Selecting validates the squad exists, is not archived, and covers every
// template role; the caller then confirms the generated preview via the diff
// in the response before committing. squad_id=null (or absent) clears the
// template without touching existing bindings.
func (h *Handler) SetProjectCollaborationSquad(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if !h.requireSquadTemplateWriter(w, r, project) {
		return
	}
	var req SetProjectCollaborationSquadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.SquadID == nil || strings.TrimSpace(*req.SquadID) == "" {
		updated, err := h.Queries.SetProjectCollaborationSquad(r.Context(), db.SetProjectCollaborationSquadParams{
			ID: project.ID, CollaborationSquadID: pgtype.UUID{}, WorkspaceID: project.WorkspaceID,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to clear collaboration squad")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"project_id": uuidToString(updated.ID), "squad_id": nil})
		return
	}
	squadUUID, ok := parseUUIDOrBadRequest(w, *req.SquadID, "squad_id")
	if !ok {
		return
	}
	squad, err := h.Queries.GetSquadInWorkspace(r.Context(), db.GetSquadInWorkspaceParams{
		ID: squadUUID, WorkspaceID: project.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "squad not found")
		return
	}
	if squad.ArchivedAt.Valid {
		writeError(w, http.StatusConflict, "squad is archived")
		return
	}
	mapping, missing, complete := h.squadRoleTemplateMapping(r, squad.ID, project.WorkspaceID)
	if !complete {
		h.writeSquadTemplateConflict(w, r, missing)
		return
	}
	if _, err := h.Queries.SetProjectCollaborationSquad(r.Context(), db.SetProjectCollaborationSquadParams{
		ID: project.ID, CollaborationSquadID: squad.ID, WorkspaceID: project.WorkspaceID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to set collaboration squad")
		return
	}
	diff := h.squadTemplateDiff(r, project, mapping)
	writeJSON(w, http.StatusOK, map[string]any{
		"project_id": uuidToString(project.ID),
		"squad_id":   uuidToString(squad.ID),
		"squad_name": squad.Name,
		"diff":       diff,
		"preview":    squadTemplatePreview(r, h, mapping),
	})
}

func squadTemplatePreview(r *http.Request, h *Handler, mapping map[string]db.SquadMember) []SquadBindingRef {
	preview := make([]SquadBindingRef, 0, len(squadTemplateRoles))
	for _, role := range squadTemplateRoles {
		m := mapping[role]
		preview = append(preview, h.squadBindingRef(r, db.ProjectAgentBinding{AgentID: m.MemberID, WorkspaceID: pgtype.UUID{}}))
	}
	return preview
}

// GetProjectRoleSource returns the project's squad template state: squad
// identity, current bindings with source flags, local override presence, and
// the sync diff preview.
func (h *Handler) GetProjectRoleSource(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	response := map[string]any{
		"project_id":        uuidToString(project.ID),
		"squad_id":          nil,
		"squad_name":        nil,
		"squad_archived":    false,
		"squad_missing":     false,
		"overrides_present": false,
		"bindings":          []ProjectAgentBindingResponse{},
		"diff":              map[string]any{"changes": []any{}, "removals": []any{}, "adds": []any{}, "unchanged": []string{}, "diff_hash": ""},
	}
	bindings, err := h.Queries.ListProjectAgentBindings(r.Context(), db.ListProjectAgentBindingsParams{
		ProjectID: project.ID, WorkspaceID: project.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list project agent bindings")
		return
	}
	bindingResponses := make([]ProjectAgentBindingResponse, len(bindings))
	overrides := false
	for i, b := range bindings {
		bindingResponses[i] = projectAgentBindingToResponse(b)
		if b.Role != "mika" && b.Source == "manual" {
			overrides = true
		}
	}
	response["bindings"] = bindingResponses
	response["overrides_present"] = overrides
	if !project.CollaborationSquadID.Valid {
		writeJSON(w, http.StatusOK, response)
		return
	}
	squad, err := h.Queries.GetSquadInWorkspace(r.Context(), db.GetSquadInWorkspaceParams{
		ID: project.CollaborationSquadID, WorkspaceID: project.WorkspaceID,
	})
	if err != nil {
		response["squad_missing"] = true
		writeJSON(w, http.StatusOK, response)
		return
	}
	response["squad_id"] = uuidToString(squad.ID)
	response["squad_name"] = squad.Name
	response["squad_archived"] = squad.ArchivedAt.Valid
	if squad.ArchivedAt.Valid {
		writeJSON(w, http.StatusOK, response)
		return
	}
	mapping, _, complete := h.squadRoleTemplateMapping(r, squad.ID, project.WorkspaceID)
	if !complete {
		// Mapping no longer covers every role: surface a diff with the
		// removals so the UI can explain why sync/restore are disabled.
		response["mapping_incomplete"] = true
	}
	response["diff"] = h.squadTemplateDiff(r, project, mapping)
	writeJSON(w, http.StatusOK, response)
}

type SyncProjectSquadRequest struct {
	DiffHash string `json:"diff_hash"`
}

// SyncProjectSquad applies the squad mapping to the project's non-mika
// bindings inside a single transaction. The client sends the diff_hash from
// its latest preview; any squad or binding change since then rejects the
// sync so the user re-previews instead of silently syncing stale state.
func (h *Handler) SyncProjectSquad(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if !h.requireSquadTemplateWriter(w, r, project) {
		return
	}
	if !project.CollaborationSquadID.Valid {
		writeError(w, http.StatusConflict, "project has no collaboration squad")
		return
	}
	var req SyncProjectSquadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	squad, err := h.Queries.GetSquadInWorkspace(r.Context(), db.GetSquadInWorkspaceParams{
		ID: project.CollaborationSquadID, WorkspaceID: project.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "squad not found")
		return
	}
	if squad.ArchivedAt.Valid {
		writeError(w, http.StatusConflict, "squad is archived")
		return
	}
	mapping, missing, complete := h.squadRoleTemplateMapping(r, squad.ID, project.WorkspaceID)
	if !complete {
		h.writeSquadTemplateConflict(w, r, missing)
		return
	}
	bindings, err := h.Queries.ListProjectAgentBindings(r.Context(), db.ListProjectAgentBindingsParams{
		ProjectID: project.ID, WorkspaceID: project.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list project agent bindings")
		return
	}
	byRole := make(map[string]db.ProjectAgentBinding, len(bindings))
	for _, b := range bindings {
		if b.Role != "mika" {
			byRole[b.Role] = b
		}
	}
	currentHash := squadDiffHash(mapping, byRole)
	if req.DiffHash == "" || req.DiffHash != currentHash {
		writeError(w, http.StatusConflict, "diff is stale, re-preview the squad changes")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start sync transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	if err := qtx.DeleteProjectAgentBindingsByRoles(r.Context(), db.DeleteProjectAgentBindingsByRolesParams{
		ProjectID: project.ID, WorkspaceID: project.WorkspaceID, Roles: squadTemplateRoles,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to replace squad bindings")
		return
	}
	for _, role := range squadTemplateRoles {
		m := mapping[role]
		if _, err := qtx.CreateProjectAgentBinding(r.Context(), db.CreateProjectAgentBindingParams{
			WorkspaceID: project.WorkspaceID, ProjectID: project.ID, AgentID: m.MemberID, Role: role, Source: "squad",
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to write squad bindings")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit squad sync")
		return
	}
	updated, err := h.Queries.ListProjectAgentBindings(r.Context(), db.ListProjectAgentBindingsParams{
		ProjectID: project.ID, WorkspaceID: project.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reload project agent bindings")
		return
	}
	resp := make([]ProjectAgentBindingResponse, len(updated))
	for i, b := range updated {
		resp[i] = projectAgentBindingToResponse(b)
	}
	writeJSON(w, http.StatusOK, map[string]any{"bindings": resp, "total": len(resp)})
}

// RestoreProjectSquadConfig drops every local (manual) override among the
// non-mika bindings and rebuilds the squad-source bindings from the squad's
// current mapping, in one transaction. Mika bindings are never touched.
func (h *Handler) RestoreProjectSquadConfig(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if !h.requireSquadTemplateWriter(w, r, project) {
		return
	}
	if !project.CollaborationSquadID.Valid {
		writeError(w, http.StatusConflict, "project has no collaboration squad")
		return
	}
	squad, err := h.Queries.GetSquadInWorkspace(r.Context(), db.GetSquadInWorkspaceParams{
		ID: project.CollaborationSquadID, WorkspaceID: project.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "squad not found")
		return
	}
	if squad.ArchivedAt.Valid {
		writeError(w, http.StatusConflict, "squad is archived")
		return
	}
	mapping, missing, complete := h.squadRoleTemplateMapping(r, squad.ID, project.WorkspaceID)
	if !complete {
		h.writeSquadTemplateConflict(w, r, missing)
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start restore transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	if err := qtx.DeleteProjectAgentBindingsBySource(r.Context(), db.DeleteProjectAgentBindingsBySourceParams{
		ProjectID: project.ID, WorkspaceID: project.WorkspaceID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clear squad bindings")
		return
	}
	if err := qtx.DeleteProjectAgentBindingsByRoles(r.Context(), db.DeleteProjectAgentBindingsByRolesParams{
		ProjectID: project.ID, WorkspaceID: project.WorkspaceID, Roles: squadTemplateRoles,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clear local overrides")
		return
	}
	for _, role := range squadTemplateRoles {
		m := mapping[role]
		if _, err := qtx.CreateProjectAgentBinding(r.Context(), db.CreateProjectAgentBindingParams{
			WorkspaceID: project.WorkspaceID, ProjectID: project.ID, AgentID: m.MemberID, Role: role, Source: "squad",
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to rebuild squad bindings")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit squad restore")
		return
	}
	updated, err := h.Queries.ListProjectAgentBindings(r.Context(), db.ListProjectAgentBindingsParams{
		ProjectID: project.ID, WorkspaceID: project.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reload project agent bindings")
		return
	}
	resp := make([]ProjectAgentBindingResponse, len(updated))
	for i, b := range updated {
		resp[i] = projectAgentBindingToResponse(b)
	}
	writeJSON(w, http.StatusOK, map[string]any{"bindings": resp, "total": len(resp)})
}
