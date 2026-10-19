package service

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// dispatchedReviewRun dispatches one instant review run and returns its task id.
func dispatchedReviewRun(t *testing.T) (principalFixture, pgtype.UUID, string) {
	t.Helper()
	f, _, issue, agent := wakeFixture(t)
	f.Exec(t, `UPDATE agent SET review_config=jsonb_build_object('recipient_id',$2::text,'scope','workspace','instant_enabled',true) WHERE id=$1`, agent, f.UserID)
	f.Cleanup(t, `DELETE FROM review_agent_event WHERE agent_id=$1`, agent)
	f.Cleanup(t, `DELETE FROM inbox_item WHERE recipient_id=$1`, f.UserID)
	wakeSetStatus(t, f, issue, "in_review")
	dispatcher := ReviewAgentService{Tasks: f.svc.TaskSvc}
	if err := dispatcher.TickWorkspace(context.Background(), parseTestUUID(t, f.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	var taskID string
	if err := f.Pool.QueryRow(context.Background(), `SELECT id::text FROM agent_task_queue WHERE agent_id=$1 AND issue_id=$2 AND context->>'review_agent'='true'`, agent, issue).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	// CreateWakeupTask lands queued; the daemon claim flips it to running
	// before any terminal callback, so simulate the claimed state here.
	f.Exec(t, `UPDATE agent_task_queue SET status='running' WHERE id=$1`, taskID)
	return f, issue, taskID
}

func TestReviewAgentResultDeliveredOnCompletion(t *testing.T) {
	f, _, taskID := dispatchedReviewRun(t)
	ctx := context.Background()
	_, err := f.svc.TaskSvc.CompleteTask(ctx, parseTestUUID(t, taskID), []byte(`{"output":"PR #7 head abc1234 passes 12/12 checks; missing deploy evidence"}`), "", "", "", false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	var body string
	if err := f.Pool.QueryRow(ctx, `SELECT body FROM inbox_item WHERE recipient_id=$1 AND type='review_agent_result'`, f.UserID).Scan(&body); err != nil {
		t.Fatalf("result notification missing: %v", err)
	}
	if body == "" {
		t.Fatal("result notification has empty body")
	}
	if n := f.Count(t, `SELECT count(*) FROM inbox_item WHERE recipient_id=$1 AND type='review_agent_result'`, f.UserID); n != 1 {
		t.Fatalf("recipient got %d result notifications", n)
	}
}

func TestReviewAgentResultDeliveredOnFailure(t *testing.T) {
	f, _, taskID := dispatchedReviewRun(t)
	ctx := context.Background()
	// "manual" is not in the auto-retry set, so this failure is terminal.
	_, err := f.svc.TaskSvc.FailTask(ctx, parseTestUUID(t, taskID), "provider crashed mid-review", "", "", "", "manual", false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	var severity, body string
	if err := f.Pool.QueryRow(ctx, `SELECT severity,body FROM inbox_item WHERE recipient_id=$1 AND type='review_agent_result'`, f.UserID).Scan(&severity, &body); err != nil {
		t.Fatalf("failure notification missing: %v", err)
	}
	if severity != "attention" {
		t.Fatalf("failure severity = %s", severity)
	}
	if body == "" {
		t.Fatal("failure notification has empty body")
	}
}

func TestReviewAgentFailureWithPendingRetryDefersResult(t *testing.T) {
	f, _, taskID := dispatchedReviewRun(t)
	ctx := context.Background()
	// runtime_offline is auto-retryable: a retry child owns the outcome, so
	// the recipient must not get a failure notice for the superseded attempt.
	_, err := f.svc.TaskSvc.FailTask(ctx, parseTestUUID(t, taskID), "runtime went offline", "", "", "", "runtime_offline", false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM inbox_item WHERE recipient_id=$1 AND type='review_agent_result'`, f.UserID); n != 0 {
		t.Fatalf("retried failure sent %d premature notifications", n)
	}
	if n := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE id=$1 AND status='failed'`, taskID); n != 1 {
		t.Fatalf("parent task not failed: %d", n)
	}
}

func TestReviewAgentDuplicateCompletionNotifiesOnce(t *testing.T) {
	f, _, taskID := dispatchedReviewRun(t)
	ctx := context.Background()
	result := []byte(`{"output":"all checks green"}`)
	if _, err := f.svc.TaskSvc.CompleteTask(ctx, parseTestUUID(t, taskID), result, "", "", "", false, "", ""); err != nil {
		t.Fatal(err)
	}
	// A terminal-callback replay hits the already-finalized path: idempotent
	// success, no second notification.
	if _, err := f.svc.TaskSvc.CompleteTask(ctx, parseTestUUID(t, taskID), result, "", "", "", false, "", ""); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM inbox_item WHERE recipient_id=$1 AND type='review_agent_result'`, f.UserID); n != 1 {
		t.Fatalf("duplicate completion sent %d notifications", n)
	}
}

func TestReviewAgentOrdinaryTaskSendsNoResultNotification(t *testing.T) {
	f, _, _, agent := wakeFixture(t)
	ctx := context.Background()
	runtime := f.Runtime(t, "ordinary result runtime")
	f.Exec(t, `UPDATE agent SET runtime_id=$2 WHERE id=$1`, agent, runtime)
	issue := f.Issue(t, "ordinary run")
	task := f.Task(t, agent, testutil.Cols{"issue_id": issue, "runtime_id": runtime, "status": "running"})
	f.Cleanup(t, `DELETE FROM agent_task_queue WHERE id=$1`, task)
	f.Cleanup(t, `DELETE FROM inbox_item WHERE recipient_id=$1`, f.UserID)
	if _, err := f.svc.TaskSvc.CompleteTask(ctx, parseTestUUID(t, task), []byte(`{"output":"done"}`), "", "", "", false, "", ""); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM inbox_item WHERE recipient_id=$1 AND type='review_agent_result'`, f.UserID); n != 0 {
		t.Fatalf("ordinary task sent %d review notifications", n)
	}
}
