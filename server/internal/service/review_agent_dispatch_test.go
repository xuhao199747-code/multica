package service

import (
	"context"
	"sync"
	"testing"
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
	f.Exec(t, `UPDATE agent SET review_config=review_config || '{"instant_enabled":true}'::jsonb WHERE id=$1`, agent)
	if err := dispatcher.TickWorkspace(context.Background(), parseTestUUID(t, f.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE agent_id=$1 AND context->>'review_agent'='true'`, agent); n != 1 {
		t.Fatalf("restored configuration queued %d runs", n)
	}
}
