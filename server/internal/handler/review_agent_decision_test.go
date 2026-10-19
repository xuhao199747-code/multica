package handler

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func reviewDecisionIssue(t *testing.T) string {
	t.Helper()
	issue := dbfx.Issue(t, "Review decision target")
	t.Cleanup(func() { dbfx.Exec(t, `DELETE FROM review_decision WHERE issue_id=$1`, issue) })
	return issue
}

func reviewDecisionPullRequest(t *testing.T, issueID, headSha string) string {
	t.Helper()
	pr := dbfx.Insert(t, "github_pull_request", testutil.Cols{
		"workspace_id": testWorkspaceID, "installation_id": 1,
		"repo_owner": "multica", "repo_name": "multica", "pr_number": 20,
		"title": "Review decision PR", "state": "open",
		"html_url":      "https://github.com/multica-ai/multica/pull/20",
		"head_sha":      headSha,
		"pr_created_at": time.Now(), "pr_updated_at": time.Now(),
	})
	dbfx.Exec(t, `INSERT INTO issue_pull_request(issue_id,pull_request_id) VALUES($1,$2)`, issueID, pr)
	t.Cleanup(func() { dbfx.Exec(t, `DELETE FROM github_pull_request WHERE id=$1`, pr) })
	return pr
}

func TestReviewDecisionRoundTripAndSupersede(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	issue := reviewDecisionIssue(t)
	pr := reviewDecisionPullRequest(t, issue, "sha-initial")

	var created map[string]any
	testutil.Call(t, testHandler.CreateReviewDecision, withURLParam(newRequest(http.MethodPost, "/api/issues/"+issue+"/review-decisions", map[string]any{
		"pull_request_id": pr, "head_sha": "sha-initial", "decision": "changes_requested", "note": "fix the tests",
	}), "id", issue)).Want(http.StatusCreated).JSON(&created)
	if created["decision"] != "changes_requested" || created["stale"] != false {
		t.Fatalf("created decision = %#v", created)
	}

	// New commits moved the PR head; the new decision pins the current head
	// and the old one goes stale instead of silently staying "current".
	dbfx.Exec(t, `UPDATE github_pull_request SET head_sha='sha-next', pr_updated_at=now() WHERE id=$1`, pr)
	var next map[string]any
	testutil.Call(t, testHandler.CreateReviewDecision, withURLParam(newRequest(http.MethodPost, "/api/issues/"+issue+"/review-decisions", map[string]any{
		"pull_request_id": pr, "head_sha": "sha-next", "decision": "approved",
	}), "id", issue)).Want(http.StatusCreated).JSON(&next)
	if next["stale"] != false {
		t.Fatalf("fresh decision marked stale: %#v", next)
	}

	var listed map[string]any
	testutil.Call(t, testHandler.ListReviewDecisions, withURLParam(newRequest(http.MethodGet, "/api/issues/"+issue+"/review-decisions", nil), "id", issue)).Want(http.StatusOK).JSON(&listed)
	decisions := listed["decisions"].([]any)
	if len(decisions) != 2 {
		t.Fatalf("listed %d decisions", len(decisions))
	}
	byHead := map[string]map[string]any{}
	for _, raw := range decisions {
		d := raw.(map[string]any)
		byHead[d["head_sha"].(string)] = d
	}
	if byHead["sha-initial"]["superseded_at"] == nil {
		t.Fatalf("old decision not superseded: %#v", byHead["sha-initial"])
	}
	if byHead["sha-initial"]["stale"] != true {
		t.Fatalf("old head decision not stale: %#v", byHead["sha-initial"])
	}
	if byHead["sha-next"]["superseded_at"] != nil {
		t.Fatalf("current decision superseded: %#v", byHead["sha-next"])
	}

	// Resolving the current decision settles it; a second resolve is a 404.
	testutil.Call(t, testHandler.ResolveReviewDecision, withURLParams(newRequest(http.MethodPost, "/api/issues/"+issue+"/review-decisions/"+next["id"].(string)+"/resolve", nil), "id", issue, "decisionID", next["id"].(string))).Want(http.StatusOK)
	testutil.Call(t, testHandler.ResolveReviewDecision, withURLParams(newRequest(http.MethodPost, "/api/issues/"+issue+"/review-decisions/"+next["id"].(string)+"/resolve", nil), "id", issue, "decisionID", next["id"].(string))).Want(http.StatusNotFound)
}

func TestReviewDecisionValidationAndIsolation(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	issue := reviewDecisionIssue(t)
	pr := reviewDecisionPullRequest(t, issue, "sha-v")

	testutil.Call(t, testHandler.CreateReviewDecision, withURLParam(newRequest(http.MethodPost, "/api/issues/"+issue+"/review-decisions", map[string]any{
		"pull_request_id": pr, "head_sha": "sha-v", "decision": "lgtm",
	}), "id", issue)).Want(http.StatusBadRequest)
	testutil.Call(t, testHandler.CreateReviewDecision, withURLParam(newRequest(http.MethodPost, "/api/issues/"+issue+"/review-decisions", map[string]any{
		"pull_request_id": pr, "decision": "approved",
	}), "id", issue)).Want(http.StatusBadRequest)
	foreignPR := dbfx.Insert(t, "github_pull_request", testutil.Cols{
		"workspace_id": testWorkspaceID, "installation_id": 1,
		"repo_owner": "multica", "repo_name": "other", "pr_number": 21,
		"title": "Unlinked PR", "state": "open",
		"html_url":      "https://github.com/multica-ai/other/pull/21",
		"head_sha":      "sha-x",
		"pr_created_at": time.Now(), "pr_updated_at": time.Now(),
	})
	t.Cleanup(func() { dbfx.Exec(t, `DELETE FROM github_pull_request WHERE id=$1`, foreignPR) })
	testutil.Call(t, testHandler.CreateReviewDecision, withURLParam(newRequest(http.MethodPost, "/api/issues/"+issue+"/review-decisions", map[string]any{
		"pull_request_id": foreignPR, "head_sha": "sha-x", "decision": "approved",
	}), "id", issue)).Want(http.StatusBadRequest)

	// A workspace the actor cannot see yields 404, not a leak.
	foreignWorkspace := dbfx.Workspace(t, "Foreign decision workspace", fmt.Sprintf("foreign-decision-%d", time.Now().UnixNano()))
	foreignIssue := dbfx.Issue(t, "Foreign issue", testutil.Cols{"workspace_id": foreignWorkspace})
	testutil.Call(t, testHandler.ListReviewDecisions, withURLParam(newRequest(http.MethodGet, "/api/issues/"+foreignIssue+"/review-decisions", nil), "id", foreignIssue)).Want(http.StatusNotFound)
}

func TestReviewDecisionDeferredStaysCurrentUntilSuperseded(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	issue := reviewDecisionIssue(t)
	var deferral map[string]any
	testutil.Call(t, testHandler.CreateReviewDecision, withURLParam(newRequest(http.MethodPost, "/api/issues/"+issue+"/review-decisions", map[string]any{
		"head_sha": "no-pr-head", "decision": "deferred", "note": "waiting for CI evidence",
	}), "id", issue)).Want(http.StatusCreated).JSON(&deferral)
	if deferral["pull_request_id"] != nil {
		t.Fatalf("decision without a PR recorded one: %#v", deferral)
	}
	if deferral["resolved_at"] != nil || deferral["superseded_at"] != nil {
		t.Fatalf("deferred decision settled immediately: %#v", deferral)
	}
}

func TestReviewDecisionKeepsOtherPullRequestDecision(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	issue := reviewDecisionIssue(t)
	pr := reviewDecisionPullRequest(t, issue, "sha-first")
	other := dbfx.Insert(t, "github_pull_request", testutil.Cols{
		"workspace_id": testWorkspaceID, "installation_id": 1,
		"repo_owner": "multica", "repo_name": "other", "pr_number": 21,
		"title": "Other PR", "state": "open", "html_url": "https://github.com/multica-ai/other/pull/21",
		"head_sha": "sha-other", "pr_created_at": time.Now(), "pr_updated_at": time.Now(),
	})
	dbfx.Exec(t, `INSERT INTO issue_pull_request(issue_id,pull_request_id) VALUES($1,$2)`, issue, other)
	t.Cleanup(func() { dbfx.Exec(t, `DELETE FROM github_pull_request WHERE id=$1`, other) })
	for _, item := range []struct{ id, sha string }{{pr, "sha-first"}, {other, "sha-other"}} {
		testutil.Call(t, testHandler.CreateReviewDecision, withURLParam(newRequest(http.MethodPost, "/api/issues/"+issue+"/review-decisions", map[string]any{
			"pull_request_id": item.id, "head_sha": item.sha, "decision": "approved",
		}), "id", issue)).Want(http.StatusCreated)
	}
	var listed map[string]any
	testutil.Call(t, testHandler.ListReviewDecisions, withURLParam(newRequest(http.MethodGet, "/api/issues/"+issue+"/review-decisions", nil), "id", issue)).Want(http.StatusOK).JSON(&listed)
	for _, raw := range listed["decisions"].([]any) {
		decision := raw.(map[string]any)
		if decision["superseded_at"] != nil {
			t.Fatalf("decision for a different PR was superseded: %#v", decision)
		}
	}
}
