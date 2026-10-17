package handler

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

// TestSpecIssueLinks covers link idempotency, listing, unlink no-ops, and
// workspace isolation of the link endpoints.
func TestSpecIssueLinks(t *testing.T) {
	specID := dbfx.Spec(t, "Spec link fixture")
	issueID := dbfx.Issue(t, "Spec link target")

	link := func(t *testing.T, body any) *testutil.Response {
		t.Helper()
		return testutil.Call(t, testHandler.LinkSpecIssue,
			testutil.WithURLParams(newRequest("POST", "/api/specs/"+specID+"/issues", body), "id", specID))
	}

	t.Run("link returns the issue ref", func(t *testing.T) {
		var out IssueRefResponse
		link(t, map[string]any{"issue_id": issueID}).
			Want(http.StatusCreated).JSON(&out)
		if out.ID != issueID {
			t.Fatalf("LinkSpecIssue: expected linked issue %s, got %+v", issueID, out)
		}
		if out.Identifier == "" || out.Status == "" {
			t.Fatalf("LinkSpecIssue: ref must carry identifier and status, got %+v", out)
		}
	})

	t.Run("re-link is idempotent", func(t *testing.T) {
		var out IssueRefResponse
		link(t, map[string]any{"issue_id": issueID}).
			Want(http.StatusCreated).JSON(&out)
		if got := dbfx.Count(t, `SELECT count(*) FROM spec_issue WHERE spec_id = $1 AND issue_id = $2`, specID, issueID); got != 1 {
			t.Fatalf("re-link must not duplicate the junction row, count = %d", got)
		}
	})

	t.Run("list shows the linked issue", func(t *testing.T) {
		var out struct {
			Issues []IssueRefResponse `json:"issues"`
			Total  int                `json:"total"`
		}
		testutil.Call(t, testHandler.ListSpecIssues,
			testutil.WithURLParams(newRequest("GET", "/api/specs/"+specID+"/issues", nil), "id", specID)).
			Want(http.StatusOK).JSON(&out)
		if out.Total != 1 || len(out.Issues) != 1 || out.Issues[0].ID != issueID {
			t.Fatalf("ListSpecIssues: expected exactly the linked issue, got %+v", out)
		}
	})

	t.Run("link by issue identifier resolves", func(t *testing.T) {
		otherIssue := dbfx.Issue(t, "Spec link identifier target")
		var prefix string
		var number int32
		dbfx.QueryRow(t, `SELECT w.issue_prefix, i.number FROM issue i JOIN workspace w ON w.id = i.workspace_id WHERE i.id = $1`, otherIssue).
			Scan(&prefix, &number)

		var out IssueRefResponse
		testutil.Call(t, testHandler.LinkSpecIssue,
			testutil.WithURLParams(newRequest("POST", "/api/specs/"+specID+"/issues", map[string]any{"issue_id": fmt.Sprintf("%s-%d", prefix, number)}), "id", specID)).
			Want(http.StatusCreated).JSON(&out)
		if out.ID != otherIssue {
			t.Fatalf("LinkSpecIssue by identifier: resolved to %s, want %s", out.ID, otherIssue)
		}
	})

	t.Run("missing issue_id is a 400", func(t *testing.T) {
		link(t, map[string]any{}).Want(http.StatusBadRequest)
	})

	t.Run("unknown issue is a 404", func(t *testing.T) {
		link(t, map[string]any{"issue_id": "00000000-0000-0000-0000-000000000000"}).
			Want(http.StatusNotFound)
	})

	t.Run("cross-workspace issue is a 404", func(t *testing.T) {
		otherWs := dbfx.Workspace(t, "spec-link-other-ws", "spec-link-other-ws")
		otherUser := dbfx.User(t, "spec link other user", "spec-link-other@example.com")
		dbfx.Member(t, otherWs, otherUser, "owner")
		otherIssue := dbfx.Insert(t, "issue", testutil.Cols{
			"workspace_id": otherWs,
			"title":        "foreign issue",
			"status":       "todo",
			"priority":     "none",
			"creator_type": "member",
			"creator_id":   otherUser,
			"position":     0,
			"number":       1,
		})
		link(t, map[string]any{"issue_id": otherIssue}).Want(http.StatusNotFound)
	})

	t.Run("unlink removes the link and unlinked unlink is a no-op", func(t *testing.T) {
		testutil.Call(t, testHandler.UnlinkSpecIssue,
			testutil.WithURLParams(newRequest("DELETE", "/api/specs/"+specID+"/issues/"+issueID, nil), "id", specID, "issueId", issueID)).
			Want(http.StatusNoContent)
		if got := dbfx.Count(t, `SELECT count(*) FROM spec_issue WHERE spec_id = $1 AND issue_id = $2`, specID, issueID); got != 0 {
			t.Fatalf("unlink must remove the junction row, count = %d", got)
		}
		testutil.Call(t, testHandler.UnlinkSpecIssue,
			testutil.WithURLParams(newRequest("DELETE", "/api/specs/"+specID+"/issues/"+issueID, nil), "id", specID, "issueId", issueID)).
			Want(http.StatusNoContent)
	})
}

// TestIssueResponsesIncludeSpecs covers the spec summaries folded into issue
// detail and list responses.
func TestIssueResponsesIncludeSpecs(t *testing.T) {
	specID := dbfx.Spec(t, "Issue specs fixture")
	var specIdentifier string
	dbfx.QueryRow(t, `SELECT identifier FROM spec WHERE id = $1`, specID).Scan(&specIdentifier)

	linked := dbfx.Issue(t, "Issue with spec link")
	unlinked := dbfx.Issue(t, "Issue without spec link")
	dbfx.Exec(t, `INSERT INTO spec_issue (spec_id, issue_id) VALUES ($1, $2)`, specID, linked)

	t.Run("detail carries the linked spec summary", func(t *testing.T) {
		var out IssueResponse
		testutil.Call(t, testHandler.GetIssue,
			testutil.WithURLParams(newRequest("GET", "/api/issues/"+linked, nil), "id", linked)).
			Want(http.StatusOK).JSON(&out)
		if out.Specs == nil {
			t.Fatalf("GetIssue: specs must be attached (non-nil), got null")
		}
		if len(*out.Specs) != 1 {
			t.Fatalf("GetIssue: expected 1 linked spec, got %+v", *out.Specs)
		}
		s := (*out.Specs)[0]
		if s.ID != specID || s.Identifier != specIdentifier || s.Title != "Issue specs fixture" || s.Status == "" {
			t.Fatalf("GetIssue: bad spec summary %+v", s)
		}
	})

	t.Run("detail of an unlinked issue emits an empty specs array", func(t *testing.T) {
		var out IssueResponse
		testutil.Call(t, testHandler.GetIssue,
			testutil.WithURLParams(newRequest("GET", "/api/issues/"+unlinked, nil), "id", unlinked)).
			Want(http.StatusOK).JSON(&out)
		if out.Specs == nil || len(*out.Specs) != 0 {
			t.Fatalf("GetIssue: unlinked issue must emit an empty non-nil specs array, got %+v", out.Specs)
		}
	})

	t.Run("workspace list folds specs per row", func(t *testing.T) {
		var out struct {
			Issues []IssueResponse `json:"issues"`
		}
		testutil.Call(t, testHandler.ListIssues,
			newRequest("GET", fmt.Sprintf("/api/issues?workspace_id=%s&limit=200", testWorkspaceID), nil)).
			Want(http.StatusOK).JSON(&out)
		byID := map[string]IssueResponse{}
		for _, issue := range out.Issues {
			byID[issue.ID] = issue
		}
		linkedRow, ok := byID[linked]
		if !ok {
			t.Fatalf("ListIssues: linked issue %s missing from list", linked)
		}
		if linkedRow.Specs == nil || len(*linkedRow.Specs) != 1 || (*linkedRow.Specs)[0].ID != specID {
			t.Fatalf("ListIssues: linked issue must carry the spec summary, got %+v", linkedRow.Specs)
		}
	})

	t.Run("children list folds specs per row", func(t *testing.T) {
		parentID := dbfx.Issue(t, "Parent of spec-linked child")
		dbfx.Exec(t, `UPDATE issue SET parent_issue_id = $1 WHERE id = $2`, parentID, linked)
		t.Cleanup(func() {
			dbfx.Exec(t, `UPDATE issue SET parent_issue_id = NULL WHERE id = $1`, linked)
		})

		var out struct {
			Issues []IssueResponse `json:"issues"`
		}
		testutil.Call(t, testHandler.ListChildIssues,
			testutil.WithURLParams(newRequest("GET", "/api/issues/"+parentID+"/children", nil), "id", parentID)).
			Want(http.StatusOK).JSON(&out)
		found := false
		for _, child := range out.Issues {
			if child.ID != linked {
				continue
			}
			found = true
			if child.Specs == nil || len(*child.Specs) != 1 || (*child.Specs)[0].ID != specID {
				t.Fatalf("ListChildIssues: linked child must carry the spec summary, got %+v", child.Specs)
			}
		}
		if !found {
			t.Fatalf("ListChildIssues: child %s missing", linked)
		}
	})
}

// TestDeleteIssueRemovesSpecLinks guards the application-level sweep: the
// spec_issue junction has no foreign key, so issue delete must clear it
// inside the same transaction.
func TestDeleteIssueRemovesSpecLinks(t *testing.T) {
	specID := dbfx.Spec(t, "Spec surviving issue delete")
	issueID := dbfx.Issue(t, "Issue deleted with a spec link")
	dbfx.Exec(t, `INSERT INTO spec_issue (spec_id, issue_id) VALUES ($1, $2)`, specID, issueID)

	testutil.Call(t, testHandler.DeleteIssue,
		testutil.WithURLParams(newRequest("DELETE", "/api/issues/"+issueID, nil), "id", issueID)).
		Want(http.StatusNoContent)

	if got := dbfx.Count(t, `SELECT count(*) FROM spec_issue WHERE issue_id = $1`, issueID); got != 0 {
		t.Fatalf("issue delete must sweep spec_issue, leftover rows = %d", got)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM spec WHERE id = $1`, specID); got != 1 {
		t.Fatalf("issue delete must not touch the spec itself")
	}
}
