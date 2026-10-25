package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

// requireProjectWorkflowRole keeps workflow authority server-side. Members may
// intervene deliberately; agent callers must hold the named project binding.
func (h *Handler) requireProjectWorkflowRole(w http.ResponseWriter, r *http.Request, project db.Project, roles ...string) bool {
	userID, ok := requireUserID(w, r)
	if !ok {
		return false
	}
	actorType, actorID := h.resolveActor(r, userID, uuidToString(project.WorkspaceID))
	if actorType == "member" {
		return true
	}
	if actorType != "agent" {
		writeError(w, http.StatusForbidden, "invalid workflow actor")
		return false
	}
	bindings, err := h.Queries.ListProjectAgentBindings(r.Context(), db.ListProjectAgentBindingsParams{ProjectID: project.ID, WorkspaceID: project.WorkspaceID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load project agent bindings")
		return false
	}
	for _, binding := range bindings {
		if uuidToString(binding.AgentID) != actorID {
			continue
		}
		for _, role := range roles {
			if binding.Role == role {
				return true
			}
		}
	}
	writeError(w, http.StatusForbidden, "agent is not authorized for this project workflow action")
	return false
}

// requireProjectWorkflowEnabled prevents the workflow-only endpoints from
// turning an ordinary project into an automated workflow by accident. Opening
// the project Mica chat stays available without this gate: conversation is not
// an execution.
func (h *Handler) requireProjectWorkflowEnabled(w http.ResponseWriter, r *http.Request, project db.Project) bool {
	settings, err := h.Queries.GetProjectWorkflowSettings(r.Context(), db.GetProjectWorkflowSettingsParams{ProjectID: project.ID, WorkspaceID: project.WorkspaceID})
	if errors.Is(err, pgx.ErrNoRows) || !settings.Enabled {
		writeError(w, http.StatusConflict, "project workflow is disabled")
		return false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load project workflow settings")
		return false
	}
	return true
}

// requireProjectWorkflowIssueActor stops one project-bound agent from filing
// delivery or review evidence for another agent's execution issue. Members can
// still intervene intentionally; task-token agents must act only on their own
// assigned issue.
func (h *Handler) requireProjectWorkflowIssueActor(w http.ResponseWriter, r *http.Request, project db.Project, issueID pgtype.UUID) bool {
	userID, ok := requireUserID(w, r)
	if !ok {
		return false
	}
	actorType, actorID := h.resolveActor(r, userID, uuidToString(project.WorkspaceID))
	if actorType == "member" {
		return true
	}
	if actorType != "agent" {
		writeError(w, http.StatusForbidden, "invalid workflow actor")
		return false
	}
	issue, err := h.Queries.GetIssue(r.Context(), issueID)
	if err != nil || issue.WorkspaceID != project.WorkspaceID || !issue.ProjectID.Valid || issue.ProjectID != project.ID || !issue.AssigneeID.Valid || !issue.AssigneeType.Valid || issue.AssigneeType.String != "agent" || uuidToString(issue.AssigneeID) != actorID {
		writeError(w, http.StatusForbidden, "agent can only submit evidence for its assigned project issue")
		return false
	}
	return true
}

// OpenProjectMikaSession returns the member's project-scoped conversation with
// the workspace Mika. It never creates a new agent identity: one reusable
// agent can serve many projects, while each chat remains project-scoped.
func (h *Handler) OpenProjectMikaSession(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if !h.requireProjectWorkflowRole(w, r, project, "mika", "prd") {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	creatorID := parseUUID(userID)
	bindings, err := h.Queries.ListProjectAgentBindings(r.Context(), db.ListProjectAgentBindingsParams{ProjectID: project.ID, WorkspaceID: project.WorkspaceID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load project agent bindings")
		return
	}
	var mika db.Agent
	for _, binding := range bindings {
		if binding.Role != "mika" {
			continue
		}
		mika, err = h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{ID: binding.AgentID, WorkspaceID: project.WorkspaceID})
		if err != nil {
			writeError(w, http.StatusConflict, "the project's Mika is unavailable")
			return
		}
		break
	}
	if !mika.ID.Valid {
		mika, err = h.Queries.GetAgentBySystemKey(r.Context(), db.GetAgentBySystemKeyParams{
			WorkspaceID: project.WorkspaceID,
			SystemKey:   pgtype.Text{String: service.MikaSystemKey, Valid: true},
		})
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "choose a project Mika or set up Mika in this workspace")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to find Mika")
			return
		}
	}
	if session, err := h.Queries.GetProjectMikaChatSession(r.Context(), db.GetProjectMikaChatSessionParams{
		WorkspaceID: project.WorkspaceID, ProjectID: project.ID, AgentID: mika.ID, CreatorID: creatorID,
	}); err == nil {
		writeJSON(w, http.StatusOK, chatSessionToResponse(session))
		return
	} else if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to load project Mika chat")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start project Mika chat")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	if _, err := tx.Exec(r.Context(), "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", "project-mika:"+uuidToString(project.ID)+":"+userID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to lock project Mika chat")
		return
	}
	if session, err := qtx.GetProjectMikaChatSession(r.Context(), db.GetProjectMikaChatSessionParams{
		WorkspaceID: project.WorkspaceID, ProjectID: project.ID, AgentID: mika.ID, CreatorID: creatorID,
	}); err == nil {
		if err := tx.Commit(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to commit project Mika chat")
			return
		}
		writeJSON(w, http.StatusOK, chatSessionToResponse(session))
		return
	} else if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to load project Mika chat")
		return
	}
	if _, err := qtx.LockProjectForChatSessionCreate(r.Context(), db.LockProjectForChatSessionCreateParams{ID: project.ID, WorkspaceID: project.WorkspaceID}); err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	session, err := qtx.CreateChatSession(r.Context(), db.CreateChatSessionParams{ID: dbid.NewV7(), WorkspaceID: project.WorkspaceID, AgentID: mika.ID, CreatorID: creatorID, Title: "Mica · " + project.Title, ProjectID: project.ID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create project Mika chat")
		return
	}
	session, err = qtx.MarkChatSessionExplicitlyCreated(r.Context(), session.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to mark project Mika chat")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit project Mika chat")
		return
	}
	writeJSON(w, http.StatusCreated, chatSessionToResponse(session))
}

type ProjectExecutionResponse struct {
	ID                  string         `json:"id"`
	ProjectID           string         `json:"project_id"`
	WorkspaceID         string         `json:"workspace_id"`
	SourceChatSessionID string         `json:"source_chat_session_id"`
	SourceAgentID       string         `json:"source_agent_id"`
	Snapshot            map[string]any `json:"snapshot"`
	DeliveryResult      map[string]any `json:"delivery_result"`
	State               string         `json:"state"`
	ReviewRound         int32          `json:"review_round"`
	CreatedAt           string         `json:"created_at"`
}

func projectExecutionToResponse(record db.ProjectExecutionRecord) ProjectExecutionResponse {
	snapshot := map[string]any{}
	_ = json.Unmarshal(record.Snapshot, &snapshot)
	deliveryResult := map[string]any{}
	_ = json.Unmarshal(record.DeliveryResult, &deliveryResult)
	return ProjectExecutionResponse{ID: uuidToString(record.ID), ProjectID: uuidToString(record.ProjectID), WorkspaceID: uuidToString(record.WorkspaceID), SourceChatSessionID: uuidToString(record.SourceChatSessionID), SourceAgentID: uuidToString(record.SourceAgentID), Snapshot: snapshot, DeliveryResult: deliveryResult, State: record.State, ReviewRound: record.ReviewRound, CreatedAt: timestampToString(record.CreatedAt)}
}

func (h *Handler) ListProjectExecutionRecords(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	records, err := h.Queries.ListProjectExecutionRecords(r.Context(), db.ListProjectExecutionRecordsParams{ProjectID: project.ID, WorkspaceID: project.WorkspaceID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list project executions")
		return
	}
	response := make([]ProjectExecutionResponse, len(records))
	for i, record := range records {
		response[i] = projectExecutionToResponse(record)
	}
	issues, err := h.Queries.ListProjectExecutionIssuesForProject(r.Context(), db.ListProjectExecutionIssuesForProjectParams{ProjectID: project.ID, WorkspaceID: project.WorkspaceID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list project execution issues")
		return
	}
	issueResponse := make([]map[string]string, len(issues))
	for i, issue := range issues {
		issueResponse[i] = map[string]string{
			"execution_id": uuidToString(issue.ExecutionRecordID),
			"issue_id":     uuidToString(issue.IssueID),
			"role":         issue.Role,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"executions": response, "execution_issues": issueResponse, "total": len(response)})
}

type ConfirmProjectExecutionRequest struct {
	SourceChatSessionID string          `json:"source_chat_session_id"`
	SourceAgentID       string          `json:"source_agent_id"`
	Snapshot            json.RawMessage `json:"snapshot"`
}

// ConfirmProjectExecution freezes the PRD handoff. Later source-chat edits do
// not change this JSON snapshot; a new confirmation creates a new execution.
func (h *Handler) ConfirmProjectExecution(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if !h.requireProjectWorkflowEnabled(w, r, project) {
		return
	}
	if !h.requireProjectWorkflowRole(w, r, project, "mika", "prd") {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req ConfirmProjectExecutionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, req.SourceChatSessionID, "source_chat_session_id")
	if !ok {
		return
	}
	var snapshot map[string]any
	if err := json.Unmarshal(req.Snapshot, &snapshot); err != nil || snapshot == nil {
		writeError(w, http.StatusBadRequest, "snapshot must be a JSON object")
		return
	}
	if strings.TrimSpace(asWorkflowString(snapshot["goal"])) == "" {
		writeError(w, http.StatusBadRequest, "snapshot.goal is required")
		return
	}
	for _, key := range []string{"scope", "acceptance_criteria", "risks"} {
		if _, exists := snapshot[key]; !exists {
			writeError(w, http.StatusBadRequest, "snapshot."+key+" is required")
			return
		}
	}
	session, err := h.Queries.GetChatSession(r.Context(), sessionID)
	if err != nil || session.WorkspaceID != project.WorkspaceID || session.ProjectID != project.ID {
		writeError(w, http.StatusBadRequest, "source chat must belong to this project and agent")
		return
	}
	// The chat owns its source identity; callers must not guess a PRD agent
	// when a confirmed execution came directly from a Mica conversation.
	agentID := session.AgentID
	if req.SourceAgentID != "" {
		requestedAgentID, ok := parseUUIDOrBadRequest(w, req.SourceAgentID, "source_agent_id")
		if !ok {
			return
		}
		if requestedAgentID != agentID {
			writeError(w, http.StatusBadRequest, "source chat must belong to this project and agent")
			return
		}
	}
	bindings, err := h.Queries.ListProjectAgentBindings(r.Context(), db.ListProjectAgentBindingsParams{ProjectID: project.ID, WorkspaceID: project.WorkspaceID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load project agent bindings")
		return
	}
	sourceAuthorized := false
	for _, binding := range bindings {
		if binding.AgentID != agentID {
			continue
		}
		if binding.Role == "prd" || binding.Role == "mika" {
			sourceAuthorized = true
			break
		}
	}
	if !sourceAuthorized {
		writeError(w, http.StatusBadRequest, "source chat agent must be bound as this project's PRD or Mica")
		return
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid snapshot")
		return
	}
	record, err := h.Queries.CreateProjectExecutionRecord(r.Context(), db.CreateProjectExecutionRecordParams{WorkspaceID: project.WorkspaceID, ProjectID: project.ID, SourceChatSessionID: sessionID, SourceAgentID: agentID, Snapshot: payload, CreatedBy: parseUUID(userID)})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to confirm project execution")
		return
	}
	writeJSON(w, http.StatusCreated, projectExecutionToResponse(record))
}

func asWorkflowString(value any) string { text, _ := value.(string); return text }

type DispatchProjectExecutionRequest struct {
	ExecutionID string `json:"execution_id"`
	AgentID     string `json:"agent_id"`
	Role        string `json:"role"`
	Title       string `json:"title"`
}

// DispatchProjectExecution creates a normal Multica issue assigned to the
// project-bound agent. IssueService then performs the ordinary enqueue, so a
// workflow assignment always has a real run record rather than a UI-only flag.
func (h *Handler) DispatchProjectExecution(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if !h.requireProjectWorkflowEnabled(w, r, project) {
		return
	}
	if !h.requireProjectWorkflowRole(w, r, project, "mika") {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req DispatchProjectExecutionRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Role != "development" && req.Role != "review" && req.Role != "repair" {
		writeError(w, http.StatusBadRequest, "role must be development, review, or repair")
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	executionID, ok := parseUUIDOrBadRequest(w, req.ExecutionID, "execution_id")
	if !ok {
		return
	}
	agentID, ok := parseUUIDOrBadRequest(w, req.AgentID, "agent_id")
	if !ok {
		return
	}
	record, err := h.Queries.GetProjectExecutionRecord(r.Context(), db.GetProjectExecutionRecordParams{ID: executionID, ProjectID: project.ID, WorkspaceID: project.WorkspaceID})
	if err != nil {
		writeError(w, http.StatusNotFound, "project execution not found")
		return
	}
	if req.Role == "development" && record.State != string(service.ExecutionDevelopment) || req.Role == "review" && record.State != string(service.ExecutionReview) || req.Role == "repair" && record.State != string(service.ExecutionRepair) {
		writeError(w, http.StatusConflict, "execution is not ready for this assignment")
		return
	}
	bindings, err := h.Queries.ListProjectAgentBindings(r.Context(), db.ListProjectAgentBindingsParams{ProjectID: project.ID, WorkspaceID: project.WorkspaceID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load project agent bindings")
		return
	}
	requiredRole := map[string]string{
		"development": "developer",
		"repair":      "developer",
		"review":      "reviewer",
	}[req.Role]
	bound := false
	for _, binding := range bindings {
		if binding.AgentID == agentID && binding.Role == requiredRole {
			bound = true
			break
		}
	}
	if !bound {
		writeError(w, http.StatusBadRequest, "agent is not bound to this project role")
		return
	}
	roleContract := "Implement only the confirmed snapshot. Change code, run relevant tests, commit work, and submit delivery evidence. Do not change scope, dispatch other agents, release, or approve your own work."
	if req.Role == "review" {
		roleContract = "Inspect the confirmed snapshot, code, tests, commit or PR, and delivery evidence. Record findings only; do not edit code, dispatch work, or release."
	}
	if req.Role == "repair" {
		roleContract = "Fix the recorded review findings within the confirmed snapshot. Change code, run relevant tests, commit work, and submit updated delivery evidence. Do not change scope, dispatch work, release, or approve your own work."
	}
	completionCommand := fmt.Sprintf("multica project workflow delivery %s --execution %s --issue <this-issue-id> --commit <commit-ref> --tests <test-evidence> --summary <summary>", uuidToString(project.ID), uuidToString(record.ID))
	if req.Role == "review" {
		completionCommand = fmt.Sprintf("multica project workflow review %s --execution %s --issue <this-issue-id> --outcome passed|needs_changes|needs_member_decision --findings <review-evidence>", uuidToString(project.ID), uuidToString(record.ID))
	}
	roleContract += " Before changing this issue to in_review or done, successfully run the required_completion_command with this issue's actual ID and real evidence. A status change or issue comment alone does not advance this workflow."
	description, _ := json.Marshal(map[string]any{
		"project_execution_id":        uuidToString(record.ID),
		"prd_snapshot":                json.RawMessage(record.Snapshot),
		"delivery_evidence":           json.RawMessage(record.DeliveryResult),
		"workflow_role":               req.Role,
		"role_contract":               roleContract,
		"required_completion_command": completionCommand,
	})
	result, err := h.IssueService.Create(r.Context(), service.IssueCreateParams{WorkspaceID: project.WorkspaceID, Title: strings.TrimSpace(req.Title), Description: pgtype.Text{String: string(description), Valid: true}, Status: "todo", Priority: "none", AssigneeType: pgtype.Text{String: "agent", Valid: true}, AssigneeID: agentID, CreatorType: "member", CreatorID: parseUUID(userID), ProjectID: project.ID}, service.IssueCreateOpts{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to dispatch project execution")
		return
	}
	linked, err := h.Queries.LinkProjectExecutionIssue(r.Context(), db.LinkProjectExecutionIssueParams{ExecutionRecordID: record.ID, WorkspaceID: project.WorkspaceID, ProjectID: project.ID, IssueID: result.Issue.ID, Role: req.Role})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to link dispatched issue")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"issue": issueToResponse(result.Issue, uuidToString(project.WorkspaceID)), "execution_issue": linked, "task_id": uuidToPtr(result.AssignedTaskID)})
}

// StartProjectWorkflowExecutionRequest is the member confirmation payload a
// project Mica chat sends when the PRD is ready. One request starts the whole
// development stage atomically.
type StartProjectWorkflowExecutionRequest struct {
	SourceChatSessionID string          `json:"source_chat_session_id"`
	SourceAgentID       string          `json:"source_agent_id"`
	Snapshot            json.RawMessage `json:"snapshot"`
	Title               string          `json:"title"`
}

// StartProjectWorkflowExecutionFromChat is the P0 bridge: one member
// confirmation in a project Mica chat creates the execution record, the
// first development issue, the execution-issue link, and the agent run in
// one commit. It replaces the confirm-then-dispatch pair for the development
// stage; later stages still use the existing dispatch/delivery/review paths.
func (h *Handler) StartProjectWorkflowExecutionFromChat(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if !h.requireProjectWorkflowEnabled(w, r, project) {
		return
	}
	if !h.requireProjectWorkflowRole(w, r, project, "mika", "prd") {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req StartProjectWorkflowExecutionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(asWorkflowString(req.SourceChatSessionID)) == "" {
		writeError(w, http.StatusBadRequest, "source_chat_session_id is required")
		return
	}

	// Validate the source chat and snapshot exactly like ConfirmProjectExecution.
	session, snapshot, payload, ok := h.validateProjectWorkflowStart(w, r, project, req.SourceChatSessionID, req.SourceAgentID, req.Snapshot)
	if !ok {
		return
	}

	// An active execution already confirmed from this chat is an idempotent
	// replay, not a duplicate start.
	if existing, err := h.Queries.GetActiveProjectExecutionBySourceChat(r.Context(), db.GetActiveProjectExecutionBySourceChatParams{
		SourceChatSessionID: session.ID, WorkspaceID: project.WorkspaceID,
	}); err == nil {
		writeJSON(w, http.StatusOK, projectExecutionToResponse(existing))
		return
	} else if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to check existing project execution")
		return
	}

	bindings, err := h.Queries.ListProjectAgentBindings(r.Context(), db.ListProjectAgentBindingsParams{ProjectID: project.ID, WorkspaceID: project.WorkspaceID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load project agent bindings")
		return
	}
	var developerID pgtype.UUID
	for _, binding := range bindings {
		if binding.Role == "developer" {
			developerID = binding.AgentID
			break
		}
	}
	if !developerID.Valid {
		writeError(w, http.StatusConflict, "no developer agent is bound to this project")
		return
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		goal := strings.TrimSpace(asWorkflowString(snapshot["goal"]))
		if goal == "" {
			goal = project.Title
		}
		title = goal + " (development)"
	}

	roleContract := "Implement only the confirmed snapshot. Change code, run relevant tests, commit work, and submit delivery evidence. Do not change scope, dispatch other agents, release, or approve your own work."
	completionCommand := fmt.Sprintf("multica project workflow delivery %s --execution <this-execution-id> --issue <this-issue-id> --commit <commit-ref> --tests <test-evidence> --summary <summary>", uuidToString(project.ID))
	roleContract += " Before changing this issue to in_review or done, successfully run the required_completion_command with this issue's actual ID and real evidence. A status change or issue comment alone does not advance this workflow."
	description, _ := json.Marshal(map[string]any{
		"prd_snapshot":                json.RawMessage(payload),
		"workflow_role":               "development",
		"role_contract":               roleContract,
		"required_completion_command": completionCommand,
	})

	createParams := service.IssueCreateParams{
		WorkspaceID:  project.WorkspaceID,
		Title:        title,
		Description:  pgtype.Text{String: string(description), Valid: true},
		Status:       "todo",
		Priority:     "none",
		AssigneeType: pgtype.Text{String: "agent", Valid: true},
		AssigneeID:   developerID,
		CreatorType:  "member",
		CreatorID:    parseUUID(userID),
		ProjectID:    project.ID,
	}
	createOpts := service.IssueCreateOpts{ActorID: userID}

	ctx := r.Context()
	created, tx, err := h.IssueService.CreateWithTxForWorkflow(ctx, createParams, createOpts)
	if err != nil {
		if tx != nil {
			tx.Rollback(ctx)
		}
		writeError(w, http.StatusInternalServerError, "failed to create development issue")
		return
	}
	defer tx.Rollback(ctx)
	qtx := h.Queries.WithTx(tx)

	record, err := qtx.CreateProjectExecutionRecord(ctx, db.CreateProjectExecutionRecordParams{
		WorkspaceID:         project.WorkspaceID,
		ProjectID:           project.ID,
		SourceChatSessionID: session.ID,
		SourceAgentID:       session.AgentID,
		Snapshot:            payload,
		CreatedBy:           parseUUID(userID),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to confirm project execution")
		return
	}
	linked, err := qtx.LinkProjectExecutionIssue(ctx, db.LinkProjectExecutionIssueParams{
		ExecutionRecordID: record.ID,
		WorkspaceID:       project.WorkspaceID,
		ProjectID:         project.ID,
		IssueID:           created.Issue.ID,
		Role:              "development",
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to link dispatched issue")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start project execution")
		return
	}

	result := h.IssueService.CreatePostCommitForWorkflow(ctx, createParams, createOpts, created)
	writeJSON(w, http.StatusCreated, map[string]any{
		"execution":       projectExecutionToResponse(record),
		"issue":           issueToResponse(result.Issue, uuidToString(project.WorkspaceID)),
		"execution_issue": linked,
		"task_id":         uuidToPtr(result.AssignedTaskID),
	})
}

// validateProjectWorkflowStart checks the source chat belongs to this
// project, the chat agent is bound as the project's Mica or PRD agent, and
// the snapshot has the required structured fields. It returns the chat
// session, the decoded snapshot, and its re-encoded canonical payload.
func (h *Handler) validateProjectWorkflowStart(w http.ResponseWriter, r *http.Request, project db.Project, sourceChatSessionID, sourceAgentID string, rawSnapshot json.RawMessage) (db.ChatSession, map[string]any, json.RawMessage, bool) {
	sessionID, ok := parseUUIDOrBadRequest(w, sourceChatSessionID, "source_chat_session_id")
	if !ok {
		return db.ChatSession{}, nil, nil, false
	}
	var snapshot map[string]any
	if err := json.Unmarshal(rawSnapshot, &snapshot); err != nil || snapshot == nil {
		writeError(w, http.StatusBadRequest, "snapshot must be a JSON object")
		return db.ChatSession{}, nil, nil, false
	}
	if strings.TrimSpace(asWorkflowString(snapshot["goal"])) == "" {
		writeError(w, http.StatusBadRequest, "snapshot.goal is required")
		return db.ChatSession{}, nil, nil, false
	}
	for _, key := range []string{"scope", "acceptance_criteria", "risks"} {
		if _, exists := snapshot[key]; !exists {
			writeError(w, http.StatusBadRequest, "snapshot."+key+" is required")
			return db.ChatSession{}, nil, nil, false
		}
	}
	session, err := h.Queries.GetChatSession(r.Context(), sessionID)
	if err != nil || session.WorkspaceID != project.WorkspaceID || session.ProjectID != project.ID {
		writeError(w, http.StatusBadRequest, "source chat must belong to this project and agent")
		return db.ChatSession{}, nil, nil, false
	}
	if sourceAgentID != "" {
		requestedAgentID, ok := parseUUIDOrBadRequest(w, sourceAgentID, "source_agent_id")
		if !ok {
			return db.ChatSession{}, nil, nil, false
		}
		if requestedAgentID != session.AgentID {
			writeError(w, http.StatusBadRequest, "source chat must belong to this project and agent")
			return db.ChatSession{}, nil, nil, false
		}
	}
	bindings, err := h.Queries.ListProjectAgentBindings(r.Context(), db.ListProjectAgentBindingsParams{ProjectID: project.ID, WorkspaceID: project.WorkspaceID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load project agent bindings")
		return db.ChatSession{}, nil, nil, false
	}
	sourceAuthorized := false
	for _, binding := range bindings {
		if binding.AgentID != session.AgentID {
			continue
		}
		if binding.Role == "prd" || binding.Role == "mika" {
			sourceAuthorized = true
			break
		}
	}
	if !sourceAuthorized {
		writeError(w, http.StatusBadRequest, "source chat agent must be bound as this project's PRD or Mica")
		return db.ChatSession{}, nil, nil, false
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid snapshot")
		return db.ChatSession{}, nil, nil, false
	}
	return session, snapshot, payload, true
}

type LinkProjectExecutionIssueRequest struct {
	ExecutionID string `json:"execution_id"`
	IssueID     string `json:"issue_id"`
	Role        string `json:"role"`
}

func (h *Handler) LinkProjectExecutionIssue(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if !h.requireProjectWorkflowEnabled(w, r, project) {
		return
	}
	if !h.requireProjectWorkflowRole(w, r, project, "mika") {
		return
	}
	var req LinkProjectExecutionIssueRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	executionID, ok := parseUUIDOrBadRequest(w, req.ExecutionID, "execution_id")
	if !ok {
		return
	}
	issueID, ok := parseUUIDOrBadRequest(w, req.IssueID, "issue_id")
	if !ok {
		return
	}
	if req.Role != "development" && req.Role != "review" && req.Role != "repair" && req.Role != "delivery" {
		writeError(w, http.StatusBadRequest, "invalid execution issue role")
		return
	}
	if _, err := h.Queries.GetProjectExecutionRecord(r.Context(), db.GetProjectExecutionRecordParams{ID: executionID, ProjectID: project.ID, WorkspaceID: project.WorkspaceID}); err != nil {
		writeError(w, http.StatusNotFound, "project execution not found")
		return
	}
	issue, err := h.Queries.GetIssue(r.Context(), issueID)
	if err != nil || issue.WorkspaceID != project.WorkspaceID || !issue.ProjectID.Valid || issue.ProjectID != project.ID {
		writeError(w, http.StatusBadRequest, "issue must belong to this project")
		return
	}
	linked, err := h.Queries.LinkProjectExecutionIssue(r.Context(), db.LinkProjectExecutionIssueParams{ExecutionRecordID: executionID, WorkspaceID: project.WorkspaceID, ProjectID: project.ID, IssueID: issueID, Role: req.Role})
	if isUniqueViolation(err) {
		writeError(w, http.StatusConflict, "issue is already linked to a project execution")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to link project execution issue")
		return
	}
	writeJSON(w, http.StatusCreated, linked)
}

type SubmitProjectExecutionDeliveryRequest struct {
	ExecutionID string `json:"execution_id"`
	IssueID     string `json:"issue_id"`
	Commit      string `json:"commit"`
	Tests       string `json:"tests"`
	Summary     string `json:"summary"`
}

// SubmitProjectExecutionDelivery requires code, test, and change evidence
// before a delivery can enter review.
func (h *Handler) SubmitProjectExecutionDelivery(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if !h.requireProjectWorkflowEnabled(w, r, project) {
		return
	}
	if !h.requireProjectWorkflowRole(w, r, project, "developer") {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req SubmitProjectExecutionDeliveryRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Commit) == "" || strings.TrimSpace(req.Tests) == "" || strings.TrimSpace(req.Summary) == "" {
		writeError(w, http.StatusBadRequest, "commit, tests, and summary are required before review")
		return
	}
	executionID, ok := parseUUIDOrBadRequest(w, req.ExecutionID, "execution_id")
	if !ok {
		return
	}
	issueID, ok := parseUUIDOrBadRequest(w, req.IssueID, "issue_id")
	if !ok {
		return
	}
	if !h.requireProjectWorkflowIssueActor(w, r, project, issueID) {
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin workflow transition")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	record, err := qtx.LockProjectExecutionRecord(r.Context(), db.LockProjectExecutionRecordParams{ID: executionID, ProjectID: project.ID, WorkspaceID: project.WorkspaceID})
	if err != nil {
		writeError(w, http.StatusNotFound, "project execution not found")
		return
	}
	mika, err := h.projectWorkflowMika(r, project)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusConflict, "choose a project Mica before submitting delivery for review")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load project Mica")
		return
	}
	link, err := h.Queries.GetProjectExecutionIssue(r.Context(), db.GetProjectExecutionIssueParams{ExecutionRecordID: executionID, IssueID: issueID, ProjectID: project.ID, WorkspaceID: project.WorkspaceID})
	if err != nil || (link.Role != "development" && link.Role != "repair") {
		writeError(w, http.StatusBadRequest, "delivery must come from the linked development or repair issue")
		return
	}
	next, rounds, err := service.AdvanceProjectExecution(service.ProjectExecutionState(record.State), int(record.ReviewRound), service.ExecutionEventDeliverySubmitted)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	deliveryEvidence := map[string]any{}
	_ = json.Unmarshal(record.DeliveryResult, &deliveryEvidence)
	if deliveryEvidence == nil {
		deliveryEvidence = map[string]any{}
	}
	delete(deliveryEvidence, "review")
	deliveryEvidence["commit"] = strings.TrimSpace(req.Commit)
	deliveryEvidence["tests"] = strings.TrimSpace(req.Tests)
	deliveryEvidence["summary"] = strings.TrimSpace(req.Summary)
	recordWorkflowIssueEvidence(deliveryEvidence, issueID)
	evidence, _ := json.Marshal(deliveryEvidence)
	updated, err := qtx.UpdateProjectExecutionState(r.Context(), db.UpdateProjectExecutionStateParams{ID: record.ID, ProjectID: project.ID, WorkspaceID: project.WorkspaceID, State: string(next), ReviewRound: int32(rounds), DeliveryResult: evidence})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to submit project delivery")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit workflow transition")
		return
	}
	coordination, err := h.enqueueProjectWorkflowReviewCoordination(r, project, updated, mika, parseUUID(userID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "delivery was recorded but failed to queue Mica review coordination")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"execution":            projectExecutionToResponse(updated),
		"coordination_issue":   issueToResponse(coordination.Issue, uuidToString(project.WorkspaceID)),
		"coordination_task_id": uuidToPtr(coordination.AssignedTaskID),
	})
}

// projectWorkflowMika deliberately requires a project binding. A built-in Mika
// may exist in a workspace, but the workflow is an explicit project opt-in and
// must not silently give that agent authority over a project.
func (h *Handler) projectWorkflowMika(r *http.Request, project db.Project) (db.Agent, error) {
	bindings, err := h.Queries.ListProjectAgentBindings(r.Context(), db.ListProjectAgentBindingsParams{ProjectID: project.ID, WorkspaceID: project.WorkspaceID})
	if err != nil {
		return db.Agent{}, err
	}
	for _, binding := range bindings {
		if binding.Role != "mika" {
			continue
		}
		return h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{ID: binding.AgentID, WorkspaceID: project.WorkspaceID})
	}
	return db.Agent{}, pgx.ErrNoRows
}

// enqueueProjectWorkflowReviewCoordination gives the project Mica a normal
// queued task as soon as delivery first enters review. That task is the actual
// handoff record; it is not a UI-only "assigned" status.
func (h *Handler) enqueueProjectWorkflowReviewCoordination(r *http.Request, project db.Project, record db.ProjectExecutionRecord, mika db.Agent, creatorID pgtype.UUID) (service.IssueCreateResult, error) {
	snapshot := map[string]any{}
	_ = json.Unmarshal(record.Snapshot, &snapshot)
	goal := strings.TrimSpace(asWorkflowString(snapshot["goal"]))
	if goal == "" {
		goal = "project delivery"
	}
	description, err := json.Marshal(map[string]any{
		"project_execution_id": uuidToString(record.ID),
		"workflow_role":        "mika_review_coordination",
		"prd_snapshot":         json.RawMessage(record.Snapshot),
		"delivery_evidence":    json.RawMessage(record.DeliveryResult),
		"required_action":      "Inspect the submitted evidence, then dispatch the project-bound reviewer. Do not review code yourself.",
	})
	if err != nil {
		return service.IssueCreateResult{}, err
	}
	result, err := h.IssueService.Create(r.Context(), service.IssueCreateParams{
		WorkspaceID:  project.WorkspaceID,
		Title:        "Coordinate review: " + goal,
		Description:  pgtype.Text{String: string(description), Valid: true},
		Status:       "todo",
		Priority:     "none",
		AssigneeType: pgtype.Text{String: "agent", Valid: true},
		AssigneeID:   mika.ID,
		CreatorType:  "member",
		CreatorID:    creatorID,
		ProjectID:    project.ID,
	}, service.IssueCreateOpts{})
	if err != nil {
		return service.IssueCreateResult{}, err
	}
	if _, err := h.Queries.LinkProjectExecutionIssue(r.Context(), db.LinkProjectExecutionIssueParams{ExecutionRecordID: record.ID, WorkspaceID: project.WorkspaceID, ProjectID: project.ID, IssueID: result.Issue.ID, Role: "delivery"}); err != nil {
		return service.IssueCreateResult{}, err
	}
	return result, nil
}

type RecordProjectReviewRequest struct {
	ExecutionID string `json:"execution_id"`
	IssueID     string `json:"issue_id"`
	Outcome     string `json:"outcome"`
	Findings    string `json:"findings"`
}

type CompleteProjectDeliveryRequest struct {
	ExecutionID string          `json:"execution_id"`
	Evidence    json.RawMessage `json:"evidence"`
}

// CompleteProjectDelivery records project-specific delivery evidence. The
// caller chooses the delivery mechanism in settings; this endpoint does not
// assume Docker, a container, or any URL exists for every project.
func (h *Handler) CompleteProjectDelivery(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if !h.requireProjectWorkflowEnabled(w, r, project) {
		return
	}
	if !h.requireProjectWorkflowRole(w, r, project, "mika") {
		return
	}
	var req CompleteProjectDeliveryRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	executionID, ok := parseUUIDOrBadRequest(w, req.ExecutionID, "execution_id")
	if !ok {
		return
	}
	var evidence map[string]any
	if json.Unmarshal(req.Evidence, &evidence) != nil || len(evidence) == 0 {
		writeError(w, http.StatusBadRequest, "delivery evidence is required")
		return
	}
	record, err := h.Queries.GetProjectExecutionRecord(r.Context(), db.GetProjectExecutionRecordParams{ID: executionID, ProjectID: project.ID, WorkspaceID: project.WorkspaceID})
	if err != nil {
		writeError(w, http.StatusNotFound, "project execution not found")
		return
	}
	next, rounds, err := service.AdvanceProjectExecution(service.ProjectExecutionState(record.State), int(record.ReviewRound), service.ExecutionEventDeliveryCompleted)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	result := map[string]any{}
	_ = json.Unmarshal(record.DeliveryResult, &result)
	result["completion"] = evidence
	payload, _ := json.Marshal(result)
	updated, err := h.Queries.UpdateProjectExecutionState(r.Context(), db.UpdateProjectExecutionStateParams{ID: record.ID, ProjectID: project.ID, WorkspaceID: project.WorkspaceID, State: string(next), ReviewRound: int32(rounds), DeliveryResult: payload})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to complete project delivery")
		return
	}
	writeJSON(w, http.StatusOK, projectExecutionToResponse(updated))
}

func (h *Handler) RecordProjectReview(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if !h.requireProjectWorkflowEnabled(w, r, project) {
		return
	}
	if !h.requireProjectWorkflowRole(w, r, project, "reviewer") {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req RecordProjectReviewRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Findings) == "" {
		writeError(w, http.StatusBadRequest, "review findings are required")
		return
	}
	executionID, ok := parseUUIDOrBadRequest(w, req.ExecutionID, "execution_id")
	if !ok {
		return
	}
	issueID, ok := parseUUIDOrBadRequest(w, req.IssueID, "issue_id")
	if !ok {
		return
	}
	if !h.requireProjectWorkflowIssueActor(w, r, project, issueID) {
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin workflow transition")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	record, err := qtx.LockProjectExecutionRecord(r.Context(), db.LockProjectExecutionRecordParams{ID: executionID, ProjectID: project.ID, WorkspaceID: project.WorkspaceID})
	if err != nil {
		writeError(w, http.StatusNotFound, "project execution not found")
		return
	}
	link, err := h.Queries.GetProjectExecutionIssue(r.Context(), db.GetProjectExecutionIssueParams{ExecutionRecordID: executionID, IssueID: issueID, ProjectID: project.ID, WorkspaceID: project.WorkspaceID})
	if err != nil || link.Role != "review" {
		writeError(w, http.StatusBadRequest, "review result must come from the linked review issue")
		return
	}
	events := map[string]service.ProjectExecutionEvent{"passed": service.ExecutionEventReviewPassed, "needs_changes": service.ExecutionEventReviewNeedsChanges, "needs_member_decision": service.ExecutionEventReviewNeedsMemberDecision}
	event, ok := events[req.Outcome]
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid review outcome")
		return
	}
	next, rounds, err := service.AdvanceProjectExecution(service.ProjectExecutionState(record.State), int(record.ReviewRound), event)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	evidence := map[string]any{}
	_ = json.Unmarshal(record.DeliveryResult, &evidence)
	evidence["review"] = map[string]string{"outcome": req.Outcome, "findings": strings.TrimSpace(req.Findings)}
	recordWorkflowIssueEvidence(evidence, issueID)
	payload, err := json.Marshal(evidence)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record review evidence")
		return
	}
	updated, err := qtx.UpdateProjectExecutionState(r.Context(), db.UpdateProjectExecutionStateParams{ID: record.ID, ProjectID: project.ID, WorkspaceID: project.WorkspaceID, State: string(next), ReviewRound: int32(rounds), DeliveryResult: payload})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record review result")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit workflow transition")
		return
	}
	response := map[string]any{"execution": projectExecutionToResponse(updated)}
	if next == service.ExecutionRepair || next == service.ExecutionDelivering {
		mika, err := h.projectWorkflowMika(r, project)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "review was recorded but failed to load project Mica")
			return
		}
		phase := "repair"
		if next == service.ExecutionDelivering {
			phase = "delivery"
		}
		coordination, err := h.enqueueProjectWorkflowCoordination(r, project, updated, mika, parseUUID(userID), phase)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "review was recorded but failed to queue Mica coordination")
			return
		}
		response["coordination_issue"] = issueToResponse(coordination.Issue, uuidToString(project.WorkspaceID))
		response["coordination_task_id"] = uuidToPtr(coordination.AssignedTaskID)
	}
	writeJSON(w, http.StatusOK, response)
}

// enqueueProjectWorkflowCoordination creates an auditable Mica task for the
// next non-code phase. Mica then dispatches the bound developer or reviewer,
// or performs only the delivery work configured for this project.
func (h *Handler) enqueueProjectWorkflowCoordination(r *http.Request, project db.Project, record db.ProjectExecutionRecord, mika db.Agent, creatorID pgtype.UUID, phase string) (service.IssueCreateResult, error) {
	snapshot := map[string]any{}
	_ = json.Unmarshal(record.Snapshot, &snapshot)
	goal := strings.TrimSpace(asWorkflowString(snapshot["goal"]))
	if goal == "" {
		goal = "project delivery"
	}
	action := "Inspect the submitted evidence, then dispatch the project-bound reviewer. Do not review code yourself."
	if phase == "repair" {
		action = "Return the recorded review findings to the project-bound developer as a repair task, then coordinate a re-review. Do not modify code yourself."
	}
	if phase == "delivery" {
		action = "Perform only this project's configured delivery action and record its evidence. Do not assume Docker, containers, or health-check URLs."
	}
	settings, err := h.Queries.GetProjectWorkflowSettings(r.Context(), db.GetProjectWorkflowSettingsParams{ProjectID: project.ID, WorkspaceID: project.WorkspaceID})
	if err != nil {
		return service.IssueCreateResult{}, err
	}
	description, err := json.Marshal(map[string]any{
		"project_execution_id": uuidToString(record.ID),
		"workflow_role":        "mika_" + phase + "_coordination",
		"prd_snapshot":         json.RawMessage(record.Snapshot),
		"workflow_evidence":    json.RawMessage(record.DeliveryResult),
		"delivery_mode":        settings.DeliveryMode,
		"delivery_config":      json.RawMessage(settings.DeliveryConfig),
		"required_action":      action,
	})
	if err != nil {
		return service.IssueCreateResult{}, err
	}
	result, err := h.IssueService.Create(r.Context(), service.IssueCreateParams{
		WorkspaceID: project.WorkspaceID, Title: "Coordinate " + phase + ": " + goal,
		Description: pgtype.Text{String: string(description), Valid: true}, Status: "todo", Priority: "none",
		AssigneeType: pgtype.Text{String: "agent", Valid: true}, AssigneeID: mika.ID,
		CreatorType: "member", CreatorID: creatorID, ProjectID: project.ID,
	}, service.IssueCreateOpts{})
	if err != nil {
		return service.IssueCreateResult{}, err
	}
	if _, err := h.Queries.LinkProjectExecutionIssue(r.Context(), db.LinkProjectExecutionIssueParams{ExecutionRecordID: record.ID, WorkspaceID: project.WorkspaceID, ProjectID: project.ID, IssueID: result.Issue.ID, Role: "delivery"}); err != nil {
		return service.IssueCreateResult{}, err
	}
	return result, nil
}

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
