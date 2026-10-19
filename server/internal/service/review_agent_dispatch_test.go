package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
)

func TestReviewAgentDispatchOnceAcrossConcurrentWorkers(t *testing.T) {
	f, _, issue, agent := wakeFixture(t)
	f.Exec(t, `UPDATE agent SET review_config=jsonb_build_object('recipient_id',$2::text,'scope','workspace','instant_enabled',true) WHERE id=$1`, agent, f.UserID)
	f.Cleanup(t, `DELETE FROM review_agent_event WHERE agent_id=$1`, agent)
	wakeSetStatus(t, f, issue, "in_review")
	dispatcher := ReviewAgentService{Tasks: f.svc.TaskSvc}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := dispatcher.TickWorkspace(context.Background(), parseTestUUID(t, f.WorkspaceID)); err != nil {
				t.Errorf("dispatch: %v", err)
			}
		}()
	}
	wg.Wait()
	if n := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE agent_id=$1 AND issue_id=$2 AND context->>'review_agent'='true'`, agent, issue); n != 1 {
		t.Fatalf("queued %d review runs", n)
	}
	if n := f.Count(t, `SELECT count(*) FROM review_agent_event WHERE agent_id=$1 AND processed_at IS NULL`, agent); n != 0 {
		t.Fatalf("left %d pending events", n)
	}
}

func TestReviewAgentDispatchRechecksConfiguration(t *testing.T) {
	f, _, issue, agent := wakeFixture(t)
	f.Exec(t, `UPDATE agent SET review_config=jsonb_build_object('recipient_id',$2::text,'scope','workspace','instant_enabled',true) WHERE id=$1`, agent, f.UserID)
	f.Cleanup(t, `DELETE FROM review_agent_event WHERE agent_id=$1`, agent)
	wakeSetStatus(t, f, issue, "in_review")
	f.Exec(t, `UPDATE agent SET review_config=review_config || '{"instant_enabled":false}'::jsonb WHERE id=$1`, agent)
	dispatcher := ReviewAgentService{Tasks: f.svc.TaskSvc}
	if err := dispatcher.TickWorkspace(context.Background(), parseTestUUID(t, f.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE agent_id=$1 AND context->>'review_agent'='true'`, agent); n != 0 {
		t.Fatalf("disabled configuration queued %d runs", n)
	}
	// Disabling stops instant delivery for good: the pending event is
	// discarded instead of re-scanned forever, so re-enabling alone does not
	// replay it. New events captured after the re-enable dispatch normally.
	if n := f.Count(t, `SELECT count(*) FROM review_agent_event WHERE agent_id=$1 AND processed_at IS NULL`, agent); n != 0 {
		t.Fatalf("disabled configuration left %d pending events", n)
	}
	f.Exec(t, `UPDATE agent SET review_config=review_config || '{"instant_enabled":true}'::jsonb WHERE id=$1`, agent)
	if err := dispatcher.TickWorkspace(context.Background(), parseTestUUID(t, f.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE agent_id=$1 AND context->>'review_agent'='true'`, agent); n != 0 {
		t.Fatalf("re-enable without new events queued %d runs", n)
	}
	wakeSetStatus(t, f, issue, "blocked")
	if err := dispatcher.TickWorkspace(context.Background(), parseTestUUID(t, f.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE agent_id=$1 AND context->>'review_agent'='true'`, agent); n != 1 {
		t.Fatalf("restored configuration queued %d runs", n)
	}
}

func TestReviewAgentDiscardsEventsWhenAgentArchived(t *testing.T) {
	f, _, issue, agent := wakeFixture(t)
	f.Exec(t, `UPDATE agent SET review_config=jsonb_build_object('recipient_id',$2::text,'scope','workspace','instant_enabled',true) WHERE id=$1`, agent, f.UserID)
	f.Cleanup(t, `DELETE FROM review_agent_event WHERE agent_id=$1`, agent)
	wakeSetStatus(t, f, issue, "in_review")
	f.Exec(t, `UPDATE agent SET archived_at=now() WHERE id=$1`, agent)
	dispatcher := ReviewAgentService{Tasks: f.svc.TaskSvc}
	if err := dispatcher.TickWorkspace(context.Background(), parseTestUUID(t, f.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM review_agent_event WHERE agent_id=$1 AND processed_at IS NULL`, agent); n != 0 {
		t.Fatalf("archived agent left %d pending events", n)
	}
	if n := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE agent_id=$1 AND issue_id=$2`, agent, issue); n != 0 {
		t.Fatalf("archived agent queued %d runs", n)
	}
}

func TestReviewAgentInboxNotifiedOnDispatch(t *testing.T) {
	f, _, issue, agent := wakeFixture(t)
	f.Exec(t, `UPDATE agent SET review_config=jsonb_build_object('recipient_id',$2::text,'scope','workspace','instant_enabled',true) WHERE id=$1`, agent, f.UserID)
	f.Cleanup(t, `DELETE FROM review_agent_event WHERE agent_id=$1`, agent)
	f.Cleanup(t, `DELETE FROM inbox_item WHERE recipient_id=$1`, f.UserID)
	wakeSetStatus(t, f, issue, "in_review")
	dispatcher := ReviewAgentService{Tasks: f.svc.TaskSvc}
	if err := dispatcher.TickWorkspace(context.Background(), parseTestUUID(t, f.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM inbox_item WHERE recipient_id=$1 AND type='review_agent_instant'`, f.UserID); n != 1 {
		t.Fatalf("recipient got %d instant notifications", n)
	}
}

func TestReviewAgentEventWaitsWhileReviewTaskActive(t *testing.T) {
	f, _, issue, agent := wakeFixture(t)
	f.Exec(t, `UPDATE agent SET review_config=jsonb_build_object('recipient_id',$2::text,'scope','workspace','instant_enabled',true) WHERE id=$1`, agent, f.UserID)
	f.Cleanup(t, `DELETE FROM review_agent_event WHERE agent_id=$1`, agent)
	f.Cleanup(t, `DELETE FROM agent_task_queue WHERE issue_id=$1`, issue)
	wakeSetStatus(t, f, issue, "in_review")
	runtime := f.Runtime(t, "active gate runtime")
	f.Exec(t, `UPDATE agent SET runtime_id=$2 WHERE id=$1`, agent, runtime)
	active := f.Task(t, agent, testutil.Cols{"issue_id": util.UUIDToString(issue), "runtime_id": runtime, "status": "running", "context": `{"review_agent":true}`})
	dispatcher := ReviewAgentService{Tasks: f.svc.TaskSvc}
	if err := dispatcher.TickWorkspace(context.Background(), parseTestUUID(t, f.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	// The in-flight review task is the only one: dispatch stacked nothing new.
	if n := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE agent_id=$1 AND issue_id=$2`, agent, issue); n != 1 {
		t.Fatalf("active review task left %d total runs", n)
	}
	if n := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE id=$1`, active); n != 1 {
		t.Fatalf("in-flight review task vanished")
	}
	if n := f.Count(t, `SELECT count(*) FROM review_agent_event WHERE agent_id=$1 AND processed_at IS NULL`, agent); n != 1 {
		t.Fatalf("active review task consumed %d events", n)
	}
}

func TestReviewAgentScanReachesEventsBehindBlockedOnes(t *testing.T) {
	f, _, issue, agent := wakeFixture(t)
	f.Exec(t, `UPDATE agent SET review_config=jsonb_build_object('recipient_id',$2::text,'scope','workspace','instant_enabled',true) WHERE id=$1`, agent, f.UserID)
	f.Cleanup(t, `DELETE FROM review_agent_event WHERE agent_id=$1`, agent)
	f.Cleanup(t, `DELETE FROM agent_task_queue WHERE agent_id=$1`, agent)
	runtime := f.Runtime(t, "starvation runtime")
	f.Exec(t, `UPDATE agent SET runtime_id=$2 WHERE id=$1`, agent, runtime)
	// Issue X: an active review run plus 120 pending events — without the
	// scan filter these would fill the 100-row window every tick and the
	// processable event on issue Y would never be scanned.
	blocked := f.Issue(t, "starvation blocked")
	f.Cleanup(t, `DELETE FROM agent_task_queue WHERE issue_id=$1`, blocked)
	f.Task(t, agent, testutil.Cols{"issue_id": blocked, "runtime_id": runtime, "status": "running", "context": `{"review_agent":true}`})
	f.Exec(t, `INSERT INTO review_agent_event(workspace_id,agent_id,issue_id,event_key,event_type)
	 SELECT $1::uuid,$2::uuid,$3::uuid,gen_random_uuid()::text,'review.started' FROM generate_series(1,120)`,
		f.WorkspaceID, agent, blocked)
	ready := f.Issue(t, "starvation ready", testutil.Cols{"status": "in_review"})
	f.Exec(t, `INSERT INTO review_agent_event(workspace_id,agent_id,issue_id,event_key,event_type) VALUES($1::uuid,$2::uuid,$3::uuid,gen_random_uuid()::text,'review.started')`,
		f.WorkspaceID, agent, ready)
	dispatcher := ReviewAgentService{Tasks: f.svc.TaskSvc}
	if err := dispatcher.TickWorkspace(context.Background(), parseTestUUID(t, f.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE agent_id=$1 AND issue_id=$2 AND context->>'review_agent'='true'`, agent, ready); n != 1 {
		t.Fatalf("processable event behind 120 blocked ones queued %d runs", n)
	}
	if n := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE agent_id=$1 AND issue_id=$2`, agent, blocked); n != 1 {
		t.Fatalf("blocked issue got %d total runs", n)
	}
	if n := f.Count(t, `SELECT count(*) FROM review_agent_event WHERE agent_id=$1 AND issue_id=$2 AND processed_at IS NULL`, agent, blocked); n != 120 {
		t.Fatalf("blocked issue lost events: %d pending", n)
	}
	_ = issue
}

func TestReviewAgentKeepsEventWhenRuntimeRebindsMidDispatch(t *testing.T) {
	f, _, issue, agent := wakeFixture(t)
	f.Exec(t, `UPDATE agent SET review_config=jsonb_build_object('recipient_id',$2::text,'scope','workspace','instant_enabled',true) WHERE id=$1`, agent, f.UserID)
	f.Cleanup(t, `DELETE FROM review_agent_event WHERE agent_id=$1`, agent)
	wakeSetStatus(t, f, issue, "in_review")
	runtimeA := f.Runtime(t, "rebind source A")
	runtimeB := f.Runtime(t, "rebind source B")
	f.Exec(t, `UPDATE agent SET runtime_id=$2 WHERE id=$1`, agent, runtimeA)
	ctx := context.Background()

	// Hold the agent row lock with a pending rebind so the dispatcher's
	// serialized re-read observes the new runtime after its pre-lock read.
	tx1, err := f.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx1.Exec(ctx, `SELECT id FROM agent WHERE id=$1 FOR UPDATE`, agent); err != nil {
		t.Fatal(err)
	}
	if _, err := tx1.Exec(ctx, `UPDATE agent SET runtime_id=$2 WHERE id=$1`, agent, runtimeB); err != nil {
		t.Fatal(err)
	}
	dispatcher := ReviewAgentService{Tasks: f.svc.TaskSvc}
	done := make(chan error, 1)
	go func() { done <- dispatcher.TickWorkspace(ctx, parseTestUUID(t, f.WorkspaceID)) }()
	time.Sleep(300 * time.Millisecond)
	if err := tx1.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	// The raced tick must not drop the event or dispatch against the old
	// runtime: it stays pending for the next tick on the new runtime.
	if n := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE agent_id=$1`, agent); n != 0 {
		t.Fatalf("raced tick queued %d runs", n)
	}
	if n := f.Count(t, `SELECT count(*) FROM review_agent_event WHERE agent_id=$1 AND processed_at IS NULL`, agent); n != 1 {
		t.Fatalf("raced tick left %d pending events", n)
	}
	if err := dispatcher.TickWorkspace(ctx, parseTestUUID(t, f.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE agent_id=$1 AND runtime_id=$2`, agent, runtimeB); n != 1 {
		t.Fatalf("settled tick queued %d runs on the new runtime", n)
	}
}

func TestReviewAgentDiscardsWhenRecipientLeavesWorkspace(t *testing.T) {
	f, _, issue, agent := wakeFixture(t)
	recipient := f.member(t, "departing recipient")
	f.Exec(t, `UPDATE agent SET review_config=jsonb_build_object('recipient_id',$2::text,'scope','workspace','instant_enabled',true) WHERE id=$1`, agent, recipient)
	f.Cleanup(t, `DELETE FROM review_agent_event WHERE agent_id=$1`, agent)
	wakeSetStatus(t, f, issue, "in_review")
	// The recipient's membership is the stable refusal ground: with their
	// member row gone the event can never dispatch, so it is discarded.
	f.Exec(t, `DELETE FROM member WHERE user_id=$1`, recipient)
	dispatcher := ReviewAgentService{Tasks: f.svc.TaskSvc}
	if err := dispatcher.TickWorkspace(context.Background(), parseTestUUID(t, f.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM review_agent_event WHERE agent_id=$1 AND processed_at IS NULL`, agent); n != 0 {
		t.Fatalf("departed recipient left %d pending events", n)
	}
	if n := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE agent_id=$1`, agent); n != 0 {
		t.Fatalf("departed recipient queued %d runs", n)
	}
}

func TestReviewAgentTickKeepsEventsOnTransientFailure(t *testing.T) {
	f, _, issue, agent := wakeFixture(t)
	f.Exec(t, `UPDATE agent SET review_config=jsonb_build_object('recipient_id',$2::text,'scope','workspace','instant_enabled',true) WHERE id=$1`, agent, f.UserID)
	f.Cleanup(t, `DELETE FROM review_agent_event WHERE agent_id=$1`, agent)
	wakeSetStatus(t, f, issue, "in_review")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dispatcher := ReviewAgentService{Tasks: f.svc.TaskSvc}
	if err := dispatcher.TickWorkspace(ctx, parseTestUUID(t, f.WorkspaceID)); err == nil {
		t.Fatal("canceled tick reported success")
	}
	if n := f.Count(t, `SELECT count(*) FROM review_agent_event WHERE agent_id=$1 AND processed_at IS NULL`, agent); n != 1 {
		t.Fatalf("transient failure consumed %d events", n)
	}
	if n := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE agent_id=$1`, agent); n != 0 {
		t.Fatalf("transient failure queued %d runs", n)
	}
}
