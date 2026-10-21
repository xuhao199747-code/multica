package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ProjectWorkflowSettingsResponse is deliberately separate from Project. The
// workflow is opt-in and its operational details should not inflate every
// project-list response.
type ProjectWorkflowSettingsResponse struct {
	ProjectID      string          `json:"project_id"`
	WorkspaceID    string          `json:"workspace_id"`
	Enabled        bool            `json:"enabled"`
	ProjectContext string          `json:"project_context"`
	DeliveryMode   string          `json:"delivery_mode"`
	DeliveryConfig json.RawMessage `json:"delivery_config"`
	UpdatedAt      string          `json:"updated_at,omitempty"`
}

func projectWorkflowSettingsToResponse(settings db.ProjectWorkflowSetting) ProjectWorkflowSettingsResponse {
	config := json.RawMessage(settings.DeliveryConfig)
	if len(config) == 0 {
		config = json.RawMessage("{}")
	}
	return ProjectWorkflowSettingsResponse{
		ProjectID:      uuidToString(settings.ProjectID),
		WorkspaceID:    uuidToString(settings.WorkspaceID),
		Enabled:        settings.Enabled,
		ProjectContext: settings.ProjectContext,
		DeliveryMode:   settings.DeliveryMode,
		DeliveryConfig: config,
		UpdatedAt:      timestampToString(settings.UpdatedAt),
	}
}

func defaultProjectWorkflowSettings(project db.Project) ProjectWorkflowSettingsResponse {
	return ProjectWorkflowSettingsResponse{
		ProjectID:      uuidToString(project.ID),
		WorkspaceID:    uuidToString(project.WorkspaceID),
		Enabled:        false,
		DeliveryMode:   string(service.ProjectDeliveryNotifyOnly),
		DeliveryConfig: json.RawMessage("{}"),
	}
}

type UpdateProjectWorkflowSettingsRequest struct {
	Enabled        bool            `json:"enabled"`
	ProjectContext string          `json:"project_context"`
	DeliveryMode   string          `json:"delivery_mode"`
	DeliveryConfig json.RawMessage `json:"delivery_config"`
}

// GetProjectWorkflowSettings returns defaults for projects that have never
// enabled the workflow. A missing row is not an error: workflow is optional.
func (h *Handler) GetProjectWorkflowSettings(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	settings, err := h.Queries.GetProjectWorkflowSettings(r.Context(), db.GetProjectWorkflowSettingsParams{
		ProjectID: project.ID, WorkspaceID: project.WorkspaceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusOK, defaultProjectWorkflowSettings(project))
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load project workflow settings")
		return
	}
	writeJSON(w, http.StatusOK, projectWorkflowSettingsToResponse(settings))
}

// UpdateProjectWorkflowSettings persists the project-level opt-in and delivery
// policy. It never starts an execution: Mica creates an explicit confirmation
// before any developer or reviewer receives a task.
func (h *Handler) UpdateProjectWorkflowSettings(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	var req UpdateProjectWorkflowSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	mode, err := service.ParseProjectDeliveryMode(req.DeliveryMode)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	context := strings.TrimSpace(req.ProjectContext)
	config := req.DeliveryConfig
	if len(config) == 0 {
		config = json.RawMessage("{}")
	}
	var decodedConfig any
	if err := json.Unmarshal(config, &decodedConfig); err != nil || decodedConfig == nil {
		writeError(w, http.StatusBadRequest, "delivery_config must be a JSON object")
		return
	}
	if _, ok := decodedConfig.(map[string]any); !ok {
		writeError(w, http.StatusBadRequest, "delivery_config must be a JSON object")
		return
	}
	settings, err := h.Queries.UpsertProjectWorkflowSettings(r.Context(), db.UpsertProjectWorkflowSettingsParams{
		ProjectID:      project.ID,
		WorkspaceID:    project.WorkspaceID,
		Enabled:        req.Enabled,
		ProjectContext: context,
		DeliveryMode:   string(mode),
		DeliveryConfig: config,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save project workflow settings")
		return
	}
	writeJSON(w, http.StatusOK, projectWorkflowSettingsToResponse(settings))
}

// ProjectAgentBindingResponse represents a project-scoped role. The agent
// identity remains reusable across projects; only this role binding is scoped.
type ProjectAgentBindingResponse struct {
	ID          string `json:"id"`
	ProjectID   string `json:"project_id"`
	WorkspaceID string `json:"workspace_id"`
	AgentID     string `json:"agent_id"`
	Role        string `json:"role"`
	CreatedAt   string `json:"created_at"`
}

func projectAgentBindingToResponse(binding db.ProjectAgentBinding) ProjectAgentBindingResponse {
	return ProjectAgentBindingResponse{
		ID: uuidToString(binding.ID), ProjectID: uuidToString(binding.ProjectID),
		WorkspaceID: uuidToString(binding.WorkspaceID), AgentID: uuidToString(binding.AgentID),
		Role: binding.Role, CreatedAt: timestampToString(binding.CreatedAt),
	}
}

type CreateProjectAgentBindingRequest struct {
	AgentID string `json:"agent_id"`
	Role    string `json:"role"`
}

func validProjectAgentRole(role string) bool {
	switch role {
	case "mika", "prd", "developer", "reviewer":
		return true
	default:
		return false
	}
}

func (h *Handler) ListProjectAgentBindings(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	bindings, err := h.Queries.ListProjectAgentBindings(r.Context(), db.ListProjectAgentBindingsParams{
		ProjectID: project.ID, WorkspaceID: project.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list project agent bindings")
		return
	}
	response := make([]ProjectAgentBindingResponse, len(bindings))
	for i, binding := range bindings {
		response[i] = projectAgentBindingToResponse(binding)
	}
	writeJSON(w, http.StatusOK, map[string]any{"bindings": response, "total": len(response)})
}

func (h *Handler) CreateProjectAgentBinding(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	var req CreateProjectAgentBindingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	role := strings.TrimSpace(req.Role)
	if !validProjectAgentRole(role) {
		writeError(w, http.StatusBadRequest, "role must be mika, prd, developer, or reviewer")
		return
	}
	agentID, ok := parseUUIDOrBadRequest(w, req.AgentID, "agent_id")
	if !ok {
		return
	}
	if _, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
		ID: agentID, WorkspaceID: project.WorkspaceID,
	}); err != nil {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	binding, err := h.Queries.CreateProjectAgentBinding(r.Context(), db.CreateProjectAgentBindingParams{
		WorkspaceID: project.WorkspaceID, ProjectID: project.ID, AgentID: agentID, Role: role,
	})
	if isUniqueViolation(err) {
		if role == "mika" {
			writeError(w, http.StatusConflict, "this project already has a Mika binding")
			return
		}
		writeError(w, http.StatusConflict, "this agent already has this project role")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create project agent binding")
		return
	}
	writeJSON(w, http.StatusCreated, projectAgentBindingToResponse(binding))
}

func (h *Handler) DeleteProjectAgentBinding(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	bindingID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "bindingId"), "binding id")
	if !ok {
		return
	}
	deleted, err := h.Queries.DeleteProjectAgentBinding(r.Context(), db.DeleteProjectAgentBindingParams{
		ID: bindingID, ProjectID: project.ID, WorkspaceID: project.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete project agent binding")
		return
	}
	if deleted == 0 {
		writeError(w, http.StatusNotFound, "project agent binding not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
