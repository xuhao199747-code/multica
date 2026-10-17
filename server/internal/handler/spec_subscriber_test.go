package handler

import (
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

// TestCreateSpecAutoSubscribesCreator covers the DAWE-7 rule that a spec's
// creator is subscribed inside the same transaction that creates the spec.
func TestCreateSpecAutoSubscribesCreator(t *testing.T) {
	var created SpecResponse
	testutil.Call(t, testHandler.CreateSpec,
		newRequest("POST", "/api/specs", map[string]any{"title": "Spec creator subscribes"})).
		Want(http.StatusCreated).JSON(&created)

	if got := dbfx.Count(t, `SELECT count(*) FROM spec_subscriber WHERE spec_id = $1 AND member_id = $2`, created.ID, testUserID); got != 1 {
		t.Fatalf("creator must be auto-subscribed, subscription rows = %d", got)
	}

	t.Run("creator shows up in the subscriber list", func(t *testing.T) {
		var out struct {
			Subscribers []SpecSubscriberResponse `json:"subscribers"`
			Total       int                      `json:"total"`
		}
		testutil.Call(t, testHandler.ListSpecSubscribers,
			testutil.WithURLParams(newRequest("GET", "/api/specs/"+created.ID+"/subscribers", nil), "id", created.ID)).
			Want(http.StatusOK).JSON(&out)
		if out.Total != 1 || len(out.Subscribers) != 1 || out.Subscribers[0].UserID != testUserID {
			t.Fatalf("ListSpecSubscribers: expected only the creator, got %+v", out)
		}
	})
}

// TestSpecSubscribeUnsubscribe covers manual subscribe/unsubscribe of another
// workspace member and the membership gate.
func TestSpecSubscribeUnsubscribe(t *testing.T) {
	specID := dbfx.Spec(t, "Spec subscribe fixture")
	otherUser := dbfx.User(t, "spec subscribe user", "spec-subscribe@example.com")
	dbfx.Member(t, testWorkspaceID, otherUser, "member")

	t.Run("subscribe another member by user_id", func(t *testing.T) {
		testutil.Call(t, testHandler.SubscribeToSpec,
			testutil.WithURLParams(newRequest("POST", "/api/specs/"+specID+"/subscribe", map[string]any{
				"user_id":   otherUser,
				"user_type": "member",
			}), "id", specID)).
			Want(http.StatusOK)
		if got := dbfx.Count(t, `SELECT count(*) FROM spec_subscriber WHERE spec_id = $1 AND member_id = $2`, specID, otherUser); got != 1 {
			t.Fatalf("expected subscription row for %s, got %d", otherUser, got)
		}
	})

	t.Run("re-subscribe is idempotent", func(t *testing.T) {
		testutil.Call(t, testHandler.SubscribeToSpec,
			testutil.WithURLParams(newRequest("POST", "/api/specs/"+specID+"/subscribe", map[string]any{
				"user_id":   otherUser,
				"user_type": "member",
			}), "id", specID)).
			Want(http.StatusOK)
		if got := dbfx.Count(t, `SELECT count(*) FROM spec_subscriber WHERE spec_id = $1 AND member_id = $2`, specID, otherUser); got != 1 {
			t.Fatalf("re-subscribe must not duplicate the row, got %d", got)
		}
	})

	t.Run("unsubscribe removes the row and is a no-op when absent", func(t *testing.T) {
		testutil.Call(t, testHandler.UnsubscribeFromSpec,
			testutil.WithURLParams(newRequest("POST", "/api/specs/"+specID+"/unsubscribe", map[string]any{
				"user_id":   otherUser,
				"user_type": "member",
			}), "id", specID)).
			Want(http.StatusOK)
		if got := dbfx.Count(t, `SELECT count(*) FROM spec_subscriber WHERE spec_id = $1 AND member_id = $2`, specID, otherUser); got != 0 {
			t.Fatalf("unsubscribe must remove the row, got %d", got)
		}
		testutil.Call(t, testHandler.UnsubscribeFromSpec,
			testutil.WithURLParams(newRequest("POST", "/api/specs/"+specID+"/unsubscribe", map[string]any{
				"user_id":   otherUser,
				"user_type": "member",
			}), "id", specID)).
			Want(http.StatusOK)
	})

	t.Run("caller subscribes itself by default", func(t *testing.T) {
		thirdUser := dbfx.User(t, "spec subscribe self user", "spec-subscribe-self@example.com")
		dbfx.Member(t, testWorkspaceID, thirdUser, "member")
		testutil.Call(t, testHandler.SubscribeToSpec,
			testutil.WithURLParams(newRequestAs(thirdUser, "POST", "/api/specs/"+specID+"/subscribe", nil), "id", specID)).
			Want(http.StatusOK)
		if got := dbfx.Count(t, `SELECT count(*) FROM spec_subscriber WHERE spec_id = $1 AND member_id = $2`, specID, thirdUser); got != 1 {
			t.Fatalf("self-subscribe must insert a row, got %d", got)
		}
	})

	t.Run("foreign workspace member is rejected", func(t *testing.T) {
		foreignWs := dbfx.Workspace(t, "spec-sub-foreign-ws", "spec-sub-foreign-ws")
		foreignUser := dbfx.User(t, "spec subscribe foreign user", "spec-subscribe-foreign@example.com")
		dbfx.Member(t, foreignWs, foreignUser, "owner")
		testutil.Call(t, testHandler.SubscribeToSpec,
			testutil.WithURLParams(newRequest("POST", "/api/specs/"+specID+"/subscribe", map[string]any{
				"user_id":   foreignUser,
				"user_type": "member",
			}), "id", specID)).
			Want(http.StatusForbidden)
	})
}
