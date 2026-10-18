package handler

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestReviewAgentConfigurationRoundTrip(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	config := map[string]any{
		"recipient_id": testUserID, "scope": "workspace", "instant_enabled": true,
		"summary_enabled": true, "summary_time": "09:30", "timezone": "Asia/Shanghai",
	}
	var created map[string]any
	testutil.Call(t, testHandler.CreateAgent, newRequest(http.MethodPost, "/api/agents", map[string]any{
		"name":       fmt.Sprintf("review-agent-%d", time.Now().UnixNano()),
		"runtime_id": handlerTestRuntimeID(t), "review_config": config,
	})).Want(http.StatusCreated).JSON(&created)
	id := created["id"].(string)
	dbfx.Cleanup(t, `DELETE FROM agent WHERE id = $1`, id)
	if created["review_config"] == nil {
		t.Fatal("create did not return review configuration")
	}
	var updated map[string]any
	testutil.Call(t, testHandler.UpdateAgent, withURLParam(newRequest(http.MethodPut, "/api/agents/"+id, map[string]any{
		"description": "preserve review config",
	}), "id", id)).Want(http.StatusOK).JSON(&updated)
	if updated["review_config"] == nil {
		t.Fatal("unrelated update lost review configuration")
	}
	config["summary_time"] = "18:15"
	testutil.Call(t, testHandler.UpdateAgent, withURLParam(newRequest(http.MethodPut, "/api/agents/"+id, map[string]any{
		"review_config": config,
	}), "id", id)).Want(http.StatusOK).JSON(&updated)
	if updated["review_config"].(map[string]any)["summary_time"] != "18:15" {
		t.Fatalf("updated configuration = %#v", updated["review_config"])
	}
	config["timezone"] = "not/a-zone"
	testutil.Call(t, testHandler.UpdateAgent, withURLParam(newRequest(http.MethodPut, "/api/agents/"+id, map[string]any{
		"review_config": config, "description": "must not persist",
	}), "id", id)).Want(http.StatusBadRequest)
	var unchanged map[string]any
	testutil.Call(t, testHandler.GetAgent, withURLParam(newRequest(http.MethodGet, "/api/agents/"+id, nil), "id", id)).Want(http.StatusOK).JSON(&unchanged)
	if unchanged["description"] != "preserve review config" || unchanged["review_config"].(map[string]any)["timezone"] != "Asia/Shanghai" {
		t.Fatalf("invalid update changed agent: %#v", unchanged)
	}
	var cleared map[string]any
	testutil.Call(t, testHandler.UpdateAgent, withURLParam(newRequest(http.MethodPut, "/api/agents/"+id, map[string]any{
		"review_config": nil,
	}), "id", id)).Want(http.StatusOK).JSON(&cleared)
	if cleared["review_config"] != nil {
		t.Fatalf("cleared configuration = %#v", cleared["review_config"])
	}
}

func TestReviewAgentRejectsForeignRecipient(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	foreignWorkspace := dbfx.Workspace(t, "Foreign review workspace", fmt.Sprintf("foreign-review-recipient-%d", time.Now().UnixNano()))
	foreignUser := dbfx.User(t, fmt.Sprintf("foreign-review-%d@example.test", time.Now().UnixNano()), "Foreign reviewer")
	dbfx.Member(t, foreignWorkspace, foreignUser, "owner")
	testutil.Call(t, testHandler.CreateAgent, newRequest(http.MethodPost, "/api/agents", map[string]any{
		"name": "invalid-review-recipient", "runtime_id": handlerTestRuntimeID(t),
		"review_config": map[string]any{"recipient_id": foreignUser, "scope": "workspace"},
	})).Want(http.StatusBadRequest)
}

func TestReviewAgentRejectsForeignProject(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	foreignWorkspace := dbfx.Workspace(t, "Foreign review workspace", fmt.Sprintf("foreign-review-project-%d", time.Now().UnixNano()))
	foreignProject := dbfx.Project(t, "Foreign review project", testutil.Cols{"workspace_id": foreignWorkspace})
	testutil.Call(t, testHandler.CreateAgent, newRequest(http.MethodPost, "/api/agents", map[string]any{
		"name": "invalid-review-project", "runtime_id": handlerTestRuntimeID(t),
		"review_config": map[string]any{"recipient_id": testUserID, "scope": "projects", "project_ids": []string{foreignProject}},
	})).Want(http.StatusBadRequest)
}
