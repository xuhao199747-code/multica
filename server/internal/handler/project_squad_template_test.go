package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"context"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"

	"github.com/multica-ai/multica/server/internal/testutil"
)

// squadTemplateFixture builds a squad whose agent members carry standard
// roles, plus a project referencing it.
func squadTemplateFixture(t *testing.T, name string, members map[string]string) (projectID, squadID string, agentIDs map[string]string) {
	t.Helper()
	leaderID := createHandlerTestAgent(t, name+"-leader", nil)
	squadID = dbfx.Squad(t, name, leaderID)
	agentIDs = map[string]string{}
	for agentName, standardRole := range members {
		agentID := createHandlerTestAgent(t, name+"-"+agentName, nil)
		cols := testutil.Cols{}
		if standardRole != "" {
			cols["standard_role"] = standardRole
		}
		dbfx.SquadMember(t, squadID, "agent", agentID, cols)
		agentIDs[agentName] = agentID
	}
	projectID = dbfx.Project(t, name+" project")
	t.Cleanup(func() {
		dbfx.Exec(t, `DELETE FROM project_agent_binding WHERE project_id = $1`, projectID)
		dbfx.Exec(t, `DELETE FROM project WHERE id = $1`, projectID)
		dbfx.Exec(t, `DELETE FROM squad_member WHERE squad_id = $1`, squadID)
		dbfx.Exec(t, `DELETE FROM squad WHERE id = $1`, squadID)
	})
	return projectID, squadID, agentIDs
}

func withProjectParam(t *testing.T, req *http.Request, projectID string) *http.Request {
	t.Helper()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", projectID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func setCollabSquad(t *testing.T, projectID, squadID string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	body := map[string]any{"squad_id": squadID}
	testHandler.SetProjectCollaborationSquad(w, withProjectParam(t, newRequest("PUT", "/api/projects/"+projectID+"/collaboration-squad", body), projectID))
	return w
}

func syncSquad(t *testing.T, projectID, diffHash string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	testHandler.SyncProjectSquad(w, withProjectParam(t, newRequest("POST", "/api/projects/"+projectID+"/sync-squad", map[string]any{"diff_hash": diffHash}), projectID))
	return w
}

func getRoleSource(t *testing.T, projectID string) map[string]any {
	w := httptest.NewRecorder()
	testHandler.GetProjectRoleSource(w, withProjectParam(t, newRequest("GET", "/api/projects/"+projectID+"/role-source", nil), projectID))
	if w.Code != http.StatusOK {
		t.Fatalf("GetProjectRoleSource: %d %s", w.Code, w.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode role source: %v", err)
	}
	return out
}

func diffHashOf(t *testing.T, roleSource map[string]any) string {
	t.Helper()
	diff, ok := roleSource["diff"].(map[string]any)
	if !ok {
		t.Fatalf("diff missing in role source: %v", roleSource)
	}
	hash, _ := diff["diff_hash"].(string)
	return hash
}

func bindingSources(t *testing.T, projectID string) map[string]string {
	t.Helper()
	w := httptest.NewRecorder()
	testHandler.ListProjectAgentBindings(w, withProjectParam(t, newRequest("GET", "/api/projects/"+projectID+"/workflow/agents", nil), projectID))
	if w.Code != http.StatusOK {
		t.Fatalf("ListProjectAgentBindings: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Bindings []ProjectAgentBindingResponse `json:"bindings"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode bindings: %v", err)
	}
	sources := map[string]string{}
	for _, b := range out.Bindings {
		sources[b.Role] = b.Source
	}
	return sources
}

func TestSetCollaborationSquadRejectsIncompleteMapping(t *testing.T) {
	projectID, squadID, _ := squadTemplateFixture(t, "incomplete-map", map[string]string{
		"prd": "prd", // developer + reviewer missing
	})
	w := setCollabSquad(t, projectID, squadID)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "developer") || !strings.Contains(w.Body.String(), "reviewer") {
		t.Errorf("conflict should name missing roles, got %s", w.Body.String())
	}
	// The project must not be linked to the squad.
	if rs := getRoleSource(t, projectID); rs["squad_id"] != nil {
		t.Errorf("squad_id should stay nil, got %v", rs["squad_id"])
	}
}

func TestSquadSyncWritesSquadSourceBindingsAndMikaUntouched(t *testing.T) {
	projectID, squadID, agents := squadTemplateFixture(t, "full-map", map[string]string{
		"prd": "prd", "dev": "developer", "rev": "reviewer",
	})
	mikaID := createHandlerTestAgent(t, "full-map-mika-local", nil)
	w := httptest.NewRecorder()
	testHandler.CreateProjectAgentBinding(w, withURLParam(newRequest("POST", "/", map[string]any{"agent_id": mikaID, "role": "mika"}), "id", projectID))
	if w.Code != http.StatusCreated {
		t.Fatalf("create mika binding: %d %s", w.Code, w.Body.String())
	}

	w = setCollabSquad(t, projectID, squadID)
	if w.Code != http.StatusOK {
		t.Fatalf("set collaboration squad: %d %s", w.Code, w.Body.String())
	}
	hash := diffHashOf(t, getRoleSource(t, projectID))

	w = syncSquad(t, projectID, hash)
	if w.Code != http.StatusOK {
		t.Fatalf("sync squad: %d %s", w.Code, w.Body.String())
	}
	sources := bindingSources(t, projectID)
	for _, role := range []string{"prd", "developer", "reviewer"} {
		if sources[role] != "squad" {
			t.Errorf("role %s source = %q, want squad", role, sources[role])
		}
	}
	if sources["mika"] != "manual" {
		t.Errorf("mika source = %q, want manual (untouched)", sources["mika"])
	}
	// Verify the actual agents were written from the squad mapping.
	var out struct {
		Bindings []ProjectAgentBindingResponse `json:"bindings"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode sync response: %v", err)
	}
	byRole := map[string]string{}
	for _, b := range out.Bindings {
		byRole[b.Role] = b.AgentID
	}
	if byRole["prd"] != agents["prd"] || byRole["developer"] != agents["dev"] || byRole["reviewer"] != agents["rev"] {
		t.Errorf("synced agents mismatch: %v (want prd=%s developer=%s reviewer=%s)", byRole, agents["prd"], agents["dev"], agents["rev"])
	}
}

func TestSyncRejectsStaleDiffHash(t *testing.T) {
	projectID, squadID, _ := squadTemplateFixture(t, "stale-hash", map[string]string{
		"prd": "prd", "dev": "developer", "rev": "reviewer",
	})
	if w := setCollabSquad(t, projectID, squadID); w.Code != http.StatusOK {
		t.Fatalf("set collaboration squad: %d %s", w.Code, w.Body.String())
	}
	hash := diffHashOf(t, getRoleSource(t, projectID))
	// A local manual binding moves the bindings half of the hash.
	local := createHandlerTestAgent(t, "stale-hash-local", nil)
	w := httptest.NewRecorder()
	testHandler.CreateProjectAgentBinding(w, withURLParam(newRequest("POST", "/", map[string]any{"agent_id": local, "role": "developer"}), "id", projectID))
	if w.Code != http.StatusCreated && w.Code != http.StatusConflict {
		t.Fatalf("create local binding: %d %s", w.Code, w.Body.String())
	}
	w = syncSquad(t, projectID, hash)
	if w.Code != http.StatusConflict {
		t.Fatalf("stale sync: expected 409, got %d: %s", w.Code, w.Body.String())
	}
	// Re-preview and sync with the fresh hash succeeds.
	fresh := diffHashOf(t, getRoleSource(t, projectID))
	if w := syncSquad(t, projectID, fresh); w.Code != http.StatusOK {
		t.Fatalf("fresh sync: %d %s", w.Code, w.Body.String())
	}
}

func TestSyncRejectsSquadChangeAfterPreview(t *testing.T) {
	projectID, squadID, agents := squadTemplateFixture(t, "squad-moved", map[string]string{
		"prd": "prd", "dev": "developer", "rev": "reviewer",
	})
	if w := setCollabSquad(t, projectID, squadID); w.Code != http.StatusOK {
		t.Fatalf("set collaboration squad: %d %s", w.Code, w.Body.String())
	}
	hash := diffHashOf(t, getRoleSource(t, projectID))
	// Swap the squad's developer after the preview: the mapping half changes.
	replacement := createHandlerTestAgent(t, "squad-moved-dev2", nil)
	dbfx.Exec(t, `UPDATE squad_member SET standard_role = NULL WHERE squad_id = $1 AND standard_role = 'developer'`, squadID)
	dbfx.SquadMember(t, squadID, "agent", replacement, testutil.Cols{"standard_role": "developer"})
	if w := syncSquad(t, projectID, hash); w.Code != http.StatusConflict {
		t.Fatalf("sync after squad change: expected 409, got %d: %s", w.Code, w.Body.String())
	}
	_ = agents
}

func TestLocalRebindFlipsSourceToManual(t *testing.T) {
	projectID, squadID, agents := squadTemplateFixture(t, "flip-manual", map[string]string{
		"prd": "prd", "dev": "developer", "rev": "reviewer",
	})
	if w := setCollabSquad(t, projectID, squadID); w.Code != http.StatusOK {
		t.Fatalf("set collaboration squad: %d %s", w.Code, w.Body.String())
	}
	if w := syncSquad(t, projectID, diffHashOf(t, getRoleSource(t, projectID))); w.Code != http.StatusOK {
		t.Fatalf("sync: %d %s", w.Code, w.Body.String())
	}
	// Local rebind of the developer role (delete squad row, create manual row).
	localDev := createHandlerTestAgent(t, "flip-manual-local-dev", nil)
	w := httptest.NewRecorder()
	testHandler.ListProjectAgentBindings(w, withURLParam(newRequest("GET", "/", nil), "id", projectID))
	var list struct {
		Bindings []ProjectAgentBindingResponse `json:"bindings"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	for _, b := range list.Bindings {
		if b.Role == "developer" && b.AgentID == agents["dev"] {
			del := httptest.NewRecorder()
			req := newRequest("DELETE", "/api/projects/"+projectID+"/workflow/agents/"+b.ID, nil)
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("id", projectID)
			rctx.URLParams.Add("bindingId", b.ID)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
			testHandler.DeleteProjectAgentBinding(del, req)
			if del.Code != http.StatusNoContent {
				t.Fatalf("delete old developer binding: %d %s", del.Code, del.Body.String())
			}
		}
	}
	w = httptest.NewRecorder()
	testHandler.CreateProjectAgentBinding(w, withURLParam(newRequest("POST", "/", map[string]any{"agent_id": localDev, "role": "developer"}), "id", projectID))
	if w.Code != http.StatusCreated {
		t.Fatalf("create local developer binding: %d %s", w.Code, w.Body.String())
	}
	sources := bindingSources(t, projectID)
	if sources["developer"] != "manual" {
		t.Errorf("developer source = %q, want manual after local rebind", sources["developer"])
	}
	if sources["prd"] != "squad" || sources["reviewer"] != "squad" {
		t.Errorf("other squad sources should be untouched: %v", sources)
	}
	// The squad definition must not have changed.
	count := dbfx.Count(t, `SELECT count(*) FROM squad_member WHERE squad_id = $1 AND standard_role = 'developer' AND member_id = $2`, squadID, agents["dev"])
	if count != 1 {
		t.Errorf("squad developer mapping changed: count=%d", count)
	}
}

func TestRestoreSquadConfigClearsOverridesAndKeepsMika(t *testing.T) {
	projectID, squadID, agents := squadTemplateFixture(t, "restore", map[string]string{
		"prd": "prd", "dev": "developer", "rev": "reviewer",
	})
	mikaID := createHandlerTestAgent(t, "restore-mika", nil)
	w := httptest.NewRecorder()
	testHandler.CreateProjectAgentBinding(w, withURLParam(newRequest("POST", "/", map[string]any{"agent_id": mikaID, "role": "mika"}), "id", projectID))
	if w.Code != http.StatusCreated {
		t.Fatalf("mika binding: %d %s", w.Code, w.Body.String())
	}
	if w := setCollabSquad(t, projectID, squadID); w.Code != http.StatusOK {
		t.Fatalf("set collaboration squad: %d %s", w.Code, w.Body.String())
	}
	if w := syncSquad(t, projectID, diffHashOf(t, getRoleSource(t, projectID))); w.Code != http.StatusOK {
		t.Fatalf("sync: %d %s", w.Code, w.Body.String())
	}
	// Local override on developer.
	localDev := createHandlerTestAgent(t, "restore-local-dev", nil)
	w = httptest.NewRecorder()
	testHandler.CreateProjectAgentBinding(w, withURLParam(newRequest("POST", "/", map[string]any{"agent_id": localDev, "role": "developer"}), "id", projectID))
	if w.Code != http.StatusCreated && w.Code != http.StatusConflict {
		t.Fatalf("override developer: %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	testHandler.RestoreProjectSquadConfig(w, withURLParam(newRequest("POST", "/api/projects/"+projectID+"/restore-squad-config", nil), "id", projectID))
	if w.Code != http.StatusOK {
		t.Fatalf("restore: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Bindings []ProjectAgentBindingResponse `json:"bindings"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Bindings) != 4 {
		t.Fatalf("expected 4 bindings after restore (3 squad + mika), got %d", len(out.Bindings))
	}
	for _, b := range out.Bindings {
		switch b.Role {
		case "mika":
			if b.AgentID != mikaID {
				t.Errorf("mika binding changed: %s", b.AgentID)
			}
			if b.Source != "manual" {
				t.Errorf("mika source = %q, want manual", b.Source)
			}
		default:
			if b.Source != "squad" {
				t.Errorf("role %s source = %q, want squad", b.Role, b.Source)
			}
		}
	}
	_ = agents
}

func TestArchivedSquadBlocksSyncAndRestore(t *testing.T) {
	projectID, squadID, _ := squadTemplateFixture(t, "archived", map[string]string{
		"prd": "prd", "dev": "developer", "rev": "reviewer",
	})
	if w := setCollabSquad(t, projectID, squadID); w.Code != http.StatusOK {
		t.Fatalf("set collaboration squad: %d %s", w.Code, w.Body.String())
	}
	if w := syncSquad(t, projectID, diffHashOf(t, getRoleSource(t, projectID))); w.Code != http.StatusOK {
		t.Fatalf("sync: %d %s", w.Code, w.Body.String())
	}
	dbfx.Exec(t, `UPDATE squad SET archived_at = now() WHERE id = $1`, squadID)

	// Role source keeps working and flags the archived squad.
	rs := getRoleSource(t, projectID)
	if rs["squad_archived"] != true {
		t.Errorf("squad_archived = %v, want true", rs["squad_archived"])
	}
	if w := syncSquad(t, projectID, diffHashOf(t, rs)); w.Code != http.StatusConflict {
		t.Errorf("sync with archived squad: expected 409, got %d", w.Code)
	}
	w := httptest.NewRecorder()
	testHandler.RestoreProjectSquadConfig(w, withURLParam(newRequest("POST", "/", nil), "id", projectID))
	if w.Code != http.StatusConflict {
		t.Errorf("restore with archived squad: expected 409, got %d", w.Code)
	}
	// Bindings keep working (effective configuration unaffected).
	if len(bindingSources(t, projectID)) != 3 {
		t.Errorf("bindings should survive squad archival")
	}
}

func TestSetCollaborationSquadRejectsArchivedSquad(t *testing.T) {
	projectID, squadID, _ := squadTemplateFixture(t, "archived-pick", map[string]string{
		"prd": "prd", "dev": "developer", "rev": "reviewer",
	})
	dbfx.Exec(t, `UPDATE squad SET archived_at = now() WHERE id = $1`, squadID)
	if w := setCollabSquad(t, projectID, squadID); w.Code != http.StatusConflict {
		t.Fatalf("archived squad: expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSquadTemplateWriteRequiresBindingPermission(t *testing.T) {
	projectID, squadID, _ := squadTemplateFixture(t, "perm-gate", map[string]string{
		"prd": "prd", "dev": "developer", "rev": "reviewer",
	})
	// An agent caller with no project binding gets 403.
	agentID := createHandlerTestAgent(t, "perm-gate-agent", nil)
	taskID := dbfx.Task(t, agentID, testutil.Cols{"status": "completed", "completed_at": testutil.Raw("now()")})
	req := withProjectParam(t, newRequest("PUT", "/api/projects/"+projectID+"/collaboration-squad", map[string]any{"squad_id": squadID}), projectID)
	req.Header.Set("X-User-ID", agentID)
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Task-ID", taskID)
	w := httptest.NewRecorder()
	testHandler.SetProjectCollaborationSquad(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("agent without binding: expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUnbindSquadKeepsBindings(t *testing.T) {
	projectID, squadID, _ := squadTemplateFixture(t, "unbind", map[string]string{
		"prd": "prd", "dev": "developer", "rev": "reviewer",
	})
	if w := setCollabSquad(t, projectID, squadID); w.Code != http.StatusOK {
		t.Fatalf("set: %d %s", w.Code, w.Body.String())
	}
	if w := syncSquad(t, projectID, diffHashOf(t, getRoleSource(t, projectID))); w.Code != http.StatusOK {
		t.Fatalf("sync: %d %s", w.Code, w.Body.String())
	}
	w := httptest.NewRecorder()
	testHandler.SetProjectCollaborationSquad(w, withURLParam(newRequest("PUT", "/", map[string]any{"squad_id": nil}), "id", projectID))
	if w.Code != http.StatusOK {
		t.Fatalf("unbind: %d %s", w.Code, w.Body.String())
	}
	if rs := getRoleSource(t, projectID); rs["squad_id"] != nil {
		t.Errorf("squad should be cleared, got %v", rs["squad_id"])
	}
	if len(bindingSources(t, projectID)) != 3 {
		t.Errorf("bindings should survive unbind")
	}
}

func testMemberForSquadTemplateTests(t *testing.T) db.Member {
	t.Helper()
	var m db.Member
	err := testPool.QueryRow(context.Background(), `SELECT id, user_id, workspace_id, role FROM member WHERE workspace_id = $1 AND user_id = $2 LIMIT 1`, testWorkspaceID, testUserID).Scan(&m.ID, &m.UserID, &m.WorkspaceID, &m.Role)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestUpdateSquadMemberStandardRoleEnforcesUniqueness(t *testing.T) {
	leaderID := createHandlerTestAgent(t, "std-role-leader", nil)
	squadID := dbfx.Squad(t, "std-role-squad", leaderID)
	agentA := createHandlerTestAgent(t, "std-role-a", nil)
	agentB := createHandlerTestAgent(t, "std-role-b", nil)
	dbfx.SquadMember(t, squadID, "agent", agentA, testutil.Cols{"standard_role": "prd"})
	dbfx.SquadMember(t, squadID, "agent", agentB)
	t.Cleanup(func() {
		dbfx.Exec(t, `DELETE FROM squad_member WHERE squad_id = $1`, squadID)
		dbfx.Exec(t, `DELETE FROM squad WHERE id = $1`, squadID)
	})

	setStandard := func(memberID, role string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := withURLParam(newRequest("PATCH", "/api/squads/"+squadID+"/members/role?workspace_id="+testWorkspaceID, map[string]any{
			"member_type":   "agent",
			"member_id":     memberID,
			"standard_role": role,
		}), "id", squadID)
		req = req.WithContext(middleware.SetMemberContext(req.Context(), testWorkspaceID, testMemberForSquadTemplateTests(t)))
		testHandler.UpdateSquadMemberRole(w, req)
		return w
	}
	// Assign the prd role agentB already holds: conflict.
	if w := setStandard(agentB, "prd"); w.Code != http.StatusConflict {
		t.Fatalf("duplicate standard role: expected 409, got %d: %s", w.Code, w.Body.String())
	}
	// A free role works and round-trips.
	assigned := setStandard(agentB, "reviewer")
	if assigned.Code != http.StatusOK {
		t.Fatalf("assign reviewer: %d %s", assigned.Code, assigned.Body.String())
	}
	var member SquadMemberResponse
	if err := json.Unmarshal(assigned.Body.Bytes(), &member); err != nil {
		t.Fatal(err)
	}
	if member.StandardRole == nil || *member.StandardRole != "reviewer" {
		t.Errorf("standard_role = %v, want reviewer", member.StandardRole)
	}
	// Clearing works.
	if w := setStandard(agentB, ""); w.Code != http.StatusOK {
		t.Fatalf("clear standard role: %d %s", w.Code, w.Body.String())
	}
	// Invalid enum rejected.
	if w := setStandard(agentB, "mika"); w.Code != http.StatusBadRequest {
		t.Fatalf("mika standard role: expected 400, got %d", w.Code)
	}
}

func TestSquadTemplateSyncWithoutSquadConflicts(t *testing.T) {
	projectID := dbfx.Project(t, "no squad project "+fmt.Sprint(len("x")))
	t.Cleanup(func() { dbfx.Exec(t, `DELETE FROM project WHERE id = $1`, projectID) })
	if w := syncSquad(t, projectID, "abc"); w.Code != http.StatusConflict {
		t.Fatalf("sync without squad: expected 409, got %d", w.Code)
	}
	w := httptest.NewRecorder()
	testHandler.RestoreProjectSquadConfig(w, withURLParam(newRequest("POST", "/", nil), "id", projectID))
	if w.Code != http.StatusConflict {
		t.Fatalf("restore without squad: expected 409, got %d", w.Code)
	}
}
