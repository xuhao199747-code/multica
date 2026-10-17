package handler

import (
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

// specApprovalFixture builds a spec with one published version, a linked
// issue, and four members: the creator (auto-subscribed to the spec), an
// extra spec subscriber, an issue subscriber, and the future approver.
// The spec sits in in_review so the approver can perform the final
// transition.
func specApprovalFixture(t *testing.T) (specID, issueID, approverID string) {
	t.Helper()

	var created SpecResponse
	testutil.Call(t, testHandler.CreateSpec,
		newRequest("POST", "/api/specs", map[string]any{"title": "Spec approval notify"})).
		Want(http.StatusCreated).JSON(&created)
	specID = created.ID

	testutil.Call(t, testHandler.PublishSpecVersion,
		testutil.WithURLParams(newRequest("POST", "/api/specs/"+specID+"/versions", map[string]any{
			"content":   "the spec body",
			"changelog": "initial draft",
		}), "id", specID)).
		Want(http.StatusCreated)

	testutil.Call(t, testHandler.TransitionSpecStatus,
		testutil.WithURLParams(newRequest("POST", "/api/specs/"+specID+"/transition", map[string]any{
			"to": "in_review",
		}), "id", specID)).
		Want(http.StatusOK)

	issueID = dbfx.Issue(t, "Issue watching a spec approval")
	testutil.Call(t, testHandler.LinkSpecIssue,
		testutil.WithURLParams(newRequest("POST", "/api/specs/"+specID+"/issues", map[string]any{
			"issue_id": issueID,
		}), "id", specID)).
		Want(http.StatusCreated)

	// Issue subscriber: plain manual subscription row, the shape the
	// issue-subscriber fan-out reads. issue_subscriber has a composite key and
	// no id column, so this is a plain INSERT, not dbfx.Insert.
	issueWatcher := dbfx.User(t, "spec approval issue watcher", "spec-approval-issue-watcher@example.com")
	dbfx.Member(t, testWorkspaceID, issueWatcher, "member")
	dbfx.Exec(t, `INSERT INTO issue_subscriber (issue_id, user_type, user_id, reason) VALUES ($1, 'member', $2, 'manual')`, issueID, issueWatcher)

	// Extra spec subscriber.
	specWatcher := dbfx.User(t, "spec approval spec watcher", "spec-approval-spec-watcher@example.com")
	dbfx.Member(t, testWorkspaceID, specWatcher, "member")
	testutil.Call(t, testHandler.SubscribeToSpec,
		testutil.WithURLParams(newRequest("POST", "/api/specs/"+specID+"/subscribe", map[string]any{
			"user_id":   specWatcher,
			"user_type": "member",
		}), "id", specID)).
		Want(http.StatusOK)

	approverUser := dbfx.User(t, "spec approval approver", "spec-approval-approver@example.com")
	dbfx.Member(t, testWorkspaceID, approverUser, "member")
	return specID, issueID, approverUser
}

func specApprovedInboxCount(t *testing.T, recipientID, specID string) int {
	t.Helper()
	return dbfx.Count(t,
		`SELECT count(*) FROM inbox_item
		 WHERE recipient_id = $1 AND type = 'spec_approved' AND details->>'spec_id' = $2`,
		recipientID, specID)
}

// TestSpecApprovalNotifiesSubscribers covers the DAWE-7 acceptance: entering
// approved generates inbox notifications for the spec's subscribers and the
// subscribers of every linked issue, skipping the actor.
func TestSpecApprovalNotifiesSubscribers(t *testing.T) {
	specID, issueID, approver := specApprovalFixture(t)

	testutil.Call(t, testHandler.TransitionSpecStatus,
		testutil.WithURLParams(newRequestAs(approver, "POST", "/api/specs/"+specID+"/transition", map[string]any{
			"to": "approved",
		}), "id", specID)).
		Want(http.StatusOK)

	// Creator auto-subscribed at create time.
	if got := specApprovedInboxCount(t, testUserID, specID); got != 1 {
		t.Fatalf("creator must be notified exactly once, got %d", got)
	}
	// The linked issue's subscriber is notified through the issue list.
	var issueWatcher string
	dbfx.QueryRow(t, `SELECT user_id FROM issue_subscriber WHERE issue_id = $1`, issueID).Scan(&issueWatcher)
	if got := specApprovedInboxCount(t, issueWatcher, specID); got != 1 {
		t.Fatalf("issue subscriber must be notified exactly once, got %d", got)
	}
	// The manual spec subscriber too.
	var specWatcher string
	dbfx.QueryRow(t, `SELECT member_id FROM spec_subscriber WHERE spec_id = $1 AND member_id <> $2`, specID, testUserID).
		Scan(&specWatcher)
	if got := specApprovedInboxCount(t, specWatcher, specID); got != 1 {
		t.Fatalf("spec subscriber must be notified exactly once, got %d", got)
	}
	// The actor is never notified about their own action.
	if got := specApprovedInboxCount(t, approver, specID); got != 0 {
		t.Fatalf("actor must not be notified, got %d", got)
	}

	t.Run("notification carries the changelog", func(t *testing.T) {
		var body, changelog string
		dbfx.QueryRow(t,
			`SELECT body, details->>'changelog' FROM inbox_item
			 WHERE recipient_id = $1 AND type = 'spec_approved' AND details->>'spec_id' = $2`,
			testUserID, specID).Scan(&body, &changelog)
		if changelog != "initial draft" {
			t.Fatalf("details changelog = %q, want %q", changelog, "initial draft")
		}
		if body != "initial draft" {
			t.Fatalf("body must carry the changelog, got %q", body)
		}
	})
}

// TestSpecApprovedVersionPublishNotifies covers the other approval signal:
// publishing a new snapshot onto an already-approved spec changes its
// effective content, so watchers are notified again.
func TestSpecApprovedVersionPublishNotifies(t *testing.T) {
	specID, _, approver := specApprovalFixture(t)
	testutil.Call(t, testHandler.TransitionSpecStatus,
		testutil.WithURLParams(newRequestAs(approver, "POST", "/api/specs/"+specID+"/transition", map[string]any{
			"to": "approved",
		}), "id", specID)).
		Want(http.StatusOK)

	before := specApprovedInboxCount(t, testUserID, specID)

	testutil.Call(t, testHandler.PublishSpecVersion,
		testutil.WithURLParams(newRequestAs(approver, "POST", "/api/specs/"+specID+"/versions", map[string]any{
			"content":   "the spec body v2",
			"changelog": "clarify rule three",
		}), "id", specID)).
		Want(http.StatusCreated)

	if got := specApprovedInboxCount(t, testUserID, specID); got != before+1 {
		t.Fatalf("publishing onto an approved spec must notify again: before=%d after=%d", before, got)
	}
}

// TestSpecDraftAndReviewDoNotNotify guards the noise floor: only the approved
// signal generates spec notifications, not drafts or in_review transitions,
// and not plain publishes on an unapproved spec.
func TestSpecDraftAndReviewDoNotNotify(t *testing.T) {
	var created SpecResponse
	testutil.Call(t, testHandler.CreateSpec,
		newRequest("POST", "/api/specs", map[string]any{"title": "Spec approval noise floor"})).
		Want(http.StatusCreated).JSON(&created)
	specID := created.ID

	if got := specApprovedInboxCount(t, testUserID, specID); got != 0 {
		t.Fatalf("create must not notify, got %d", got)
	}

	testutil.Call(t, testHandler.PublishSpecVersion,
		testutil.WithURLParams(newRequest("POST", "/api/specs/"+specID+"/versions", map[string]any{
			"content":   "body",
			"changelog": "first cut",
		}), "id", specID)).
		Want(http.StatusCreated)
	if got := specApprovedInboxCount(t, testUserID, specID); got != 0 {
		t.Fatalf("publish on a draft spec must not notify, got %d", got)
	}

	testutil.Call(t, testHandler.TransitionSpecStatus,
		testutil.WithURLParams(newRequest("POST", "/api/specs/"+specID+"/transition", map[string]any{
			"to": "in_review",
		}), "id", specID)).
		Want(http.StatusOK)
	if got := specApprovedInboxCount(t, testUserID, specID); got != 0 {
		t.Fatalf("in_review transition must not notify, got %d", got)
	}
}
