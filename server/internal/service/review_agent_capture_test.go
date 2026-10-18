package service

import (
	"context"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestReviewAgentCapturesCommittedChanges(t *testing.T) {
	f, _, issue, agent := wakeFixture(t)
	ctx := context.Background()
	f.Exec(t, `UPDATE agent SET review_config=jsonb_build_object('recipient_id',$2::text,'scope','workspace','instant_enabled',true) WHERE id=$1`, agent, f.UserID)
	f.Cleanup(t, `DELETE FROM review_agent_event WHERE agent_id=$1`, agent)
	count := func() int { return f.Count(t, `SELECT count(*) FROM review_agent_event WHERE agent_id=$1`, agent) }
	tx, err := f.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE issue SET status='in_review' WHERE id=$1`, issue); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 0 {
		t.Fatalf("rollback left %d events", n)
	}
	wakeSetStatus(t, f, issue, "in_review")
	if n := count(); n != 1 {
		t.Fatalf("review transition produced %d events", n)
	}
	wakeSetStatus(t, f, issue, "done")
	if n := count(); n != 2 {
		t.Fatalf("review completion produced %d events", n)
	}
}

func TestReviewAgentCapturesNewIssueAlreadyInReview(t *testing.T) {
	f, _, _, agent := wakeFixture(t)
	f.Exec(t, `UPDATE agent SET review_config=jsonb_build_object('recipient_id',$2::text,'scope','workspace','instant_enabled',true) WHERE id=$1`, agent, f.UserID)
	f.Cleanup(t, `DELETE FROM review_agent_event WHERE agent_id=$1`, agent)
	issue := f.Issue(t, "new review", testutil.Cols{"status": "in_review"})
	if n := f.Count(t, `SELECT count(*) FROM review_agent_event WHERE agent_id=$1 AND issue_id=$2 AND event_type='review.started'`, agent, issue); n != 1 {
		t.Fatalf("new issue produced %d events", n)
	}
}

func TestReviewAgentEventsRemovedWithIssue(t *testing.T) {
	f, _, issue, agent := wakeFixture(t)
	f.Exec(t, `UPDATE agent SET review_config=jsonb_build_object('recipient_id',$2::text,'scope','workspace','instant_enabled',true) WHERE id=$1`, agent, f.UserID)
	f.Cleanup(t, `DELETE FROM review_agent_event WHERE agent_id=$1`, agent)
	wakeSetStatus(t, f, issue, "in_review")
	if err := f.q.DeleteIssue(context.Background(), db.DeleteIssueParams{ID: issue, WorkspaceID: parseTestUUID(t, f.WorkspaceID)}); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM review_agent_event WHERE issue_id=$1`, issue); n != 0 {
		t.Fatalf("deleted issue left %d events", n)
	}
}

func TestReviewAgentCapturesFailuresWithinSelectedProject(t *testing.T) {
	f, _, issue, reviewer := wakeFixture(t)
	project := f.Project(t, "review scope")
	f.Exec(t, `UPDATE issue SET project_id=$2 WHERE id=$1`, issue, project)
	f.Exec(t, `UPDATE agent SET review_config=jsonb_build_object('recipient_id',$2::text,'scope','projects','project_ids',jsonb_build_array($3::text),'instant_enabled',true) WHERE id=$1`, reviewer, f.UserID, project)
	f.Cleanup(t, `DELETE FROM review_agent_event WHERE agent_id=$1`, reviewer)
	runtime := f.Runtime(t, "source runtime")
	worker := f.Agent(t, "worker", runtime)
	task := f.Task(t, worker, testutil.Cols{"issue_id": util.UUIDToString(issue), "runtime_id": runtime, "status": "running"})
	f.Exec(t, `UPDATE agent_task_queue SET status='failed' WHERE id=$1`, task)
	if n := f.Count(t, `SELECT count(*) FROM review_agent_event WHERE agent_id=$1 AND event_type='task.failed' AND source_task_id=$2`, reviewer, task); n != 1 {
		t.Fatalf("failure produced %d events", n)
	}
	otherIssue := f.Issue(t, "outside scope")
	f.Exec(t, `UPDATE issue SET status='in_review' WHERE id=$1`, otherIssue)
	if n := f.Count(t, `SELECT count(*) FROM review_agent_event WHERE agent_id=$1 AND issue_id=$2`, reviewer, otherIssue); n != 0 {
		t.Fatalf("out-of-scope issue produced %d events", n)
	}
}

func TestReviewAgentSuppressesOwnChanges(t *testing.T) {
	f, _, issue, agent := wakeFixture(t)
	ctx := context.Background()
	f.Exec(t, `UPDATE agent SET review_config=jsonb_build_object('recipient_id',$2::text,'scope','workspace','instant_enabled',true) WHERE id=$1`, agent, f.UserID)
	f.Cleanup(t, `DELETE FROM review_agent_event WHERE agent_id=$1`, agent)
	tx, err := f.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('multica.actor_type','agent',true), set_config('multica.actor_id',$1,true)`, agent); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE issue SET status='in_review' WHERE id=$1`, issue); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM review_agent_event WHERE agent_id=$1`, agent); n != 0 {
		t.Fatalf("own change produced %d events", n)
	}
}
