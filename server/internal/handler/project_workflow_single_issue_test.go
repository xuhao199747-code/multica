package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestProjectWorkflowAdvancesSingleMainIssue(t *testing.T) {
	w := httptest.NewRecorder()
	testHandler.CreateProject(w, newRequest(http.MethodPost, "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Single workflow issue project",
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", w.Code, w.Body.String())
	}
	var project ProjectResponse
	if err := json.NewDecoder(w.Body).Decode(&project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		testHandler.DeleteProject(httptest.NewRecorder(), withURLParam(newRequest(http.MethodDelete, "/api/projects/"+project.ID, nil), "id", project.ID))
	})
	enableProjectWorkflowForTest(t, project.ID)

	mikaID := createHandlerTestAgent(t, "single-issue-mika", nil)
	developerID := createHandlerTestAgent(t, "single-issue-developer", nil)
	prdID := createHandlerTestAgent(t, "single-issue-prd", nil)
	reviewerID := createHandlerTestAgent(t, "single-issue-reviewer", nil)
	for _, binding := range []struct {
		agentID string
		role    string
	}{
		{mikaID, "mika"},
		{developerID, "developer"},
		{prdID, "prd"},
		{reviewerID, "reviewer"},
	} {
		w = httptest.NewRecorder()
		testHandler.CreateProjectAgentBinding(w, withURLParam(newRequest(http.MethodPost, "/api/projects/"+project.ID+"/workflow/agents", map[string]any{
			"agent_id": binding.agentID,
			"role":     binding.role,
		}), "id", project.ID))
		if w.Code != http.StatusCreated {
			t.Fatalf("bind %s: %d %s", binding.role, w.Code, w.Body.String())
		}
	}

	sessionID := createChatSessionWithProjectForTest(t, prdID, project.ID)
	w = httptest.NewRecorder()
	testHandler.StartProjectWorkflowExecutionFromChat(w, withURLParam(newRequest(http.MethodPost, "/api/projects/"+project.ID+"/workflow/executions/start", map[string]any{
		"source_chat_session_id": sessionID,
		"snapshot": map[string]any{
			"goal":                "Keep one task through the workflow",
			"scope":               []string{"server"},
			"acceptance_criteria": []string{"one primary issue"},
			"risks":               []string{},
		},
	}), "id", project.ID))
	if w.Code != http.StatusCreated {
		t.Fatalf("start execution: %d %s", w.Code, w.Body.String())
	}
	var started struct {
		Execution ProjectExecutionResponse `json:"execution"`
		Issue     IssueResponse            `json:"issue"`
	}
	if err := json.NewDecoder(w.Body).Decode(&started); err != nil {
		t.Fatal(err)
	}

	// A plain status update cannot replace structured delivery evidence.
	w = httptest.NewRecorder()
	testHandler.UpdateIssue(w, withURLParam(newRequest(http.MethodPatch, "/api/issues/"+started.Issue.ID, map[string]any{
		"status": "in_review",
	}), "id", started.Issue.ID))
	if w.Code != http.StatusConflict {
		t.Fatalf("status without delivery evidence: %d %s", w.Code, w.Body.String())
	}

	submitDelivery := func(commit string, expectMikaTask bool) ProjectExecutionResponse {
		t.Helper()
		w = httptest.NewRecorder()
		testHandler.SubmitProjectExecutionDelivery(w, withURLParam(newRequest(http.MethodPost, "/api/projects/"+project.ID+"/workflow/executions/delivery", map[string]any{
			"execution_id": started.Execution.ID,
			"issue_id":     started.Issue.ID,
			"commit":       commit,
			"tests":        "go test ./...",
			"summary":      "Updated the primary issue",
		}), "id", project.ID))
		if w.Code != http.StatusOK {
			t.Fatalf("submit delivery: %d %s", w.Code, w.Body.String())
		}
		var response struct {
			Execution ProjectExecutionResponse `json:"execution"`
			MainIssue IssueResponse            `json:"main_issue"`
			TaskID    *string                  `json:"task_id"`
		}
		if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
			t.Fatal(err)
		}
		if response.MainIssue.ID != started.Issue.ID {
			t.Fatalf("review handoff main issue = %q, want %q", response.MainIssue.ID, started.Issue.ID)
		}
		if expectMikaTask && (response.TaskID == nil || *response.TaskID == "") {
			t.Fatal("review handoff did not queue a Mika sub-run")
		}
		return response.Execution
	}
	dispatch := func(role, agentID string) {
		t.Helper()
		w = httptest.NewRecorder()
		testHandler.DispatchProjectExecution(w, withURLParam(newRequest(http.MethodPost, "/api/projects/"+project.ID+"/workflow/executions/dispatch", map[string]any{
			"execution_id": started.Execution.ID,
			"agent_id":     agentID,
			"role":         role,
			"title":        "Ignored phase title",
		}), "id", project.ID))
		if w.Code != http.StatusOK {
			t.Fatalf("dispatch %s: %d %s", role, w.Code, w.Body.String())
		}
		var response struct {
			Issue  IssueResponse `json:"issue"`
			TaskID *string       `json:"task_id"`
		}
		if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
			t.Fatal(err)
		}
		if response.Issue.ID != started.Issue.ID {
			t.Fatalf("%s sub-run issue = %q, want %q", role, response.Issue.ID, started.Issue.ID)
		}
	}
	recordReview := func(outcome string) ProjectExecutionResponse {
		t.Helper()
		w = httptest.NewRecorder()
		testHandler.RecordProjectReview(w, withURLParam(newRequest(http.MethodPost, "/api/projects/"+project.ID+"/workflow/executions/review", map[string]any{
			"execution_id": started.Execution.ID,
			"issue_id":     started.Issue.ID,
			"outcome":      outcome,
			"findings":     "Recorded on the primary issue",
		}), "id", project.ID))
		if w.Code != http.StatusOK {
			t.Fatalf("record review %s: %d %s", outcome, w.Code, w.Body.String())
		}
		var response struct {
			Execution ProjectExecutionResponse `json:"execution"`
			MainIssue IssueResponse            `json:"main_issue"`
			TaskID    *string                  `json:"task_id"`
		}
		if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
			t.Fatal(err)
		}
		if response.MainIssue.ID != started.Issue.ID {
			t.Fatalf("review result main issue = %q, want %q", response.MainIssue.ID, started.Issue.ID)
		}
		return response.Execution
	}
	assertSingleLink := func() {
		t.Helper()
		var linkCount int
		if err := testPool.QueryRow(context.Background(), `
			SELECT count(*) FROM project_execution_issue WHERE execution_record_id = $1
		`, started.Execution.ID).Scan(&linkCount); err != nil || linkCount != 1 {
			t.Fatalf("execution link count = %d, err = %v, want 1", linkCount, err)
		}
	}

	if execution := submitDelivery("abc123", true); execution.State != "review" {
		t.Fatalf("state after delivery = %q, want review", execution.State)
	}
	assertSingleLink()

	// Delivery evidence is not review evidence: a manual status update cannot
	// bypass the review result that is now required for the primary issue.
	w = httptest.NewRecorder()
	testHandler.UpdateIssue(w, withURLParam(newRequest(http.MethodPatch, "/api/issues/"+started.Issue.ID, map[string]any{
		"status": "in_review",
	}), "id", started.Issue.ID))
	if w.Code != http.StatusConflict {
		t.Fatalf("status without review evidence: %d %s", w.Code, w.Body.String())
	}

	dispatch("review", reviewerID)
	if execution := recordReview("needs_changes"); execution.State != "repair" || execution.ReviewRound != 1 {
		t.Fatalf("state after needs_changes = %q/%d, want repair/1", execution.State, execution.ReviewRound)
	}
	assertSingleLink()
	dispatch("repair", developerID)
	if execution := submitDelivery("def456", false); execution.State != "review" {
		t.Fatalf("state after repair delivery = %q, want review", execution.State)
	}
	dispatch("review", reviewerID)
	if execution := recordReview("passed"); execution.State != "delivering" {
		t.Fatalf("state after passed review = %q, want delivering", execution.State)
	}
	assertSingleLink()

	// A review pass does not replace delivery-completion evidence.
	w = httptest.NewRecorder()
	testHandler.UpdateIssue(w, withURLParam(newRequest(http.MethodPatch, "/api/issues/"+started.Issue.ID, map[string]any{
		"status": "done",
	}), "id", started.Issue.ID))
	if w.Code != http.StatusConflict {
		t.Fatalf("status without completion evidence: %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	testHandler.CompleteProjectDelivery(w, withURLParam(newRequest(http.MethodPost, "/api/projects/"+project.ID+"/workflow/executions/delivery/complete", map[string]any{
		"execution_id": started.Execution.ID,
		"evidence":     map[string]any{"result": "notified"},
	}), "id", project.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("complete delivery: %d %s", w.Code, w.Body.String())
	}
	assertIssueStatus(t, started.Issue.ID, "done")
	assertSingleLink()
}

func TestListProjectExecutionRecordsGroupsLegacyWorkflowIssues(t *testing.T) {
	w := httptest.NewRecorder()
	testHandler.CreateProject(w, newRequest(http.MethodPost, "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Legacy workflow list project",
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", w.Code, w.Body.String())
	}
	var project ProjectResponse
	if err := json.NewDecoder(w.Body).Decode(&project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		testHandler.DeleteProject(httptest.NewRecorder(), withURLParam(newRequest(http.MethodDelete, "/api/projects/"+project.ID, nil), "id", project.ID))
	})
	enableProjectWorkflowForTest(t, project.ID)

	prdID := createHandlerTestAgent(t, "legacy-list-prd", nil)
	w = httptest.NewRecorder()
	testHandler.CreateProjectAgentBinding(w, withURLParam(newRequest(http.MethodPost, "/api/projects/"+project.ID+"/workflow/agents", map[string]any{
		"agent_id": prdID,
		"role":     "prd",
	}), "id", project.ID))
	if w.Code != http.StatusCreated {
		t.Fatalf("bind prd: %d %s", w.Code, w.Body.String())
	}
	sessionID := createChatSessionWithProjectForTest(t, prdID, project.ID)
	w = httptest.NewRecorder()
	testHandler.ConfirmProjectExecution(w, withURLParam(newRequest(http.MethodPost, "/api/projects/"+project.ID+"/workflow/executions", map[string]any{
		"source_chat_session_id": sessionID,
		"source_agent_id":        prdID,
		"snapshot": map[string]any{
			"goal":                "Group historical workflow issues",
			"scope":               []string{"server"},
			"acceptance_criteria": []string{"one main result"},
			"risks":               []string{},
		},
	}), "id", project.ID))
	if w.Code != http.StatusCreated {
		t.Fatalf("confirm execution: %d %s", w.Code, w.Body.String())
	}
	var execution ProjectExecutionResponse
	if err := json.NewDecoder(w.Body).Decode(&execution); err != nil {
		t.Fatal(err)
	}

	createIssue := func(title, status string) db.Issue {
		t.Helper()
		result, err := testHandler.IssueService.Create(context.Background(), service.IssueCreateParams{
			WorkspaceID: parseUUID(project.WorkspaceID),
			Title:       title,
			Status:      status,
			Priority:    "none",
			CreatorType: "member",
			CreatorID:   parseUUID(testUserID),
			ProjectID:   parseUUID(project.ID),
		}, service.IssueCreateOpts{})
		if err != nil {
			t.Fatalf("create %s issue: %v", title, err)
		}
		return result.Issue
	}
	development := createIssue("Legacy development issue", "in_progress")
	review := createIssue("Legacy review issue", "todo")
	delivery := createIssue("Legacy delivery issue", "done")
	for _, link := range []struct {
		issue db.Issue
		role  string
	}{
		{development, "development"},
		{review, "review"},
		{delivery, "delivery"},
	} {
		if _, err := testHandler.Queries.LinkProjectExecutionIssue(context.Background(), db.LinkProjectExecutionIssueParams{
			ExecutionRecordID: parseUUID(execution.ID),
			WorkspaceID:       parseUUID(project.WorkspaceID),
			ProjectID:         parseUUID(project.ID),
			IssueID:           link.issue.ID,
			Role:              link.role,
		}); err != nil {
			t.Fatalf("link %s issue: %v", link.role, err)
		}
	}

	w = httptest.NewRecorder()
	testHandler.ListProjectExecutionRecords(w, withURLParam(newRequest(http.MethodGet, "/api/projects/"+project.ID+"/workflow/executions", nil), "id", project.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("list executions: %d %s", w.Code, w.Body.String())
	}
	var listed struct {
		Executions []struct {
			ID        string `json:"id"`
			MainIssue *struct {
				ID     string `json:"id"`
				Title  string `json:"title"`
				Status string `json:"status"`
			} `json:"main_issue"`
			HistoricalIssues []struct {
				ID string `json:"id"`
			} `json:"historical_issues"`
		} `json:"executions"`
	}
	if err := json.NewDecoder(w.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	var grouped *struct {
		ID        string `json:"id"`
		MainIssue *struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			Status string `json:"status"`
		} `json:"main_issue"`
		HistoricalIssues []struct {
			ID string `json:"id"`
		} `json:"historical_issues"`
	}
	for i := range listed.Executions {
		if listed.Executions[i].ID == execution.ID {
			grouped = &listed.Executions[i]
			break
		}
	}
	if grouped == nil || grouped.MainIssue == nil {
		t.Fatalf("missing grouped execution: %+v", listed.Executions)
	}
	if grouped.MainIssue.ID != uuidToString(development.ID) || grouped.MainIssue.Title != development.Title || grouped.MainIssue.Status != "in_progress" {
		t.Fatalf("legacy main issue = %+v, want active development issue", grouped.MainIssue)
	}
	if len(grouped.HistoricalIssues) != 2 {
		t.Fatalf("historical issues = %+v, want review and delivery", grouped.HistoricalIssues)
	}

	var unchangedStatus string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM issue WHERE id = $1`, review.ID).Scan(&unchangedStatus); err != nil || unchangedStatus != "todo" {
		t.Fatalf("legacy review status = %q, err = %v, want todo", unchangedStatus, err)
	}
}
