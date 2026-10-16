package handler

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

type specListEnvelope struct {
	Specs []SpecSummaryResponse `json:"specs"`
	Total int                   `json:"total"`
}

func specCounterValue(t *testing.T) int32 {
	t.Helper()
	var counter int32
	dbfx.QueryRow(t, `SELECT spec_counter FROM workspace WHERE id = $1`, testWorkspaceID).Scan(&counter)
	return counter
}

// TestSpecList covers the list envelope: non-nil empty slices, created specs
// showing up, and the optional project_id filter.
func TestSpecList(t *testing.T) {
	t.Run("empty list is a non-nil slice", func(t *testing.T) {
		// A filter matching nothing keeps the result independent of specs
		// other tests left in the shared workspace.
		emptyProject := dbfx.Project(t, "spec-list-empty-filter-project")
		var out specListEnvelope
		testutil.Call(t, testHandler.ListSpecs,
			newRequest("GET", "/api/specs?project_id="+emptyProject, nil)).
			Want(http.StatusOK).JSON(&out)
		if out.Specs == nil {
			t.Fatalf("ListSpecs: specs must be non-nil, got null")
		}
		if out.Total != 0 || len(out.Specs) != 0 {
			t.Fatalf("ListSpecs: expected empty result, got %+v", out)
		}
	})

	t.Run("invalid project_id filter is a 400", func(t *testing.T) {
		testutil.Call(t, testHandler.ListSpecs,
			newRequest("GET", "/api/specs?project_id=not-a-uuid", nil)).
			Want(http.StatusBadRequest)
	})

	t.Run("created specs appear and project_id filters", func(t *testing.T) {
		projectID := dbfx.Project(t, "spec-list-project")

		var first SpecResponse
		testutil.Call(t, testHandler.CreateSpec,
			newRequest("POST", "/api/specs", map[string]any{"title": "Spec list A"})).
			Want(http.StatusCreated).JSON(&first)
		var second SpecResponse
		testutil.Call(t, testHandler.CreateSpec,
			newRequest("POST", "/api/specs", map[string]any{
				"title":      "Spec list B",
				"project_id": projectID,
			})).
			Want(http.StatusCreated).JSON(&second)

		var all specListEnvelope
		testutil.Call(t, testHandler.ListSpecs, newRequest("GET", "/api/specs", nil)).
			Want(http.StatusOK).JSON(&all)
		seen := map[string]bool{}
		for _, s := range all.Specs {
			seen[s.ID] = true
		}
		if !seen[first.ID] || !seen[second.ID] {
			t.Fatalf("ListSpecs: created specs missing from list: %+v", all)
		}

		var filtered specListEnvelope
		testutil.Call(t, testHandler.ListSpecs,
			newRequest("GET", "/api/specs?project_id="+projectID, nil)).
			Want(http.StatusOK).JSON(&filtered)
		if filtered.Total != 1 || len(filtered.Specs) != 1 || filtered.Specs[0].ID != second.ID {
			t.Fatalf("ListSpecs project_id filter: expected only %s, got %+v", second.ID, filtered)
		}
	})
}

// TestSpecGet covers identifier/UUID lookup, the embedded current_version,
// and cross-workspace isolation.
func TestSpecGet(t *testing.T) {
	specID := dbfx.Spec(t, "Spec get fixture")
	versionID := dbfx.SpecVersion(t, specID, 1, testutil.Cols{"content": "the current body"})
	dbfx.Exec(t, `UPDATE spec SET current_version_id = $1 WHERE id = $2`, versionID, specID)

	var identifier string
	dbfx.QueryRow(t, `SELECT identifier FROM spec WHERE id = $1`, specID).Scan(&identifier)

	t.Run("by UUID with current_version content", func(t *testing.T) {
		var out SpecResponse
		testutil.Call(t, testHandler.GetSpec,
			testutil.WithURLParams(newRequest("GET", "/api/specs/"+specID, nil), "id", specID)).
			Want(http.StatusOK).JSON(&out)
		if out.CurrentVersion == nil {
			t.Fatalf("GetSpec: expected current_version, got %+v", out)
		}
		if out.CurrentVersion.Content != "the current body" || out.CurrentVersion.Number != 1 {
			t.Fatalf("GetSpec: unexpected current_version: %+v", out.CurrentVersion)
		}
		if out.CurrentVersionID == nil || *out.CurrentVersionID != versionID {
			t.Fatalf("GetSpec: unexpected current_version_id: %+v", out.CurrentVersionID)
		}
	})

	t.Run("by identifier text", func(t *testing.T) {
		var out SpecResponse
		testutil.Call(t, testHandler.GetSpec,
			testutil.WithURLParams(newRequest("GET", "/api/specs/"+identifier, nil), "id", identifier)).
			Want(http.StatusOK).JSON(&out)
		if out.ID != specID {
			t.Fatalf("GetSpec by identifier: expected %s, got %s", specID, out.ID)
		}
	})

	t.Run("unknown id 404s", func(t *testing.T) {
		testutil.Call(t, testHandler.GetSpec,
			testutil.WithURLParams(newRequest("GET", "/api/specs/nope", nil), "id", "nope")).
			Want(http.StatusNotFound)
	})

	t.Run("other workspace's spec 404s", func(t *testing.T) {
		otherWorkspace := dbfx.Workspace(t, "Spec Other Workspace", "spec-other-ws")
		otherSpec := dbfx.Spec(t, "Other workspace spec", testutil.Cols{"workspace_id": otherWorkspace})

		// Same user, main test workspace header: the foreign spec must not
		// resolve across the workspace boundary.
		testutil.Call(t, testHandler.GetSpec,
			testutil.WithURLParams(newRequest("GET", "/api/specs/"+otherSpec, nil), "id", otherSpec)).
			Want(http.StatusNotFound)
	})
}

// TestSpecCreate covers identifier minting inside the create transaction and
// request validation.
func TestSpecCreate(t *testing.T) {
	before := specCounterValue(t)

	var first SpecResponse
	testutil.Call(t, testHandler.CreateSpec,
		newRequest("POST", "/api/specs", map[string]any{"title": "Spec create one"})).
		Want(http.StatusCreated).JSON(&first)

	var second SpecResponse
	testutil.Call(t, testHandler.CreateSpec,
		newRequest("POST", "/api/specs", map[string]any{"title": "Spec create two"})).
		Want(http.StatusCreated).JSON(&second)

	wantFirst := fmt.Sprintf("HAN-S-%d", before+1)
	wantSecond := fmt.Sprintf("HAN-S-%d", before+2)
	if first.Identifier != wantFirst {
		t.Fatalf("CreateSpec: expected identifier %s, got %s", wantFirst, first.Identifier)
	}
	if second.Identifier != wantSecond {
		t.Fatalf("CreateSpec: expected identifier %s, got %s", wantSecond, second.Identifier)
	}
	if first.Status != "draft" {
		t.Fatalf("CreateSpec: expected draft status, got %s", first.Status)
	}
	if first.CurrentVersion != nil {
		t.Fatalf("CreateSpec: expected null current_version, got %+v", first.CurrentVersion)
	}
	if specCounterValue(t) != before+2 {
		t.Fatalf("CreateSpec: workspace spec_counter did not advance by 2")
	}

	t.Run("empty or whitespace title is a 400", func(t *testing.T) {
		testutil.Call(t, testHandler.CreateSpec,
			newRequest("POST", "/api/specs", map[string]any{"title": ""})).
			Want(http.StatusBadRequest)
		testutil.Call(t, testHandler.CreateSpec,
			newRequest("POST", "/api/specs", map[string]any{"title": "   "})).
			Want(http.StatusBadRequest)
	})

	t.Run("malformed project_id is a 400", func(t *testing.T) {
		testutil.Call(t, testHandler.CreateSpec,
			newRequest("POST", "/api/specs", map[string]any{"title": "x", "project_id": "nope"})).
			Want(http.StatusBadRequest)
	})

	t.Run("unknown project_id is a 400", func(t *testing.T) {
		testutil.Call(t, testHandler.CreateSpec,
			newRequest("POST", "/api/specs", map[string]any{
				"title":      "x",
				"project_id": "00000000-0000-0000-0000-000000000000",
			})).
			Want(http.StatusBadRequest)
	})

	t.Run("project from another workspace is a 400", func(t *testing.T) {
		otherWorkspace := dbfx.Workspace(t, "Spec Create Other Workspace", "spec-create-other-ws")
		otherProject := dbfx.Project(t, "Other workspace project", testutil.Cols{"workspace_id": otherWorkspace})
		testutil.Call(t, testHandler.CreateSpec,
			newRequest("POST", "/api/specs", map[string]any{
				"title":      "cross-workspace project",
				"project_id": otherProject,
			})).
			Want(http.StatusBadRequest)
	})

	t.Run("valid project_id is stored", func(t *testing.T) {
		projectID := dbfx.Project(t, "Spec create project")
		var out SpecResponse
		testutil.Call(t, testHandler.CreateSpec,
			newRequest("POST", "/api/specs", map[string]any{
				"title":      "Spec with project",
				"project_id": projectID,
			})).
			Want(http.StatusCreated).JSON(&out)
		if out.ProjectID == nil || *out.ProjectID != projectID {
			t.Fatalf("CreateSpec: expected project_id %s, got %+v", projectID, out.ProjectID)
		}
	})
}

// TestSpecUpdate covers rename, project membership changes, omitted-means-
// unchanged semantics, and the loader's 404s.
func TestSpecUpdate(t *testing.T) {
	projectID := dbfx.Project(t, "Spec update project")
	specID := dbfx.Spec(t, "Spec update fixture", testutil.Cols{"project_id": projectID})

	var identifier string
	dbfx.QueryRow(t, `SELECT identifier FROM spec WHERE id = $1`, specID).Scan(&identifier)

	t.Run("rename by identifier", func(t *testing.T) {
		var out SpecResponse
		testutil.Call(t, testHandler.UpdateSpec,
			testutil.WithURLParams(newRequest("PUT", "/api/specs/"+identifier,
				map[string]any{"title": "Renamed spec"}), "id", identifier)).
			Want(http.StatusOK).JSON(&out)
		if out.Title != "Renamed spec" {
			t.Fatalf("UpdateSpec: unexpected title %q", out.Title)
		}
		// Omitted project_id must stay unchanged.
		if out.ProjectID == nil || *out.ProjectID != projectID {
			t.Fatalf("UpdateSpec: omitted project_id changed: %+v", out.ProjectID)
		}
	})

	t.Run("explicit null project_id clears membership", func(t *testing.T) {
		var out SpecResponse
		testutil.Call(t, testHandler.UpdateSpec,
			testutil.WithURLParams(newRequest("PUT", "/api/specs/"+specID,
				map[string]any{"project_id": nil}), "id", specID)).
			Want(http.StatusOK).JSON(&out)
		if out.ProjectID != nil {
			t.Fatalf("UpdateSpec: expected null project_id, got %+v", *out.ProjectID)
		}
	})

	t.Run("reassign to another project", func(t *testing.T) {
		otherProject := dbfx.Project(t, "Spec update project two")
		var out SpecResponse
		testutil.Call(t, testHandler.UpdateSpec,
			testutil.WithURLParams(newRequest("PUT", "/api/specs/"+specID,
				map[string]any{"project_id": otherProject}), "id", specID)).
			Want(http.StatusOK).JSON(&out)
		if out.ProjectID == nil || *out.ProjectID != otherProject {
			t.Fatalf("UpdateSpec: expected project_id %s, got %+v", otherProject, out.ProjectID)
		}
	})

	t.Run("empty title is a 400", func(t *testing.T) {
		testutil.Call(t, testHandler.UpdateSpec,
			testutil.WithURLParams(newRequest("PUT", "/api/specs/"+specID,
				map[string]any{"title": "  "}), "id", specID)).
			Want(http.StatusBadRequest)
	})

	t.Run("unknown project_id is a 400", func(t *testing.T) {
		testutil.Call(t, testHandler.UpdateSpec,
			testutil.WithURLParams(newRequest("PUT", "/api/specs/"+specID,
				map[string]any{"project_id": "00000000-0000-0000-0000-000000000000"}), "id", specID)).
			Want(http.StatusBadRequest)
	})

	t.Run("unknown spec 404s", func(t *testing.T) {
		testutil.Call(t, testHandler.UpdateSpec,
			testutil.WithURLParams(newRequest("PUT", "/api/specs/00000000-0000-0000-0000-000000000000",
				map[string]any{"title": "x"}), "id", "00000000-0000-0000-0000-000000000000")).
			Want(http.StatusNotFound)
	})
}

// TestSpecDelete covers the draft-only gate and the dependent-row cleanup in
// spec_version / spec_issue / spec_subscriber.
func TestSpecDelete(t *testing.T) {
	t.Run("non-draft spec is a 409 and survives", func(t *testing.T) {
		specID := dbfx.Spec(t, "Approved spec fixture", testutil.Cols{"status": "approved"})
		testutil.Call(t, testHandler.DeleteSpec,
			testutil.WithURLParams(newRequest("DELETE", "/api/specs/"+specID, nil), "id", specID)).
			Want(http.StatusConflict)
		if got := dbfx.Count(t, `SELECT count(*) FROM spec WHERE id = $1`, specID); got != 1 {
			t.Fatalf("DeleteSpec 409: spec row must survive, count = %d", got)
		}
	})

	t.Run("draft delete cleans dependents and returns 204", func(t *testing.T) {
		issueID := dbfx.Issue(t, "Spec delete linked issue")
		specID := dbfx.Spec(t, "Draft spec fixture")
		versionID := dbfx.SpecVersion(t, specID, 1)
		dbfx.Exec(t, `UPDATE spec SET current_version_id = $1 WHERE id = $2`, versionID, specID)
		dbfx.InsertNoID(t, "spec_issue",
			testutil.Cols{"spec_id": specID, "issue_id": issueID},
			"spec_id = $1 AND issue_id = $2", specID, issueID)
		dbfx.InsertNoID(t, "spec_subscriber",
			testutil.Cols{"spec_id": specID, "member_id": testUserID},
			"spec_id = $1 AND member_id = $2", specID, testUserID)

		res := testutil.Call(t, testHandler.DeleteSpec,
			testutil.WithURLParams(newRequest("DELETE", "/api/specs/"+specID, nil), "id", specID))
		res.Want(http.StatusNoContent)
		if strings.TrimSpace(res.Body.String()) != "" {
			t.Fatalf("DeleteSpec 204: expected empty body, got %q", res.Body.String())
		}

		if got := dbfx.Count(t, `SELECT count(*) FROM spec WHERE id = $1`, specID); got != 0 {
			t.Fatalf("DeleteSpec: spec row remains, count = %d", got)
		}
		if got := dbfx.Count(t, `SELECT count(*) FROM spec_version WHERE spec_id = $1`, specID); got != 0 {
			t.Fatalf("DeleteSpec: spec_version rows remain, count = %d", got)
		}
		if got := dbfx.Count(t, `SELECT count(*) FROM spec_issue WHERE spec_id = $1`, specID); got != 0 {
			t.Fatalf("DeleteSpec: spec_issue rows remain, count = %d", got)
		}
		if got := dbfx.Count(t, `SELECT count(*) FROM spec_subscriber WHERE spec_id = $1`, specID); got != 0 {
			t.Fatalf("DeleteSpec: spec_subscriber rows remain, count = %d", got)
		}
	})

	t.Run("unknown spec 404s", func(t *testing.T) {
		testutil.Call(t, testHandler.DeleteSpec,
			testutil.WithURLParams(newRequest("DELETE", "/api/specs/00000000-0000-0000-0000-000000000000", nil),
				"id", "00000000-0000-0000-0000-000000000000")).
			Want(http.StatusNotFound)
	})
}
