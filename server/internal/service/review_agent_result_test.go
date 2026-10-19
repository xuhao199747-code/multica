package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
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

func TestManualReviewQueuesActionableIssues(t *testing.T) {
	f, _, _, agent := wakeFixture(t)
	ctx := context.Background()
	target := f.Issue(t, "review target")
	f.Exec(t, `UPDATE issue SET status='in_review' WHERE id=$1`, target)
	worker := f.privateAgentOwnedBy(t, f.UserID, "manual-review-worker")
	f.Exec(t, `UPDATE issue SET assignee_type='agent',assignee_id=$2 WHERE id=$1`, target, worker)
	var workerRuntime string
	if err := f.Pool.QueryRow(ctx, `SELECT runtime_id::text FROM agent WHERE id=$1`, worker).Scan(&workerRuntime); err != nil {
		t.Fatal(err)
	}
	f.Insert(t, "agent_task_queue", testutil.Cols{"agent_id": worker, "runtime_id": workerRuntime, "issue_id": target, "status": "completed", "priority": 0})
	f.Cleanup(t, `DELETE FROM review_repair WHERE issue_id=$1`, target)
	f.Exec(t, `UPDATE agent SET review_config=jsonb_build_object('recipient_id',$2::text,'scope','workspace','instant_enabled',true) WHERE id=$1`, agent, f.UserID)
	issue := f.Issue(t, "review summary")
	comment := f.Comment(t, issue, "Please review and fix the findings")
	var runtime string
	if err := f.Pool.QueryRow(ctx, `SELECT runtime_id::text FROM agent WHERE id=$1`, agent).Scan(&runtime); err != nil {
		t.Fatal(err)
	}
	task := f.Task(t, agent, testutil.Cols{"issue_id": issue, "runtime_id": runtime, "status": "running", "trigger_comment_id": comment, "originator_user_id": f.UserID, "accountable_user_id": f.UserID})
	f.Cleanup(t, `DELETE FROM review_agent_event WHERE agent_id=$1`, agent)
	f.Cleanup(t, `DELETE FROM agent_task_queue WHERE id=$1`, task)
	result := []byte(fmt.Sprintf(`{"output":%q}`, fmt.Sprintf(`{"reviews":[{"issue_id":%q,"verdict":"needs_changes","findings":"fix test"}]}`, target)))
	if _, err := f.svc.TaskSvc.CompleteTask(ctx, parseTestUUID(t, task), result, "", "", "", false, "", ""); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM review_agent_event WHERE source_task_id=$1 AND issue_id=$2 AND processed_at IS NULL`, task, target); n != 1 {
		t.Fatalf("expected one review event, got %d", n)
	}
	svc := ReviewAgentService{Tasks: f.svc.TaskSvc}
	if err := svc.TickWorkspace(ctx, parseTestUUID(t, f.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE agent_id=$1 AND issue_id=$2 AND context->>'review_agent'='true' AND status='queued'`, agent, target); n != 1 {
		t.Fatalf("expected targeted review task, got %d", n)
	}
	var note string
	if err := f.Pool.QueryRow(ctx, `SELECT handoff_note FROM agent_task_queue WHERE agent_id=$1 AND issue_id=$2 AND context->>'review_agent'='true'`, agent, target).Scan(&note); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "fix test") {
		t.Fatalf("review did not receive original findings: %s", note)
	}
	var reviewTask string
	if err := f.Pool.QueryRow(ctx, `SELECT id::text FROM agent_task_queue WHERE agent_id=$1 AND issue_id=$2 AND context->>'review_agent'='true'`, agent, target).Scan(&reviewTask); err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `UPDATE agent_task_queue SET status='running' WHERE id=$1`, reviewTask)
	if _, err := f.svc.TaskSvc.CompleteTask(ctx, parseTestUUID(t, reviewTask), []byte(`{"output":"{\"verdict\":\"needs_changes\",\"findings\":\"fix test\"}"}`), "", "", "", false, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.TickRepairs(ctx, parseTestUUID(t, f.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	var repairTask string
	if err := f.Pool.QueryRow(ctx, `SELECT id::text FROM agent_task_queue WHERE issue_id=$1 AND agent_id=$2 AND context->>'review_repair'='true'`, target, worker).Scan(&repairTask); err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `UPDATE agent_task_queue SET status='dispatched' WHERE id=$1`, repairTask)
	if _, err := f.svc.TaskSvc.StartTask(ctx, parseTestUUID(t, repairTask)); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM issue WHERE id=$1 AND status='in_progress'`, target); n != 1 {
		t.Fatal("repair start did not update status")
	}
	if _, err := f.svc.TaskSvc.CompleteTask(ctx, parseTestUUID(t, repairTask), []byte(`{"output":"fixed"}`), "", "", "", false, "", ""); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM issue WHERE id=$1 AND status='in_review'`, target); n != 1 {
		t.Fatal("repair completion did not update status")
	}
	if err := svc.TickRepairs(ctx, parseTestUUID(t, f.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1 AND context->>'review_agent'='true' AND context->>'review_repair_round'='1'`, target); n != 1 {
		t.Fatalf("expected one re-review, got %d", n)
	}
}

func TestManualReviewAcceptsReportInPostedComment(t *testing.T) {
	f, _, _, agent := wakeFixture(t)
	ctx := context.Background()
	target := f.Issue(t, "review target")
	f.Exec(t, `UPDATE issue SET status='in_review' WHERE id=$1`, target)
	f.Exec(t, `UPDATE agent SET review_config=jsonb_build_object('recipient_id',$2::text,'scope','workspace','instant_enabled',true) WHERE id=$1`, agent, f.UserID)
	issue := f.Issue(t, "review summary")
	trigger := f.Comment(t, issue, "Review and fix")
	var runtime string
	if err := f.Pool.QueryRow(ctx, `SELECT runtime_id::text FROM agent WHERE id=$1`, agent).Scan(&runtime); err != nil {
		t.Fatal(err)
	}
	task := f.Task(t, agent, testutil.Cols{"issue_id": issue, "runtime_id": runtime, "status": "running", "trigger_comment_id": trigger, "originator_user_id": f.UserID, "accountable_user_id": f.UserID})
	f.Comment(t, issue, fmt.Sprintf(`Report <!-- multica-review {"reviews":[{"issue_id":%q,"verdict":"needs_changes","findings":"fix it"}]} -->`, target), testutil.Cols{"author_type": "agent", "author_id": agent, "source_task_id": task})
	f.Cleanup(t, `DELETE FROM review_agent_event WHERE agent_id=$1`, agent)
	f.Cleanup(t, `DELETE FROM agent_task_queue WHERE id=$1`, task)
	if _, err := f.svc.TaskSvc.CompleteTask(ctx, parseTestUUID(t, task), []byte(`{"output":""}`), "", "", "", false, "", ""); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM review_agent_event WHERE source_task_id=$1 AND issue_id=$2`, task, target); n != 1 {
		t.Fatalf("expected one event from posted comment, got %d", n)
	}
	svc := ReviewAgentService{Tasks: f.svc.TaskSvc}
	if err := svc.TickWorkspace(ctx, parseTestUUID(t, f.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	var note string
	if err := f.Pool.QueryRow(ctx, `SELECT handoff_note FROM agent_task_queue WHERE agent_id=$1 AND issue_id=$2 AND context->>'review_agent'='true'`, agent, target).Scan(&note); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "fix it") {
		t.Fatalf("review did not receive posted findings: %s", note)
	}
}

func TestManualReviewWithoutStructuredReportNotifiesRecipient(t *testing.T) {
	f, _, _, agent := wakeFixture(t)
	ctx := context.Background()
	f.Exec(t, `UPDATE agent SET review_config=jsonb_build_object('recipient_id',$2::text,'scope','workspace','instant_enabled',true) WHERE id=$1`, agent, f.UserID)
	issue := f.Issue(t, "review summary")
	trigger := f.Comment(t, issue, "Review and fix")
	var runtime string
	if err := f.Pool.QueryRow(ctx, `SELECT runtime_id::text FROM agent WHERE id=$1`, agent).Scan(&runtime); err != nil {
		t.Fatal(err)
	}
	task := f.Task(t, agent, testutil.Cols{"issue_id": issue, "runtime_id": runtime, "status": "running", "trigger_comment_id": trigger, "originator_user_id": f.UserID, "accountable_user_id": f.UserID})
	f.Cleanup(t, `DELETE FROM inbox_item WHERE recipient_id=$1`, f.UserID)
	f.Cleanup(t, `DELETE FROM agent_task_queue WHERE id=$1`, task)
	if _, err := f.svc.TaskSvc.CompleteTask(ctx, parseTestUUID(t, task), []byte(`{"output":"Needs fixes"}`), "", "", "", false, "", ""); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM inbox_item WHERE recipient_id=$1 AND issue_id=$2 AND severity='attention'`, f.UserID, issue); n != 1 {
		t.Fatalf("expected attention notification, got %d", n)
	}
}

func TestManualReviewDoesNotQueueBlockedIssue(t *testing.T) {
	f, _, _, agent := wakeFixture(t)
	ctx := context.Background()
	target := f.Issue(t, "blocked target")
	f.Exec(t, `UPDATE issue SET status='blocked' WHERE id=$1`, target)
	f.Exec(t, `UPDATE agent SET review_config=jsonb_build_object('recipient_id',$2::text,'scope','workspace','instant_enabled',true) WHERE id=$1`, agent, f.UserID)
	issue := f.Issue(t, "review summary")
	trigger := f.Comment(t, issue, "Review and fix")
	var runtime string
	if err := f.Pool.QueryRow(ctx, `SELECT runtime_id::text FROM agent WHERE id=$1`, agent).Scan(&runtime); err != nil {
		t.Fatal(err)
	}
	task := f.Task(t, agent, testutil.Cols{"issue_id": issue, "runtime_id": runtime, "status": "running", "trigger_comment_id": trigger, "originator_user_id": f.UserID, "accountable_user_id": f.UserID})
	f.Cleanup(t, `DELETE FROM inbox_item WHERE recipient_id=$1`, f.UserID)
	f.Cleanup(t, `DELETE FROM review_agent_event WHERE agent_id=$1`, agent)
	f.Cleanup(t, `DELETE FROM agent_task_queue WHERE id=$1`, task)
	output := fmt.Sprintf(`{"reviews":[{"issue_id":%q,"verdict":"needs_changes","findings":"fix"}]}`, target)
	result := []byte(fmt.Sprintf(`{"output":%q}`, output))
	if _, err := f.svc.TaskSvc.CompleteTask(ctx, parseTestUUID(t, task), result, "", "", "", false, "", ""); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM review_agent_event WHERE source_task_id=$1`, task); n != 0 {
		t.Fatalf("blocked issue queued %d times", n)
	}
	if n := f.Count(t, `SELECT count(*) FROM inbox_item WHERE recipient_id=$1 AND issue_id=$2 AND severity='attention'`, f.UserID, issue); n != 1 {
		t.Fatalf("expected attention notification, got %d", n)
	}
}

func TestActionableReviewCreatesRepairRequest(t *testing.T) {
	f, issue, taskID := dispatchedReviewRun(t)
	f.Cleanup(t, `DELETE FROM review_repair WHERE issue_id=$1`, issue)
	result := []byte(`{"output":"{\"verdict\":\"needs_changes\",\"findings\":\"Fix the failing test\"}"}`)
	if _, err := f.svc.TaskSvc.CompleteTask(context.Background(), parseTestUUID(t, taskID), result, "", "", "", false, "", ""); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM review_repair WHERE review_task_id=$1 AND status='pending'`, taskID); n != 1 {
		t.Fatalf("expected one pending repair, got %d", n)
	}
}

func TestReviewTaskRequestsStructuredVerdict(t *testing.T) {
	f, _, taskID := dispatchedReviewRun(t)
	var note string
	if err := f.Pool.QueryRow(context.Background(), `SELECT handoff_note FROM agent_task_queue WHERE id=$1`, taskID).Scan(&note); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, `needs_changes`) || !strings.Contains(note, `findings`) {
		t.Fatalf("review instructions do not request a structured verdict: %s", note)
	}
}

func TestRepairDispatcherRoutesToOriginalExecutor(t *testing.T) {
	f, issue, taskID := dispatchedReviewRun(t)
	f.Cleanup(t, `DELETE FROM review_repair WHERE issue_id=$1`, issue)
	worker := f.privateAgentOwnedBy(t, f.UserID, "repair-worker")
	f.Exec(t, `UPDATE issue SET assignee_type='agent', assignee_id=$2 WHERE id=$1`, issue, worker)
	var runtime string
	if err := f.Pool.QueryRow(context.Background(), `SELECT runtime_id::text FROM agent WHERE id=$1`, worker).Scan(&runtime); err != nil {
		t.Fatal(err)
	}
	f.Insert(t, "agent_task_queue", testutil.Cols{"agent_id": worker, "runtime_id": runtime, "issue_id": issue, "status": "completed", "priority": 0})
	result := []byte(`{"output":"{\"verdict\":\"needs_changes\",\"findings\":\"Fix the failing test\"}"}`)
	if _, err := f.svc.TaskSvc.CompleteTask(context.Background(), parseTestUUID(t, taskID), result, "", "", "", false, "", ""); err != nil {
		t.Fatal(err)
	}
	svc := ReviewAgentService{Tasks: f.svc.TaskSvc}
	workspaceID := parseTestUUID(t, f.WorkspaceID)
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- svc.TickRepairs(context.Background(), workspaceID) }()
	}
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if n := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE agent_id=$1 AND issue_id=$2 AND context->>'review_repair'='true'`, worker, issue); n != 1 {
		t.Fatalf("expected one repair task for original executor, got %d", n)
	}
	if err := svc.TickRepairs(context.Background(), parseTestUUID(t, f.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE agent_id=$1 AND issue_id=$2 AND context->>'review_repair'='true'`, worker, issue); n != 1 {
		t.Fatalf("duplicate repair tasks: %d", n)
	}
	var repairID string
	if err := f.Pool.QueryRow(context.Background(), `SELECT id::text FROM agent_task_queue WHERE agent_id=$1 AND issue_id=$2 AND context->>'review_repair'='true'`, worker, issue).Scan(&repairID); err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `UPDATE agent_task_queue SET status='dispatched' WHERE id=$1`, repairID)
	if _, err := f.svc.TaskSvc.StartTask(context.Background(), parseTestUUID(t, repairID)); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM issue WHERE id=$1 AND status='in_progress'`, issue); n != 1 {
		t.Fatalf("repair did not move issue to in_progress: %d", n)
	}
	if _, err := f.svc.TaskSvc.CompleteTask(context.Background(), parseTestUUID(t, repairID), []byte(`{"output":"fixed and tested"}`), "", "", "", false, "", ""); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM issue WHERE id=$1 AND status='in_review'`, issue); n != 1 {
		t.Fatalf("completed repair did not return issue to in_review: %d", n)
	}
	if n := f.Count(t, `SELECT count(*) FROM review_agent_event WHERE issue_id=$1 AND processed_at IS NULL`, issue); n != 0 {
		t.Fatalf("automatic status transitions triggered %d redundant reviews", n)
	}
	if err := svc.TickRepairs(context.Background(), parseTestUUID(t, f.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1 AND context->>'review_repair_round'='1' AND context->>'review_agent'='true'`, issue); n != 1 {
		t.Fatalf("expected one rereview, got %d", n)
	}
	var rereviewID string
	if err := f.Pool.QueryRow(context.Background(), `SELECT id::text FROM agent_task_queue WHERE issue_id=$1 AND context->>'review_repair_round'='1' AND context->>'review_agent'='true'`, issue).Scan(&rereviewID); err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `UPDATE agent_task_queue SET status='running' WHERE id=$1`, rereviewID)
	if _, err := f.svc.TaskSvc.CompleteTask(context.Background(), parseTestUUID(t, rereviewID), []byte(`{"output":"{\"verdict\":\"pass\"}"}`), "", "", "", false, "", ""); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM review_repair WHERE issue_id=$1 AND status='passed'`, issue); n != 1 {
		t.Fatalf("expected passed repair, got %d", n)
	}
}

func TestRepairWithoutAssignedExecutorEscalates(t *testing.T) {
	f, issue, taskID := dispatchedReviewRun(t)
	f.Cleanup(t, `DELETE FROM review_repair WHERE issue_id=$1`, issue)
	result := []byte(`{"output":"{\"verdict\":\"needs_changes\",\"findings\":\"Fix it\"}"}`)
	if _, err := f.svc.TaskSvc.CompleteTask(context.Background(), parseTestUUID(t, taskID), result, "", "", "", false, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := (&ReviewAgentService{Tasks: f.svc.TaskSvc}).TickRepairs(context.Background(), parseTestUUID(t, f.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM review_repair WHERE issue_id=$1 AND status='blocked'`, issue); n != 1 {
		t.Fatalf("expected blocked repair, got %d", n)
	}
	if n := f.Count(t, `SELECT count(*) FROM inbox_item WHERE issue_id=$1 AND type='review_repair_blocked'`, issue); n != 1 {
		t.Fatalf("expected escalation notification, got %d", n)
	}
}

func TestRepairStopsWhenReviewDisabled(t *testing.T) {
	f, issue, taskID := dispatchedReviewRun(t)
	f.Cleanup(t, `DELETE FROM review_repair WHERE issue_id=$1`, issue)
	if _, err := f.svc.TaskSvc.CompleteTask(context.Background(), parseTestUUID(t, taskID), []byte(`{"output":"{\"verdict\":\"needs_changes\",\"findings\":\"Fix it\"}"}`), "", "", "", false, "", ""); err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `UPDATE agent SET review_config=review_config || '{"instant_enabled":false}'::jsonb WHERE id=(SELECT agent_id FROM agent_task_queue WHERE id=$1)`, taskID)
	if err := (&ReviewAgentService{Tasks: f.svc.TaskSvc}).TickRepairs(context.Background(), parseTestUUID(t, f.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM review_repair WHERE issue_id=$1 AND status='blocked'`, issue); n != 1 {
		t.Fatalf("expected blocked repair when review is disabled, got %d", n)
	}
	if n := f.Count(t, `SELECT count(*) FROM inbox_item WHERE issue_id=$1 AND type='review_repair_blocked'`, issue); n != 1 {
		t.Fatalf("expected notification, got %d", n)
	}
}

func TestRepairPreservesHumanStatusChange(t *testing.T) {
	f, issue, taskID := dispatchedReviewRun(t)
	f.Cleanup(t, `DELETE FROM review_repair WHERE issue_id=$1`, issue)
	worker := f.privateAgentOwnedBy(t, f.UserID, "repair-manual-status")
	f.Exec(t, `UPDATE issue SET assignee_type='agent', assignee_id=$2 WHERE id=$1`, issue, worker)
	var runtime string
	if err := f.Pool.QueryRow(context.Background(), `SELECT runtime_id::text FROM agent WHERE id=$1`, worker).Scan(&runtime); err != nil {
		t.Fatal(err)
	}
	f.Insert(t, "agent_task_queue", testutil.Cols{"agent_id": worker, "runtime_id": runtime, "issue_id": issue, "status": "completed", "priority": 0})
	if _, err := f.svc.TaskSvc.CompleteTask(context.Background(), parseTestUUID(t, taskID), []byte(`{"output":"{\"verdict\":\"needs_changes\",\"findings\":\"Fix it\"}"}`), "", "", "", false, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := (&ReviewAgentService{Tasks: f.svc.TaskSvc}).TickRepairs(context.Background(), parseTestUUID(t, f.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	var repairID string
	if err := f.Pool.QueryRow(context.Background(), `SELECT id::text FROM agent_task_queue WHERE agent_id=$1 AND issue_id=$2 AND context->>'review_repair'='true'`, worker, issue).Scan(&repairID); err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `UPDATE agent_task_queue SET status='dispatched' WHERE id=$1`, repairID)
	if _, err := f.svc.TaskSvc.StartTask(context.Background(), parseTestUUID(t, repairID)); err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `UPDATE issue SET status='blocked', revision=revision+1 WHERE id=$1`, issue)
	if _, err := f.svc.TaskSvc.CompleteTask(context.Background(), parseTestUUID(t, repairID), []byte(`{"output":"fixed"}`), "", "", "", false, "", ""); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM issue WHERE id=$1 AND status='blocked'`, issue); n != 1 {
		t.Fatalf("human status change was overwritten: %d", n)
	}
}

func TestRepairFailureNotifiesRecipient(t *testing.T) {
	f, issue, taskID := dispatchedReviewRun(t)
	f.Cleanup(t, `DELETE FROM review_repair WHERE issue_id=$1`, issue)
	worker := f.privateAgentOwnedBy(t, f.UserID, "repair-fail")
	f.Exec(t, `UPDATE issue SET assignee_type='agent', assignee_id=$2 WHERE id=$1`, issue, worker)
	var runtime string
	if err := f.Pool.QueryRow(context.Background(), `SELECT runtime_id::text FROM agent WHERE id=$1`, worker).Scan(&runtime); err != nil {
		t.Fatal(err)
	}
	f.Insert(t, "agent_task_queue", testutil.Cols{"agent_id": worker, "runtime_id": runtime, "issue_id": issue, "status": "completed", "priority": 0})
	if _, err := f.svc.TaskSvc.CompleteTask(context.Background(), parseTestUUID(t, taskID), []byte(`{"output":"{\"verdict\":\"needs_changes\",\"findings\":\"Fix it\"}"}`), "", "", "", false, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := (&ReviewAgentService{Tasks: f.svc.TaskSvc}).TickRepairs(context.Background(), parseTestUUID(t, f.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	var repairID string
	if err := f.Pool.QueryRow(context.Background(), `SELECT id::text FROM agent_task_queue WHERE agent_id=$1 AND issue_id=$2 AND context->>'review_repair'='true'`, worker, issue).Scan(&repairID); err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `UPDATE agent_task_queue SET status='running' WHERE id=$1`, repairID)
	if _, err := f.svc.TaskSvc.FailTask(context.Background(), parseTestUUID(t, repairID), "cannot fix", "", "", "", "manual", false, "", ""); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM review_repair WHERE issue_id=$1 AND status='failed'`, issue); n != 1 {
		t.Fatalf("expected failed repair, got %d", n)
	}
	if n := f.Count(t, `SELECT count(*) FROM inbox_item WHERE issue_id=$1 AND type='review_repair_failed'`, issue); n != 1 {
		t.Fatalf("expected failure notification, got %d", n)
	}
}

func TestRepairSweeperFailureNotifiesRecipient(t *testing.T) {
	f, issue, taskID := dispatchedReviewRun(t)
	f.Cleanup(t, `DELETE FROM review_repair WHERE issue_id=$1`, issue)
	worker := f.privateAgentOwnedBy(t, f.UserID, "repair-sweeper")
	f.Exec(t, `UPDATE issue SET assignee_type='agent', assignee_id=$2 WHERE id=$1`, issue, worker)
	var runtime string
	if err := f.Pool.QueryRow(context.Background(), `SELECT runtime_id::text FROM agent WHERE id=$1`, worker).Scan(&runtime); err != nil {
		t.Fatal(err)
	}
	f.Insert(t, "agent_task_queue", testutil.Cols{"agent_id": worker, "runtime_id": runtime, "issue_id": issue, "status": "completed", "priority": 0})
	if _, err := f.svc.TaskSvc.CompleteTask(context.Background(), parseTestUUID(t, taskID), []byte(`{"output":"{\"verdict\":\"needs_changes\",\"findings\":\"Fix it\"}"}`), "", "", "", false, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := (&ReviewAgentService{Tasks: f.svc.TaskSvc}).TickRepairs(context.Background(), parseTestUUID(t, f.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	var repairID string
	if err := f.Pool.QueryRow(context.Background(), `SELECT id::text FROM agent_task_queue WHERE agent_id=$1 AND issue_id=$2 AND context->>'review_repair'='true'`, worker, issue).Scan(&repairID); err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `UPDATE agent_task_queue SET status='failed', failure_reason='manual', error='runtime stopped' WHERE id=$1`, repairID)
	task, err := f.svc.TaskSvc.Queries.GetAgentTask(context.Background(), parseTestUUID(t, repairID))
	if err != nil {
		t.Fatal(err)
	}
	f.svc.TaskSvc.HandleFailedTasks(context.Background(), []db.AgentTaskQueue{task})
	if n := f.Count(t, `SELECT count(*) FROM review_repair WHERE issue_id=$1 AND status='failed'`, issue); n != 1 {
		t.Fatalf("expected failed repair, got %d", n)
	}
	if n := f.Count(t, `SELECT count(*) FROM inbox_item WHERE issue_id=$1 AND type='review_repair_failed'`, issue); n != 1 {
		t.Fatalf("expected failure notification, got %d", n)
	}
}

func TestRepairRoundLimitNotifiesRecipient(t *testing.T) {
	f, issue, taskID := dispatchedReviewRun(t)
	f.Cleanup(t, `DELETE FROM review_repair WHERE issue_id=$1`, issue)
	f.Exec(t, `UPDATE agent_task_queue SET context=context || '{"review_repair_round":3}'::jsonb WHERE id=$1`, taskID)
	result := []byte(`{"output":"{\"verdict\":\"needs_changes\",\"findings\":\"Still failing\"}"}`)
	if _, err := f.svc.TaskSvc.CompleteTask(context.Background(), parseTestUUID(t, taskID), result, "", "", "", false, "", ""); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM review_repair WHERE issue_id=$1 AND status='pending'`, issue); n != 0 {
		t.Fatalf("unexpected repair after limit: %d", n)
	}
	if n := f.Count(t, `SELECT count(*) FROM inbox_item WHERE issue_id=$1 AND type='review_agent_result' AND severity='attention'`, issue); n != 1 {
		t.Fatalf("expected attention notification at limit, got %d", n)
	}
}

func TestRepairThreeRoundsEndInAttention(t *testing.T) {
	f, issue, taskID := dispatchedReviewRun(t)
	f.Cleanup(t, `DELETE FROM review_repair WHERE issue_id=$1`, issue)
	worker := f.privateAgentOwnedBy(t, f.UserID, "repair-three-rounds")
	f.Exec(t, `UPDATE issue SET assignee_type='agent', assignee_id=$2 WHERE id=$1`, issue, worker)
	var runtime string
	if err := f.Pool.QueryRow(context.Background(), `SELECT runtime_id::text FROM agent WHERE id=$1`, worker).Scan(&runtime); err != nil {
		t.Fatal(err)
	}
	f.Insert(t, "agent_task_queue", testutil.Cols{"agent_id": worker, "runtime_id": runtime, "issue_id": issue, "status": "completed", "priority": 0})
	verdict := []byte(`{"output":"{\"verdict\":\"needs_changes\",\"findings\":\"Still failing\"}"}`)
	if _, err := f.svc.TaskSvc.CompleteTask(context.Background(), parseTestUUID(t, taskID), verdict, "", "", "", false, "", ""); err != nil {
		t.Fatal(err)
	}
	svc := ReviewAgentService{Tasks: f.svc.TaskSvc}
	for round := 1; round <= 3; round++ {
		if err := svc.TickRepairs(context.Background(), parseTestUUID(t, f.WorkspaceID)); err != nil {
			t.Fatal(err)
		}
		var repairID string
		if err := f.Pool.QueryRow(context.Background(), `SELECT repair_task_id::text FROM review_repair WHERE issue_id=$1 AND round=$2`, issue, round).Scan(&repairID); err != nil {
			t.Fatal(err)
		}
		f.Exec(t, `UPDATE agent_task_queue SET status='running' WHERE id=$1`, repairID)
		if _, err := f.svc.TaskSvc.CompleteTask(context.Background(), parseTestUUID(t, repairID), []byte(`{"output":"fixed and tested"}`), "", "", "", false, "", ""); err != nil {
			t.Fatal(err)
		}
		if err := svc.TickRepairs(context.Background(), parseTestUUID(t, f.WorkspaceID)); err != nil {
			t.Fatal(err)
		}
		var rereviewID string
		if err := f.Pool.QueryRow(context.Background(), `SELECT rereview_task_id::text FROM review_repair WHERE issue_id=$1 AND round=$2`, issue, round).Scan(&rereviewID); err != nil {
			t.Fatal(err)
		}
		f.Exec(t, `UPDATE agent_task_queue SET status='running' WHERE id=$1`, rereviewID)
		if _, err := f.svc.TaskSvc.CompleteTask(context.Background(), parseTestUUID(t, rereviewID), verdict, "", "", "", false, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.Count(t, `SELECT count(*) FROM review_repair WHERE issue_id=$1`, issue); n != 3 {
		t.Fatalf("expected exactly three rounds, got %d", n)
	}
	if n := f.Count(t, `SELECT count(*) FROM inbox_item WHERE issue_id=$1 AND type='review_agent_result' AND severity='attention'`, issue); n != 1 {
		t.Fatalf("expected one escalation, got %d", n)
	}
}

func TestRepairRetryCompletionContinuesToRereview(t *testing.T) {
	f, issue, taskID := dispatchedReviewRun(t)
	f.Cleanup(t, `DELETE FROM review_repair WHERE issue_id=$1`, issue)
	worker := f.privateAgentOwnedBy(t, f.UserID, "repair-retry")
	f.Exec(t, `UPDATE issue SET assignee_type='agent', assignee_id=$2 WHERE id=$1`, issue, worker)
	var runtime string
	if err := f.Pool.QueryRow(context.Background(), `SELECT runtime_id::text FROM agent WHERE id=$1`, worker).Scan(&runtime); err != nil {
		t.Fatal(err)
	}
	f.Insert(t, "agent_task_queue", testutil.Cols{"agent_id": worker, "runtime_id": runtime, "issue_id": issue, "status": "completed", "priority": 0})
	if _, err := f.svc.TaskSvc.CompleteTask(context.Background(), parseTestUUID(t, taskID), []byte(`{"output":"{\"verdict\":\"needs_changes\",\"findings\":\"Fix it\"}"}`), "", "", "", false, "", ""); err != nil {
		t.Fatal(err)
	}
	svc := ReviewAgentService{Tasks: f.svc.TaskSvc}
	if err := svc.TickRepairs(context.Background(), parseTestUUID(t, f.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	var repairID string
	if err := f.Pool.QueryRow(context.Background(), `SELECT id::text FROM agent_task_queue WHERE agent_id=$1 AND context->>'review_repair'='true'`, worker).Scan(&repairID); err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `UPDATE agent_task_queue SET status='running' WHERE id=$1`, repairID)
	if _, err := f.svc.TaskSvc.FailTask(context.Background(), parseTestUUID(t, repairID), "offline", "", "", "", "runtime_offline", false, "", ""); err != nil {
		t.Fatal(err)
	}
	var retryID string
	if err := f.Pool.QueryRow(context.Background(), `SELECT id::text FROM agent_task_queue WHERE retry_of_task_id=$1`, repairID).Scan(&retryID); err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `UPDATE agent_task_queue SET status='running' WHERE id=$1`, retryID)
	if _, err := f.svc.TaskSvc.CompleteTask(context.Background(), parseTestUUID(t, retryID), []byte(`{"output":"fixed"}`), "", "", "", false, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.TickRepairs(context.Background(), parseTestUUID(t, f.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1 AND context->>'review_agent'='true' AND context->>'review_repair_round'='1'`, issue); n != 1 {
		t.Fatalf("retry did not trigger rereview: %d", n)
	}
}
