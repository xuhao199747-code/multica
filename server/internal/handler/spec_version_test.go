package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// randomSuffix keeps fixture identifiers unique under the workspace-scoped
// unique index without pulling in a UUID helper from elsewhere.
func randomSuffix(t *testing.T) string {
	t.Helper()
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return hex.EncodeToString(b[:])
}

// insertSpecForVersions creates a spec fixture and guarantees its version
// rows (including ones the handlers create) go away with it.
func insertSpecForVersions(t *testing.T, status string) string {
	t.Helper()
	specID := dbfx.Insert(t, "spec", testutil.Cols{
		"workspace_id": testWorkspaceID,
		"title":        "Version lifecycle spec",
		"identifier":   "SVLTST-" + randomSuffix(t),
		"status":       status,
		"created_by":   testUserID,
	})
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM spec_version WHERE spec_id = $1`, specID)
	})
	return specID
}

func publishSpecVersion(t *testing.T, specID, content, changelog string) *httptest.ResponseRecorder {
	t.Helper()
	req := newRequest("POST", "/api/specs/"+specID+"/versions", map[string]any{
		"content":   content,
		"changelog": changelog,
	})
	req = withURLParam(req, "id", specID)
	w := httptest.NewRecorder()
	testHandler.PublishSpecVersion(w, req)
	return w
}

func transitionSpec(t *testing.T, specID, to string) *httptest.ResponseRecorder {
	t.Helper()
	req := newRequest("POST", "/api/specs/"+specID+"/transition", map[string]any{"to": to})
	req = withURLParam(req, "id", specID)
	w := httptest.NewRecorder()
	testHandler.TransitionSpecStatus(w, req)
	return w
}

func patchLatestSpecVersion(t *testing.T, specID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	req := newRequest("PATCH", "/api/specs/"+specID+"/versions/latest", body)
	req = withURLParam(req, "id", specID)
	w := httptest.NewRecorder()
	testHandler.UpdateLatestSpecVersion(w, req)
	return w
}

// withSpecVersionParams sets both chi params in a single route context;
// withURLParam builds a fresh context per call, so chaining it would drop
// the id param.
func withSpecVersionParams(req *http.Request, specID, number string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", specID)
	if number != "" {
		rctx.URLParams.Add("number", number)
	}
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func specStatus(t *testing.T, specID string) string {
	t.Helper()
	var status string
	dbfx.QueryRow(t, `SELECT status FROM spec WHERE id = $1`, specID).Scan(&status)
	return status
}

func TestPublishSpecVersion_AppendsAndBumpsCurrent(t *testing.T) {
	specID := insertSpecForVersions(t, "draft")

	w := publishSpecVersion(t, specID, "# Auth rules", "Initial draft")
	if w.Code != http.StatusCreated {
		t.Fatalf("publish v1: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var v1 SpecVersionItem
	if err := json.NewDecoder(w.Body).Decode(&v1); err != nil {
		t.Fatalf("decode v1: %v", err)
	}
	if v1.Number != 1 {
		t.Fatalf("v1 number = %d, want 1", v1.Number)
	}
	if v1.Content != "# Auth rules" || v1.Changelog != "Initial draft" {
		t.Fatalf("v1 content/changelog = %q/%q", v1.Content, v1.Changelog)
	}

	w = publishSpecVersion(t, specID, "# Auth rules v2", "Add lockout")
	if w.Code != http.StatusCreated {
		t.Fatalf("publish v2: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var v2 SpecVersionItem
	if err := json.NewDecoder(w.Body).Decode(&v2); err != nil {
		t.Fatalf("decode v2: %v", err)
	}
	if v2.Number != 2 {
		t.Fatalf("v2 number = %d, want 2", v2.Number)
	}

	var currentID string
	dbfx.QueryRow(t, `SELECT current_version_id FROM spec WHERE id = $1`, specID).Scan(&currentID)
	if currentID != v2.ID {
		t.Fatalf("current_version_id = %q, want latest version %q", currentID, v2.ID)
	}

	// v1 is untouched by the v2 publish.
	req := newRequest("GET", "/api/specs/"+specID+"/versions/1", nil)
	req = withSpecVersionParams(req, specID, "1")
	w = httptest.NewRecorder()
	testHandler.GetSpecVersion(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get v1: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var stored SpecVersionItem
	if err := json.NewDecoder(w.Body).Decode(&stored); err != nil {
		t.Fatalf("decode v1 detail: %v", err)
	}
	if stored.ID != v1.ID || stored.Content != "# Auth rules" {
		t.Fatalf("v1 mutated by v2 publish: %+v", stored)
	}
}

func TestPublishSpecVersion_RequiresContentAndChangelog(t *testing.T) {
	specID := insertSpecForVersions(t, "draft")

	raw := []map[string]any{
		{"content": "# Rules"},
		{"content": "# Rules", "changelog": ""},
		{"content": "# Rules", "changelog": "   "},
		{"changelog": "x"},
		{"content": "  ", "changelog": "x"},
	}
	for i, body := range raw {
		req := newRequest("POST", "/api/specs/"+specID+"/versions", body)
		req = withURLParam(req, "id", specID)
		w := httptest.NewRecorder()
		testHandler.PublishSpecVersion(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("case %d: expected 400, got %d: %s", i, w.Code, w.Body.String())
		}
	}

	if n := dbfx.Count(t, `SELECT count(*) FROM spec_version WHERE spec_id = $1`, specID); n != 0 {
		t.Fatalf("rejected publishes created %d versions, want 0", n)
	}
}

func TestPublishSpecVersion_DeprecatedSpecIsFrozen(t *testing.T) {
	specID := insertSpecForVersions(t, "deprecated")
	w := publishSpecVersion(t, specID, "# Rules", "comeback")
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM spec_version WHERE spec_id = $1`, specID); n != 0 {
		t.Fatalf("deprecated spec got %d versions, want 0", n)
	}
}

func TestListSpecVersions_OrdersNewestFirst(t *testing.T) {
	specID := insertSpecForVersions(t, "draft")
	publishSpecVersion(t, specID, "v1", "c1")
	publishSpecVersion(t, specID, "v2", "c2")

	req := newRequest("GET", "/api/specs/"+specID+"/versions", nil)
	req = withURLParam(req, "id", specID)
	w := httptest.NewRecorder()
	testHandler.ListSpecVersions(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Versions []SpecVersionItem `json:"versions"`
		Total    int               `json:"total"`
	}
	if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if out.Total != 2 || len(out.Versions) != 2 {
		t.Fatalf("list = %d versions, want 2", out.Total)
	}
	if out.Versions[0].Number != 2 || out.Versions[1].Number != 1 {
		t.Fatalf("order = %d,%d, want 2,1", out.Versions[0].Number, out.Versions[1].Number)
	}
}

func TestGetSpecVersion_BadNumberAndMissing(t *testing.T) {
	specID := insertSpecForVersions(t, "draft")
	publishSpecVersion(t, specID, "v1", "c1")

	for _, num := range []string{"abc", "0", "-1"} {
		req := newRequest("GET", "/api/specs/"+specID+"/versions/"+num, nil)
		req = withSpecVersionParams(req, specID, num)
		w := httptest.NewRecorder()
		testHandler.GetSpecVersion(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("number %q: expected 400, got %d", num, w.Code)
		}
	}

	req := newRequest("GET", "/api/specs/"+specID+"/versions/99", nil)
	req = withSpecVersionParams(req, specID, "99")
	w := httptest.NewRecorder()
	testHandler.GetSpecVersion(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing version: expected 404, got %d", w.Code)
	}
}

func TestSpecTransition_HappyPath(t *testing.T) {
	specID := insertSpecForVersions(t, "draft")

	// in_review without any version is rejected.
	w := transitionSpec(t, specID, "in_review")
	if w.Code != http.StatusConflict {
		t.Fatalf("in_review without versions: expected 409, got %d: %s", w.Code, w.Body.String())
	}

	publishSpecVersion(t, specID, "v1", "c1")

	w = transitionSpec(t, specID, "in_review")
	if w.Code != http.StatusOK {
		t.Fatalf("draft→in_review: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var st SpecStateItem
	if err := json.NewDecoder(w.Body).Decode(&st); err != nil {
		t.Fatalf("decode transition: %v", err)
	}
	if st.Status != "in_review" {
		t.Fatalf("transition status = %q", st.Status)
	}

	w = transitionSpec(t, specID, "approved")
	if w.Code != http.StatusOK {
		t.Fatalf("in_review→approved: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := specStatus(t, specID); got != "approved" {
		t.Fatalf("status in DB = %q, want approved", got)
	}
}

func TestSpecTransition_IllegalTargets(t *testing.T) {
	cases := []struct {
		name   string
		from   string
		to     string
		prepub bool
	}{
		{"draft to approved", "draft", "approved", true},
		{"draft to draft", "draft", "draft", true},
		{"approved to in_review", "approved", "in_review", true},
		{"approved to approved", "approved", "approved", true},
		{"deprecated to draft", "deprecated", "draft", true},
		{"deprecated to approved", "deprecated", "approved", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			specID := insertSpecForVersions(t, tc.from)
			if tc.prepub {
				publishSpecVersion(t, specID, "v1", "c1")
			}
			w := transitionSpec(t, specID, tc.to)
			if w.Code != http.StatusConflict {
				t.Fatalf("%s→%s: expected 409, got %d: %s", tc.from, tc.to, w.Code, w.Body.String())
			}
			if got := specStatus(t, specID); got != tc.from {
				t.Fatalf("status drifted to %q after illegal transition", got)
			}
		})
	}

	// Unknown target status is a bad request, not a conflict.
	specID := insertSpecForVersions(t, "draft")
	w := transitionSpec(t, specID, "published")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown target: expected 400, got %d", w.Code)
	}
}

func TestSpecTransition_DeprecateFromEveryState(t *testing.T) {
	for _, from := range []string{"draft", "in_review", "approved"} {
		t.Run(from, func(t *testing.T) {
			specID := insertSpecForVersions(t, from)
			publishSpecVersion(t, specID, "v1", "c1")
			w := transitionSpec(t, specID, "deprecated")
			if w.Code != http.StatusOK {
				t.Fatalf("%s→deprecated: expected 200, got %d: %s", from, w.Code, w.Body.String())
			}
			if got := specStatus(t, specID); got != "deprecated" {
				t.Fatalf("status = %q, want deprecated", got)
			}
		})
	}
}

func TestSpecVersions_OnlyLatestDraftEditable(t *testing.T) {
	specID := insertSpecForVersions(t, "draft")
	w := publishSpecVersion(t, specID, "v1 body", "c1")
	var v1 SpecVersionItem
	if err := json.NewDecoder(w.Body).Decode(&v1); err != nil {
		t.Fatalf("decode v1: %v", err)
	}
	w = publishSpecVersion(t, specID, "v2 body", "c2")
	var v2 SpecVersionItem
	if err := json.NewDecoder(w.Body).Decode(&v2); err != nil {
		t.Fatalf("decode v2: %v", err)
	}

	// Draft: editing latest updates in place, no new version, older snapshot untouched.
	w = patchLatestSpecVersion(t, specID, map[string]any{"content": "v2 revised"})
	if w.Code != http.StatusOK {
		t.Fatalf("edit latest: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var edited SpecVersionItem
	if err := json.NewDecoder(w.Body).Decode(&edited); err != nil {
		t.Fatalf("decode edited: %v", err)
	}
	if edited.ID != v2.ID || edited.Number != 2 {
		t.Fatalf("edit created or moved versions: %+v", edited)
	}
	if edited.Content != "v2 revised" || edited.Changelog != "c2" {
		t.Fatalf("edited content/changelog = %q/%q", edited.Content, edited.Changelog)
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM spec_version WHERE spec_id = $1`, specID); n != 2 {
		t.Fatalf("edit changed version count to %d, want 2", n)
	}
	var v1Content string
	dbfx.QueryRow(t, `SELECT content FROM spec_version WHERE spec_id = $1 AND number = 1`, specID).Scan(&v1Content)
	if v1Content != "v1 body" {
		t.Fatalf("older snapshot mutated: %q", v1Content)
	}

	// Changelog is optional on edit; empty string is rejected.
	w = patchLatestSpecVersion(t, specID, map[string]any{"content": "v2 again", "changelog": ""})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty changelog on edit: expected 400, got %d", w.Code)
	}
	w = patchLatestSpecVersion(t, specID, map[string]any{"content": "v2 again", "changelog": "c2 revised"})
	if w.Code != http.StatusOK {
		t.Fatalf("edit with changelog: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Leaving draft freezes every snapshot.
	w = transitionSpec(t, specID, "in_review")
	if w.Code != http.StatusOK {
		t.Fatalf("transition: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	w = patchLatestSpecVersion(t, specID, map[string]any{"content": "sneaky"})
	if w.Code != http.StatusConflict {
		t.Fatalf("edit in review: expected 409, got %d", w.Code)
	}
	w = transitionSpec(t, specID, "approved")
	if w.Code != http.StatusOK {
		t.Fatalf("approve: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	w = patchLatestSpecVersion(t, specID, map[string]any{"content": "sneakier"})
	if w.Code != http.StatusConflict {
		t.Fatalf("edit approved: expected 409, got %d", w.Code)
	}

	var content string
	dbfx.QueryRow(t, `SELECT content FROM spec_version WHERE spec_id = $1 AND number = 2`, specID).Scan(&content)
	if content != "v2 again" {
		t.Fatalf("published snapshot mutated after transitions: %q", content)
	}
}

func TestSpecVersions_EditRequiresVersions(t *testing.T) {
	specID := insertSpecForVersions(t, "draft")
	w := patchLatestSpecVersion(t, specID, map[string]any{"content": "x"})
	if w.Code != http.StatusNotFound {
		t.Fatalf("edit with no versions: expected 404, got %d", w.Code)
	}
}

func TestSpecVersions_SpecNotFound(t *testing.T) {
	missing := "00000000-0000-0000-0000-0000000000ff"
	req := newRequest("POST", "/api/specs/"+missing+"/versions", map[string]any{
		"content": "x", "changelog": "y",
	})
	req = withURLParam(req, "id", missing)
	w := httptest.NewRecorder()
	testHandler.PublishSpecVersion(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("publish to missing spec: expected 404, got %d", w.Code)
	}

	req = newRequest("POST", "/api/specs/"+missing+"/transition", map[string]any{"to": "deprecated"})
	req = withURLParam(req, "id", missing)
	w = httptest.NewRecorder()
	testHandler.TransitionSpecStatus(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("transition on missing spec: expected 404, got %d", w.Code)
	}
}

func TestPublishSpecVersion_TrimsWhitespace(t *testing.T) {
	specID := insertSpecForVersions(t, "draft")
	w := publishSpecVersion(t, specID, "  # Rules\n", "  first cut  ")
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var v SpecVersionItem
	if err := json.NewDecoder(w.Body).Decode(&v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if v.Content != "# Rules" || v.Changelog != "first cut" {
		t.Fatalf("content/changelog not trimmed: %q/%q", v.Content, v.Changelog)
	}
}
