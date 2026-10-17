package handler

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// specCommentURL is the version-scoped comments path used by every test here.
func specCommentURL(specID, versionID string) string {
	return "/api/specs/" + specID + "/versions/" + versionID + "/comments"
}

// createSpecCommentHTTP posts one spec comment through the real handler and
// returns the recorded response.
func createSpecCommentHTTP(t *testing.T, specID, versionID string, body map[string]any) *testutil.Response {
	t.Helper()
	req := newRequest(http.MethodPost, specCommentURL(specID, versionID), body)
	req = testutil.WithURLParams(req, "id", specID, "versionId", versionID)
	return testutil.Call(t, testHandler.CreateSpecComment, req)
}

// mustCreateSpecComment creates a spec comment and fails the test unless it
// was accepted, returning the decoded response.
func mustCreateSpecComment(t *testing.T, specID, versionID string, body map[string]any) CommentResponse {
	t.Helper()
	var out CommentResponse
	createSpecCommentHTTP(t, specID, versionID, body).Want(http.StatusCreated).JSON(&out)
	return out
}

// TestSpecCommentAnchorCheck is the acceptance test for the exactly-one-of
// (issue_id, spec_version_id) invariant: the database CHECK rejects a row with
// both anchors and a row with neither, while the ordinary write paths keep
// producing valid rows.
func TestSpecCommentAnchorCheck(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	specID := dbfx.Spec(t, "anchor check spec")
	versionID := dbfx.SpecVersion(t, specID, 1)
	issueID := dbfx.Issue(t, "anchor check issue")

	t.Run("both anchors violate the check", func(t *testing.T) {
		_, err := testPool.Exec(ctx, `
			INSERT INTO comment (issue_id, spec_version_id, workspace_id, author_type, author_id, content, type)
			VALUES ($1, $2, $3, 'member', $4, 'both anchors', 'comment')
		`, issueID, versionID, testWorkspaceID, testUserID)
		if err == nil || !strings.Contains(err.Error(), "comment_anchor_check") {
			t.Fatalf("expected comment_anchor_check violation, got %v", err)
		}
	})

	t.Run("neither anchor violates the check", func(t *testing.T) {
		_, err := testPool.Exec(ctx, `
			INSERT INTO comment (issue_id, spec_version_id, workspace_id, author_type, author_id, content, type)
			VALUES (NULL, NULL, $1, 'member', $2, 'no anchor', 'comment')
		`, testWorkspaceID, testUserID)
		if err == nil || !strings.Contains(err.Error(), "comment_anchor_check") {
			t.Fatalf("expected comment_anchor_check violation, got %v", err)
		}
	})

	t.Run("issue comment fixture satisfies exactly-one", func(t *testing.T) {
		commentID := dbfx.Comment(t, issueID, "issue anchored")
		var n int
		dbfx.QueryRow(t,
			`SELECT count(*) FROM comment WHERE id = $1 AND issue_id IS NOT NULL AND spec_version_id IS NULL`,
			commentID).Scan(&n)
		if n != 1 {
			t.Fatalf("issue comment row = %d, want exactly-one satisfied", n)
		}
	})

	t.Run("spec comment create satisfies exactly-one", func(t *testing.T) {
		resp := mustCreateSpecComment(t, specID, versionID, map[string]any{"content": "spec anchored"})
		var issueAnchor, specAnchor *string
		dbfx.QueryRow(t, `SELECT issue_id::text, spec_version_id::text FROM comment WHERE id = $1`, resp.ID).
			Scan(&issueAnchor, &specAnchor)
		if issueAnchor != nil {
			t.Fatalf("spec comment issue_id = %v, want NULL", *issueAnchor)
		}
		if specAnchor == nil || *specAnchor != versionID {
			t.Fatalf("spec comment spec_version_id = %v, want %s", specAnchor, versionID)
		}
	})
}

func TestSpecCommentCreate(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	specID := dbfx.Spec(t, "spec comment create")
	versionID := dbfx.SpecVersion(t, specID, 1)

	t.Run("201 with anchor fields", func(t *testing.T) {
		out := mustCreateSpecComment(t, specID, versionID, map[string]any{"content": "first"})
		if out.ID == "" {
			t.Fatalf("CreateSpecComment: empty id in %+v", out)
		}
		if out.IssueID != "" {
			t.Fatalf("CreateSpecComment: issue_id = %q, want empty", out.IssueID)
		}
		if out.SpecVersionID != versionID {
			t.Fatalf("CreateSpecComment: spec_version_id = %q, want %s", out.SpecVersionID, versionID)
		}
		if out.Content != "first" || out.Type != "comment" || out.AuthorType != "member" {
			t.Fatalf("CreateSpecComment: unexpected response %+v", out)
		}
	})

	t.Run("empty content 400s", func(t *testing.T) {
		createSpecCommentHTTP(t, specID, versionID, map[string]any{"content": ""}).
			Want(http.StatusBadRequest)
	})

	t.Run("invalid type 400s", func(t *testing.T) {
		createSpecCommentHTTP(t, specID, versionID, map[string]any{
			"content": "forging a system row", "type": "system",
		}).Want(http.StatusBadRequest)
	})

	t.Run("unknown version 404s", func(t *testing.T) {
		createSpecCommentHTTP(t, specID, "00000000-0000-4000-8000-000000000000",
			map[string]any{"content": "nowhere"}).Want(http.StatusNotFound)
	})

	t.Run("version from another spec 404s", func(t *testing.T) {
		otherSpec := dbfx.Spec(t, "spec comment create other")
		otherVersion := dbfx.SpecVersion(t, otherSpec, 1)
		createSpecCommentHTTP(t, specID, otherVersion, map[string]any{"content": "wrong spec"}).
			Want(http.StatusNotFound)
	})

	t.Run("other workspace's spec 404s", func(t *testing.T) {
		otherWorkspace := dbfx.Workspace(t, "Spec Comment Other WS", "spec-comment-other-ws")
		otherSpec := dbfx.Spec(t, "other workspace spec", testutil.Cols{"workspace_id": otherWorkspace})
		otherVersion := dbfx.SpecVersion(t, otherSpec, 1)
		createSpecCommentHTTP(t, otherSpec, otherVersion, map[string]any{"content": "foreign"}).
			Want(http.StatusNotFound)
	})

	t.Run("parent on a different version 400s", func(t *testing.T) {
		version2 := dbfx.SpecVersion(t, specID, 2)
		parent := mustCreateSpecComment(t, specID, version2, map[string]any{"content": "v2 parent"})
		createSpecCommentHTTP(t, specID, versionID, map[string]any{
			"content": "cross-anchor reply", "parent_id": parent.ID,
		}).Want(http.StatusBadRequest)
	})

	t.Run("reply on the same version 201s with parent", func(t *testing.T) {
		parent := mustCreateSpecComment(t, specID, versionID, map[string]any{"content": "root"})
		out := mustCreateSpecComment(t, specID, versionID, map[string]any{
			"content": "reply", "parent_id": parent.ID,
		})
		if out.ParentID == nil || *out.ParentID != parent.ID {
			t.Fatalf("reply parent_id = %v, want %s", out.ParentID, parent.ID)
		}
	})

	t.Run("unknown parent 400s", func(t *testing.T) {
		createSpecCommentHTTP(t, specID, versionID, map[string]any{
			"content": "orphan reply", "parent_id": "00000000-0000-4000-8000-000000000000",
		}).Want(http.StatusBadRequest)
	})
}

func TestSpecCommentListIsolation(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	specID := dbfx.Spec(t, "spec comment list isolation")
	v1 := dbfx.SpecVersion(t, specID, 1)
	v2 := dbfx.SpecVersion(t, specID, 2)

	v1Root := mustCreateSpecComment(t, specID, v1, map[string]any{"content": "v1 root"})
	mustCreateSpecComment(t, specID, v1, map[string]any{"content": "v1 reply", "parent_id": v1Root.ID})
	v2Root := mustCreateSpecComment(t, specID, v2, map[string]any{"content": "v2 root"})

	t.Run("version list returns only that version's thread", func(t *testing.T) {
		req := newRequest(http.MethodGet, specCommentURL(specID, v1), nil)
		req = testutil.WithURLParams(req, "id", specID, "versionId", v1)
		var out []CommentResponse
		testutil.Call(t, testHandler.ListSpecVersionComments, req).Want(http.StatusOK).JSON(&out)
		if len(out) != 2 {
			t.Fatalf("ListSpecVersionComments v1 = %d comments, want 2: %+v", len(out), out)
		}
		for _, c := range out {
			if c.SpecVersionID != v1 {
				t.Fatalf("v1 list returned comment anchored to %q: %+v", c.SpecVersionID, c)
			}
		}
		if out[0].ID != v1Root.ID {
			t.Fatalf("v1 list order: first = %s, want root %s", out[0].ID, v1Root.ID)
		}
	})

	t.Run("aggregate list returns both versions with version numbers", func(t *testing.T) {
		req := newRequest(http.MethodGet, "/api/specs/"+specID+"/comments", nil)
		req = testutil.WithURLParams(req, "id", specID)
		var out []CommentResponse
		testutil.Call(t, testHandler.ListSpecComments, req).Want(http.StatusOK).JSON(&out)
		if len(out) != 3 {
			t.Fatalf("ListSpecComments = %d comments, want 3: %+v", len(out), out)
		}
		// Newest version first: both v2 rows precede both v1 rows.
		byID := map[string]CommentResponse{}
		for _, c := range out {
			byID[c.ID] = c
		}
		if byID[v2Root.ID].SpecVersionNumber != 2 {
			t.Fatalf("v2 root version number = %d, want 2", byID[v2Root.ID].SpecVersionNumber)
		}
		if byID[v1Root.ID].SpecVersionNumber != 1 {
			t.Fatalf("v1 root version number = %d, want 1", byID[v1Root.ID].SpecVersionNumber)
		}
		for i, c := range out {
			want := 1
			if i == 0 {
				want = 2
			}
			if c.SpecVersionNumber != want {
				t.Fatalf("position %d: spec_version_number = %d, want %d (%+v)", i, c.SpecVersionNumber, want, c)
			}
		}
	})
}

// specCommentEventCapture records comment:resolved / comment:unresolved events
// for one spec version, the spec-side analogue of commentEventCapture.
type specCommentEventCapture struct {
	mu     sync.Mutex
	events []struct {
		Type      string
		CommentID string
	}
}

func captureSpecCommentEvents(t *testing.T, versionID string) *specCommentEventCapture {
	t.Helper()
	cap := &specCommentEventCapture{}
	record := func(e events.Event) {
		m, ok := e.Payload.(map[string]any)
		if !ok {
			return
		}
		c, ok := m["comment"].(CommentResponse)
		if !ok || c.SpecVersionID != versionID {
			return
		}
		cap.mu.Lock()
		cap.events = append(cap.events, struct {
			Type      string
			CommentID string
		}{e.Type, c.ID})
		cap.mu.Unlock()
	}
	testHandler.Bus.Subscribe(protocol.EventCommentResolved, record)
	testHandler.Bus.Subscribe(protocol.EventCommentUnresolved, record)
	return cap
}

func (c *specCommentEventCapture) countFor(eventType, commentID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, e := range c.events {
		if e.Type == eventType && e.CommentID == commentID {
			n++
		}
	}
	return n
}

func TestSpecCommentResolve(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	specID := dbfx.Spec(t, "spec comment resolve")
	versionID := dbfx.SpecVersion(t, specID, 1)

	root := mustCreateSpecComment(t, specID, versionID, map[string]any{"content": "resolve root"})
	a := mustCreateSpecComment(t, specID, versionID, map[string]any{"content": "a", "parent_id": root.ID})
	b := mustCreateSpecComment(t, specID, versionID, map[string]any{"content": "b", "parent_id": root.ID})

	cap := captureSpecCommentEvents(t, versionID)

	resolve := func(commentID string) CommentResponse {
		t.Helper()
		req := newRequest(http.MethodPost, "/api/comments/"+commentID+"/resolve", nil)
		req = testutil.WithURLParams(req, "commentId", commentID)
		var out CommentResponse
		testutil.Call(t, testHandler.ResolveComment, req).Want(http.StatusOK).JSON(&out)
		return out
	}

	t.Run("resolve sets resolved_at and fires comment:resolved", func(t *testing.T) {
		out := resolve(a.ID)
		if out.ResolvedAt == nil {
			t.Fatalf("ResolveComment: resolved_at not set: %+v", out)
		}
		var resolvedAt *time.Time
		dbfx.QueryRow(t, `SELECT resolved_at FROM comment WHERE id = $1`, a.ID).Scan(&resolvedAt)
		if resolvedAt == nil {
			t.Fatalf("resolved_at not persisted for %s", a.ID)
		}
		if n := cap.countFor(protocol.EventCommentResolved, a.ID); n != 1 {
			t.Fatalf("comment:resolved events for %s = %d, want 1", a.ID, n)
		}
	})

	t.Run("resolving a sibling clears the first resolution in the same tx", func(t *testing.T) {
		resolve(b.ID)
		if commentResolved(t, a.ID) {
			t.Fatalf("a should be cleared after b resolved")
		}
		if !commentResolved(t, b.ID) {
			t.Fatalf("b should be resolved")
		}
		if n := cap.countFor(protocol.EventCommentUnresolved, a.ID); n != 1 {
			t.Fatalf("comment:unresolved events for cleared %s = %d, want 1", a.ID, n)
		}
	})

	t.Run("unresolve clears and fires comment:unresolved", func(t *testing.T) {
		req := newRequest(http.MethodDelete, "/api/comments/"+b.ID+"/resolve", nil)
		req = testutil.WithURLParams(req, "commentId", b.ID)
		var out CommentResponse
		testutil.Call(t, testHandler.UnresolveComment, req).Want(http.StatusOK).JSON(&out)
		if out.ResolvedAt != nil {
			t.Fatalf("UnresolveComment: resolved_at still set: %+v", out)
		}
		if n := cap.countFor(protocol.EventCommentUnresolved, b.ID); n != 1 {
			t.Fatalf("comment:unresolved events for %s = %d, want 1", b.ID, n)
		}
	})
}

func TestSpecCommentUpdate(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	specID := dbfx.Spec(t, "spec comment update")
	versionID := dbfx.SpecVersion(t, specID, 1)

	update := func(commentID string, body map[string]any) *testutil.Response {
		t.Helper()
		req := newRequest(http.MethodPut, "/api/comments/"+commentID, body)
		req = testutil.WithURLParams(req, "commentId", commentID)
		return testutil.Call(t, testHandler.UpdateComment, req)
	}

	t.Run("author edit bumps revision and keeps the anchor", func(t *testing.T) {
		created := mustCreateSpecComment(t, specID, versionID, map[string]any{"content": "before"})
		var out CommentResponse
		update(created.ID, map[string]any{"content": "after"}).Want(http.StatusOK).JSON(&out)
		if out.Content != "after" {
			t.Fatalf("UpdateComment: content = %q, want after", out.Content)
		}
		if out.Revision != created.Revision+1 {
			t.Fatalf("UpdateComment: revision = %d, want %d", out.Revision, created.Revision+1)
		}
		if out.SpecVersionID != versionID {
			t.Fatalf("UpdateComment: spec_version_id = %q, want %s", out.SpecVersionID, versionID)
		}
		if out.IssueRevision != 0 {
			t.Fatalf("UpdateComment: issue_revision = %d, want 0 for spec comments", out.IssueRevision)
		}
	})

	t.Run("non-author non-admin edit 403s", func(t *testing.T) {
		created := mustCreateSpecComment(t, specID, versionID, map[string]any{"content": "mine"})
		otherUser := dbfx.User(t, "Spec Comment Other Member", "spec-comment-other@example.test")
		dbfx.Member(t, testWorkspaceID, otherUser, "member")

		req := newRequest(http.MethodPut, "/api/comments/"+created.ID, map[string]any{"content": "hijack"})
		req = testutil.WithURLParams(req, "commentId", created.ID)
		req.Header.Set("X-User-ID", otherUser)
		testutil.Call(t, testHandler.UpdateComment, req).Want(http.StatusForbidden)
	})

	t.Run("agent mention in an edit does not enqueue a task", func(t *testing.T) {
		agentID := createHandlerTestAgent(t, "spec comment update agent", nil)
		created := mustCreateSpecComment(t, specID, versionID, map[string]any{"content": "plain"})
		content := fmt.Sprintf("[@Agent](mention://agent/%s) please review", agentID)
		update(created.ID, map[string]any{"content": content}).Want(http.StatusOK)

		if n := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE trigger_comment_id = $1`, created.ID); n != 0 {
			t.Fatalf("agent_task_queue rows for spec comment %s = %d, want 0", created.ID, n)
		}
	})
}

func TestSpecCommentReactions(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	specID := dbfx.Spec(t, "spec comment reactions")
	versionID := dbfx.SpecVersion(t, specID, 1)
	comment := mustCreateSpecComment(t, specID, versionID, map[string]any{"content": "react to me"})

	t.Run("add reaction 201s", func(t *testing.T) {
		req := newRequest(http.MethodPost, "/api/comments/"+comment.ID+"/reactions", map[string]any{"emoji": "👍"})
		req = testutil.WithURLParams(req, "commentId", comment.ID)
		var out ReactionResponse
		testutil.Call(t, testHandler.AddReaction, req).Want(http.StatusCreated).JSON(&out)
		if out.CommentID != comment.ID || out.Emoji != "👍" {
			t.Fatalf("AddReaction: unexpected response %+v", out)
		}
		if n := dbfx.Count(t, `SELECT count(*) FROM comment_reaction WHERE comment_id = $1`, comment.ID); n != 1 {
			t.Fatalf("comment_reaction rows = %d, want 1", n)
		}
	})

	t.Run("remove reaction round-trips", func(t *testing.T) {
		req := newRequest(http.MethodDelete, "/api/comments/"+comment.ID+"/reactions", map[string]any{"emoji": "👍"})
		req = testutil.WithURLParams(req, "commentId", comment.ID)
		testutil.Call(t, testHandler.RemoveReaction, req).Want(http.StatusNoContent)
		if n := dbfx.Count(t, `SELECT count(*) FROM comment_reaction WHERE comment_id = $1`, comment.ID); n != 0 {
			t.Fatalf("comment_reaction rows after remove = %d, want 0", n)
		}
	})
}

func TestSpecCommentAttachments(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	specID := dbfx.Spec(t, "spec comment attachments")
	versionID := dbfx.SpecVersion(t, specID, 1)

	// unboundUpload mirrors what UploadFile persists when no issue_id is
	// given: an attachment whose issue_id stays NULL.
	unboundUpload := func(t *testing.T) string {
		t.Helper()
		return dbfx.Insert(t, "attachment", testutil.Cols{
			"workspace_id":  testWorkspaceID,
			"uploader_type": "member", "uploader_id": testUserID,
			"filename": "spec-note.txt", "url": "https://example.test/spec-note.txt",
			"content_type": "text/plain", "size_bytes": 1,
		})
	}

	t.Run("unbound upload links to the spec comment", func(t *testing.T) {
		attachmentID := unboundUpload(t)
		out := mustCreateSpecComment(t, specID, versionID, map[string]any{
			"content": "with file", "attachment_ids": []string{attachmentID},
		})
		if len(out.Attachments) != 1 || out.Attachments[0].ID != attachmentID {
			t.Fatalf("response attachments = %+v, want the posted one", out.Attachments)
		}
		if n := dbfx.Count(t, `SELECT count(*) FROM attachment WHERE id = $1 AND comment_id = $2`, attachmentID, out.ID); n != 1 {
			t.Fatalf("attachment linked rows = %d, want 1", n)
		}
	})

	t.Run("attachment already bound to an issue is refused", func(t *testing.T) {
		issueID := dbfx.Issue(t, "spec comment attachment owner")
		issueAttachment := dbfx.Insert(t, "attachment", testutil.Cols{
			"workspace_id": testWorkspaceID, "issue_id": issueID,
			"uploader_type": "member", "uploader_id": testUserID,
			"filename": "issue-file.txt", "url": "https://example.test/issue-file.txt",
			"content_type": "text/plain", "size_bytes": 1,
		})
		createSpecCommentHTTP(t, specID, versionID, map[string]any{
			"content": "stealing", "attachment_ids": []string{issueAttachment},
		}).Want(http.StatusConflict)
	})
}

func TestSpecCommentDelete(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	specID := dbfx.Spec(t, "spec comment delete")
	versionID := dbfx.SpecVersion(t, specID, 1)

	root := mustCreateSpecComment(t, specID, versionID, map[string]any{"content": "delete root"})
	reply := mustCreateSpecComment(t, specID, versionID, map[string]any{"content": "delete reply", "parent_id": root.ID})

	delete := func(commentID string) *testutil.Response {
		t.Helper()
		req := newRequest(http.MethodDelete, "/api/comments/"+commentID, nil)
		req = testutil.WithURLParams(req, "commentId", commentID)
		return testutil.Call(t, testHandler.DeleteComment, req)
	}

	// Deleting the root while the reply lives tombstones it in place — the
	// reply keeps its parent and the thread survives.
	delete(root.ID).Want(http.StatusNoContent)

	var deletedAt *time.Time
	var content string
	dbfx.QueryRow(t, `SELECT deleted_at, content FROM comment WHERE id = $1`, root.ID).Scan(&deletedAt, &content)
	if deletedAt == nil || content != "" {
		t.Fatalf("root after delete: deleted_at=%v content=%q, want tombstone", deletedAt, content)
	}
	var parentID string
	dbfx.QueryRow(t, `SELECT parent_id::text FROM comment WHERE id = $1`, reply.ID).Scan(&parentID)
	if parentID != root.ID {
		t.Fatalf("reply parent = %s, want %s", parentID, root.ID)
	}

	// Deleting the last reply prunes the now-replyless tombstone ancestor.
	delete(reply.ID).Want(http.StatusNoContent)
	if n := dbfx.Count(t, `SELECT count(*) FROM comment WHERE id = $1`, root.ID); n != 0 {
		t.Fatalf("root tombstone rows after reply delete = %d, want 0 (pruned)", n)
	}
}
