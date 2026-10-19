package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// summaryFixture configures an agent with instant delivery off and a daily
// summary schedule, plus one in_review issue in scope.
func summaryFixture(t *testing.T, summaryTime, timezone string) (principalFixture, pgtype.UUID, string) {
	t.Helper()
	f, _, issue, agent := wakeFixture(t)
	f.Exec(t, `UPDATE agent SET review_config=jsonb_build_object('recipient_id',$2::text,'scope','workspace','instant_enabled',false,'summary_enabled',true,'summary_time',$3::text,'timezone',$4::text) WHERE id=$1`, agent, f.UserID, summaryTime, timezone)
	f.Cleanup(t, `DELETE FROM review_agent_summary_state WHERE agent_id=$1`, agent)
	f.Cleanup(t, `DELETE FROM review_agent_event WHERE agent_id=$1`, agent)
	wakeSetStatus(t, f, issue, "in_review")
	return f, issue, agent
}

func summaryRow(t *testing.T, f principalFixture, agent string) db.ListReviewSummaryAgentsRow {
	t.Helper()
	rows, err := f.q.ListReviewSummaryAgents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if util.UUIDToString(row.ID) == agent {
			return row
		}
	}
	t.Fatalf("agent %s not listed as summary agent", agent)
	return db.ListReviewSummaryAgentsRow{}
}

func summaryTaskCount(t *testing.T, f principalFixture, agent string) int {
	t.Helper()
	return f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE agent_id=$1 AND context->>'review_summary'='true'`, agent)
}

func TestReviewAgentSummaryDispatchesOncePerLocalDay(t *testing.T) {
	f, _, agent := summaryFixture(t, "09:30", "Asia/Shanghai")
	svc := ReviewAgentService{Tasks: f.svc.TaskSvc}
	// 2026-10-18 01:30 UTC == 09:30 local: exactly due.
	dueTime := time.Date(2026, 10, 18, 1, 30, 0, 0, time.UTC)
	if err := svc.summaryForAgent(context.Background(), summaryRow(t, f, agent), dueTime); err != nil {
		t.Fatal(err)
	}
	if n := summaryTaskCount(t, f, agent); n != 1 {
		t.Fatalf("first due tick queued %d summaries", n)
	}
	if got := f.Count(t, `SELECT count(*) FROM review_agent_summary_state WHERE agent_id=$1 AND last_date='2026-10-18'`, agent); got != 1 {
		t.Fatalf("watermark rows = %d", got)
	}
	// Same local day: no second summary even if the tick repeats.
	if err := svc.summaryForAgent(context.Background(), summaryRow(t, f, agent), dueTime.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if n := summaryTaskCount(t, f, agent); n != 1 {
		t.Fatalf("same-day repeat queued %d summaries", n)
	}
	// Next local day before the scheduled time: still waiting.
	before := time.Date(2026, 10, 18, 17, 0, 0, 0, time.UTC) // 01:00 local Oct 19
	if err := svc.summaryForAgent(context.Background(), summaryRow(t, f, agent), before); err != nil {
		t.Fatal(err)
	}
	if n := summaryTaskCount(t, f, agent); n != 1 {
		t.Fatalf("before-time tick queued %d summaries", n)
	}
	// Yesterday's summary run finished; otherwise its still-queued task would
	// (correctly) cover the issue and today would stay silent.
	f.Exec(t, `UPDATE agent_task_queue SET status='completed' WHERE agent_id=$1 AND context->>'review_summary'='true'`, agent)
	// Next local day after the scheduled time: exactly one more.
	nextDue := time.Date(2026, 10, 19, 2, 0, 0, 0, time.UTC) // 10:00 local Oct 19
	if err := svc.summaryForAgent(context.Background(), summaryRow(t, f, agent), nextDue); err != nil {
		t.Fatal(err)
	}
	if n := summaryTaskCount(t, f, agent); n != 2 {
		t.Fatalf("next-day tick queued %d summaries", n)
	}
	if got := f.Count(t, `SELECT count(*) FROM review_agent_summary_state WHERE agent_id=$1 AND last_date='2026-10-19'`, agent); got != 1 {
		t.Fatalf("watermark not advanced: %d", got)
	}
}

func TestReviewAgentSummaryWaitsForConfiguredLocalTime(t *testing.T) {
	f, _, agent := summaryFixture(t, "09:30", "Asia/Shanghai")
	svc := ReviewAgentService{Tasks: f.svc.TaskSvc}
	// 2026-10-18 00:30 UTC == 08:30 local: before the schedule.
	early := time.Date(2026, 10, 18, 0, 30, 0, 0, time.UTC)
	if err := svc.summaryForAgent(context.Background(), summaryRow(t, f, agent), early); err != nil {
		t.Fatal(err)
	}
	if n := summaryTaskCount(t, f, agent); n != 0 {
		t.Fatalf("early tick queued %d summaries", n)
	}
	if got := f.Count(t, `SELECT count(*) FROM review_agent_summary_state WHERE agent_id=$1`, agent); got != 0 {
		t.Fatalf("early tick wrote watermark: %d", got)
	}
}

func TestReviewAgentSummaryClaimsPendingEvents(t *testing.T) {
	f, issue, agent := summaryFixture(t, "09:30", "Asia/Shanghai")
	// A second issue with a pending event: its event must be claimed by the
	// summary so the instant dispatcher cannot double-dispatch it later.
	other := f.Issue(t, "summary pending", testutil.Cols{"status": "blocked"})
	f.Cleanup(t, `DELETE FROM agent_task_queue WHERE issue_id=$1`, other)
	svc := ReviewAgentService{Tasks: f.svc.TaskSvc}
	dueTime := time.Date(2026, 10, 18, 1, 30, 0, 0, time.UTC)
	if err := svc.summaryForAgent(context.Background(), summaryRow(t, f, agent), dueTime); err != nil {
		t.Fatal(err)
	}
	if n := summaryTaskCount(t, f, agent); n != 1 {
		t.Fatalf("queued %d summaries", n)
	}
	if n := f.Count(t, `SELECT count(*) FROM review_agent_event WHERE agent_id=$1 AND processed_at IS NULL`, agent); n != 0 {
		t.Fatalf("left %d pending events", n)
	}
	if n := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE agent_id=$1 AND context->>'review_summary'='true'`, agent); n != 1 {
		t.Fatalf("summary task missing: %d", n)
	}
	_ = issue
}

func TestReviewAgentSummaryEmptyStillAdvancesWatermark(t *testing.T) {
	f, issue, agent := summaryFixture(t, "09:30", "Asia/Shanghai")
	// Take the issue out of the review window and give it no pending events:
	// nothing to report, but the day is still marked delivered.
	wakeSetStatus(t, f, issue, "done")
	svc := ReviewAgentService{Tasks: f.svc.TaskSvc}
	dueTime := time.Date(2026, 10, 18, 1, 30, 0, 0, time.UTC)
	if err := svc.summaryForAgent(context.Background(), summaryRow(t, f, agent), dueTime); err != nil {
		t.Fatal(err)
	}
	if n := summaryTaskCount(t, f, agent); n != 0 {
		t.Fatalf("empty summary queued %d runs", n)
	}
	if got := f.Count(t, `SELECT count(*) FROM review_agent_summary_state WHERE agent_id=$1 AND last_date='2026-10-18'`, agent); got != 1 {
		t.Fatalf("watermark rows = %d", got)
	}
}

func TestReviewAgentSummarySkipsIssueWithActiveReviewTask(t *testing.T) {
	f, issue, agent := summaryFixture(t, "09:30", "Asia/Shanghai")
	// An instant review run of this same agent already in flight covers the
	// issue: the summary must not stack a second task on it, and must not
	// advance the watermark — the day's report is owed once the run settles.
	runtime := f.Runtime(t, "summary active runtime")
	f.Exec(t, `UPDATE agent SET runtime_id=$2 WHERE id=$1`, agent, runtime)
	f.Task(t, agent, testutil.Cols{"issue_id": util.UUIDToString(issue), "runtime_id": runtime, "status": "running", "context": `{"review_agent":true}`})
	svc := ReviewAgentService{Tasks: f.svc.TaskSvc}
	dueTime := time.Date(2026, 10, 18, 1, 30, 0, 0, time.UTC)
	if err := svc.summaryForAgent(context.Background(), summaryRow(t, f, agent), dueTime); err != nil {
		t.Fatal(err)
	}
	if n := summaryTaskCount(t, f, agent); n != 0 {
		t.Fatalf("summary queued %d runs over an active review", n)
	}
	if got := f.Count(t, `SELECT count(*) FROM review_agent_summary_state WHERE agent_id=$1`, agent); got != 0 {
		t.Fatalf("watermark advanced with active work unsettled: %d", got)
	}
}

func TestReviewAgentSummaryDeliversAfterActiveTasksSettle(t *testing.T) {
	f, issue, agent := summaryFixture(t, "09:30", "Asia/Shanghai")
	runtime := f.Runtime(t, "summary settle runtime")
	f.Exec(t, `UPDATE agent SET runtime_id=$2 WHERE id=$1`, agent, runtime)
	f.Task(t, agent, testutil.Cols{"issue_id": util.UUIDToString(issue), "runtime_id": runtime, "status": "running", "context": `{"review_agent":true}`})
	svc := ReviewAgentService{Tasks: f.svc.TaskSvc}
	dueTime := time.Date(2026, 10, 18, 1, 30, 0, 0, time.UTC)
	if err := svc.summaryForAgent(context.Background(), summaryRow(t, f, agent), dueTime); err != nil {
		t.Fatal(err)
	}
	if n := summaryTaskCount(t, f, agent); n != 0 {
		t.Fatalf("queued %d summaries before tasks settled", n)
	}
	// The in-flight run finishes; the same local day is still owed a report.
	f.Exec(t, `UPDATE agent_task_queue SET status='completed' WHERE agent_id=$1 AND context->>'review_agent'='true'`, agent)
	if err := svc.summaryForAgent(context.Background(), summaryRow(t, f, agent), dueTime.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n := summaryTaskCount(t, f, agent); n != 1 {
		t.Fatalf("settled day queued %d summaries", n)
	}
	if got := f.Count(t, `SELECT count(*) FROM review_agent_summary_state WHERE agent_id=$1 AND last_date='2026-10-18'`, agent); got != 1 {
		t.Fatalf("watermark rows = %d", got)
	}
}

func TestReviewAgentSummaryCoversMoreThanTwentyFiveIssues(t *testing.T) {
	f, _, agent := summaryFixture(t, "09:30", "Asia/Shanghai")
	f.Cleanup(t, `DELETE FROM agent_task_queue WHERE agent_id=$1`, agent)
	// 29 more in-scope review issues on top of the fixture issue: a capped
	// enumerator would permanently skip whatever fell past the cap while
	// still advancing the watermark.
	for i := 0; i < 29; i++ {
		extra := f.Issue(t, fmt.Sprintf("backlog item %02d", i), testutil.Cols{"status": "in_review"})
		f.Cleanup(t, `DELETE FROM agent_task_queue WHERE issue_id=$1`, extra)
	}
	svc := ReviewAgentService{Tasks: f.svc.TaskSvc}
	dueTime := time.Date(2026, 10, 18, 1, 30, 0, 0, time.UTC)
	if err := svc.summaryForAgent(context.Background(), summaryRow(t, f, agent), dueTime); err != nil {
		t.Fatal(err)
	}
	if n := summaryTaskCount(t, f, agent); n != 1 {
		t.Fatalf("queued %d summaries", n)
	}
	var note string
	if err := f.Pool.QueryRow(context.Background(), `SELECT handoff_note FROM agent_task_queue WHERE agent_id=$1 AND context->>'review_summary'='true'`, agent).Scan(&note); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 29; i++ {
		title := fmt.Sprintf("backlog item %02d", i)
		if !strings.Contains(note, title) {
			t.Fatalf("summary note missing %q", title)
		}
	}
}

func TestReviewAgentSummaryCoversMoreThanOneHundredIssues(t *testing.T) {
	f, _, agent := summaryFixture(t, "09:30", "Asia/Shanghai")
	f.Cleanup(t, `DELETE FROM agent_task_queue WHERE agent_id=$1`, agent)
	// 119 more issues: the scope query itself must not page the candidate
	// pool away, or the last ones would never be summarized.
	for i := 0; i < 119; i++ {
		extra := f.Issue(t, fmt.Sprintf("wide backlog %03d", i), testutil.Cols{"status": "in_review"})
		f.Cleanup(t, `DELETE FROM agent_task_queue WHERE issue_id=$1`, extra)
	}
	svc := ReviewAgentService{Tasks: f.svc.TaskSvc}
	dueTime := time.Date(2026, 10, 18, 1, 30, 0, 0, time.UTC)
	if err := svc.summaryForAgent(context.Background(), summaryRow(t, f, agent), dueTime); err != nil {
		t.Fatal(err)
	}
	if n := summaryTaskCount(t, f, agent); n != 1 {
		t.Fatalf("queued %d summaries", n)
	}
	var note string
	if err := f.Pool.QueryRow(context.Background(), `SELECT handoff_note FROM agent_task_queue WHERE agent_id=$1 AND context->>'review_summary'='true'`, agent).Scan(&note); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 119; i++ {
		title := fmt.Sprintf("wide backlog %03d", i)
		if !strings.Contains(note, title) {
			t.Fatalf("summary note missing %q", title)
		}
	}
}

func TestReviewAgentSummaryRevalidatesPendingEventScope(t *testing.T) {
	f, issue, agent := summaryFixture(t, "09:30", "Asia/Shanghai")
	f.Cleanup(t, `DELETE FROM agent_task_queue WHERE agent_id=$1`, agent)
	// The agent monitors one project; a pending event references an issue in
	// another project (captured before the scope narrowed). The summary must
	// not claim or report it.
	inScope := f.Project(t, "summary in scope")
	outScope := f.Project(t, "summary out scope")
	f.Exec(t, `UPDATE issue SET project_id=$2 WHERE id=$1`, issue, inScope)
	f.Exec(t, `UPDATE agent SET review_config=jsonb_build_object('recipient_id',$2::text,'scope','projects','project_ids',jsonb_build_array($3::text),'instant_enabled',false,'summary_enabled',true,'summary_time','09:30','timezone','Asia/Shanghai') WHERE id=$1`, agent, f.UserID, inScope)
	stranger := f.Issue(t, "narrowed out", testutil.Cols{"status": "blocked", "project_id": outScope})
	f.Cleanup(t, `DELETE FROM agent_task_queue WHERE issue_id=$1`, stranger)
	f.Exec(t, `INSERT INTO review_agent_event(workspace_id,agent_id,issue_id,event_key,event_type) VALUES($1::uuid,$2::uuid,$3::uuid,gen_random_uuid()::text,'issue.blocked')`, f.WorkspaceID, agent, stranger)
	svc := ReviewAgentService{Tasks: f.svc.TaskSvc}
	dueTime := time.Date(2026, 10, 18, 1, 30, 0, 0, time.UTC)
	if err := svc.summaryForAgent(context.Background(), summaryRow(t, f, agent), dueTime); err != nil {
		t.Fatal(err)
	}
	if n := summaryTaskCount(t, f, agent); n != 1 {
		t.Fatalf("queued %d summaries", n)
	}
	var note string
	if err := f.Pool.QueryRow(context.Background(), `SELECT handoff_note FROM agent_task_queue WHERE agent_id=$1 AND context->>'review_summary'='true'`, agent).Scan(&note); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(note, "narrowed out") {
		t.Fatal("summary claimed an out-of-scope pending issue")
	}
	if n := f.Count(t, `SELECT count(*) FROM review_agent_event WHERE agent_id=$1 AND issue_id=$2 AND processed_at IS NULL`, agent, stranger); n != 1 {
		t.Fatalf("out-of-scope pending event consumed: %d left", n)
	}
}

func TestReviewAgentSummaryConcurrentTicksDeliverOnce(t *testing.T) {
	f, _, agent := summaryFixture(t, "09:30", "Asia/Shanghai")
	f.Cleanup(t, `DELETE FROM agent_task_queue WHERE agent_id=$1`, agent)
	svc := ReviewAgentService{Tasks: f.svc.TaskSvc}
	dueTime := time.Date(2026, 10, 18, 1, 30, 0, 0, time.UTC)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := svc.summaryForAgent(context.Background(), summaryRow(t, f, agent), dueTime); err != nil {
				t.Errorf("concurrent summary: %v", err)
			}
		}()
	}
	wg.Wait()
	if n := summaryTaskCount(t, f, agent); n != 1 {
		t.Fatalf("concurrent ticks queued %d summaries", n)
	}
	if got := f.Count(t, `SELECT count(*) FROM review_agent_summary_state WHERE agent_id=$1 AND last_date='2026-10-18'`, agent); got != 1 {
		t.Fatalf("watermark rows = %d", got)
	}
}

func summaryNote(t *testing.T, f principalFixture, agent string) string {
	t.Helper()
	var note string
	if err := f.Pool.QueryRow(context.Background(), `SELECT handoff_note FROM agent_task_queue WHERE agent_id=$1 AND context->>'review_summary'='true' ORDER BY created_at LIMIT 1`, agent).Scan(&note); err != nil {
		t.Fatal(err)
	}
	return note
}

func workspacePrefix(t *testing.T, f principalFixture) string {
	t.Helper()
	var prefix string
	if err := f.Pool.QueryRow(context.Background(), `SELECT issue_prefix FROM workspace WHERE id=$1`, f.WorkspaceID).Scan(&prefix); err != nil {
		t.Fatal(err)
	}
	return prefix
}

func issueNumbers(t *testing.T, f principalFixture, title string) []int {
	t.Helper()
	rows, err := f.Pool.Query(context.Background(), `SELECT number FROM issue WHERE workspace_id=$1 AND title=$2 ORDER BY number`, f.WorkspaceID, title)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var numbers []int
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		numbers = append(numbers, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return numbers
}

func TestReviewAgentSummaryListsUniqueIssueIdentifiers(t *testing.T) {
	f, _, agent := summaryFixture(t, "09:30", "Asia/Shanghai")
	f.Cleanup(t, `DELETE FROM agent_task_queue WHERE agent_id=$1`, agent)
	// 两个同名任务：只有标题时接收人无法区分，条目必须带唯一 identifier。
	f.Issue(t, "duplicate title", testutil.Cols{"status": "in_review"})
	f.Issue(t, "duplicate title", testutil.Cols{"status": "in_review"})
	svc := ReviewAgentService{Tasks: f.svc.TaskSvc}
	dueTime := time.Date(2026, 10, 18, 1, 30, 0, 0, time.UTC)
	if err := svc.summaryForAgent(context.Background(), summaryRow(t, f, agent), dueTime); err != nil {
		t.Fatal(err)
	}
	prefix := workspacePrefix(t, f)
	numbers := issueNumbers(t, f, "duplicate title")
	if len(numbers) != 2 {
		t.Fatalf("expected 2 same-titled issues, got %v", numbers)
	}
	note := summaryNote(t, f, agent)
	for _, n := range numbers {
		identifier := fmt.Sprintf("%s-%d", prefix, n)
		if !strings.Contains(note, identifier) {
			t.Fatalf("summary note missing identifier %s", identifier)
		}
	}
}

func TestReviewAgentSummaryRechecksDueUnderLockAfterScheduleEdit(t *testing.T) {
	f, _, agent := summaryFixture(t, "09:30", "Asia/Shanghai")
	f.Cleanup(t, `DELETE FROM agent_task_queue WHERE agent_id=$1`, agent)
	ctx := context.Background()
	// 持锁把汇总时间改成当天更晚时刻：锁外的旧配置本来判定 due，
	// 锁内复查必须基于编辑后的配置才不会误发。
	tx1, err := f.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx1.Exec(ctx, `SELECT id FROM agent WHERE id=$1 FOR UPDATE`, agent); err != nil {
		t.Fatal(err)
	}
	if _, err := tx1.Exec(ctx, `UPDATE agent SET review_config=review_config || '{"summary_time":"23:59"}'::jsonb WHERE id=$1`, agent); err != nil {
		t.Fatal(err)
	}
	svc := ReviewAgentService{Tasks: f.svc.TaskSvc}
	dueTime := time.Date(2026, 10, 18, 1, 30, 0, 0, time.UTC) // 09:30 local: due under the OLD config only
	done := make(chan error, 1)
	go func() { done <- svc.summaryForAgent(context.Background(), summaryRow(t, f, agent), dueTime) }()
	time.Sleep(300 * time.Millisecond)
	if err := tx1.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if n := summaryTaskCount(t, f, agent); n != 0 {
		t.Fatalf("stale schedule queued %d summaries", n)
	}
	if got := f.Count(t, `SELECT count(*) FROM review_agent_summary_state WHERE agent_id=$1`, agent); got != 0 {
		t.Fatalf("watermark advanced on stale schedule: %d", got)
	}
}

func TestReviewAgentSummarySplitsOversizedReportsWithoutDroppingCoverage(t *testing.T) {
	old := reviewSummaryNoteBudget
	reviewSummaryNoteBudget = 900
	t.Cleanup(func() { reviewSummaryNoteBudget = old })
	f, _, agent := summaryFixture(t, "09:30", "Asia/Shanghai")
	f.Cleanup(t, `DELETE FROM agent_task_queue WHERE agent_id=$1`, agent)
	titles := make([]string, 0, 29)
	for i := 0; i < 29; i++ {
		title := fmt.Sprintf("batch coverage %02d", i)
		titles = append(titles, title)
		extra := f.Issue(t, title, testutil.Cols{"status": "in_review"})
		f.Cleanup(t, `DELETE FROM agent_task_queue WHERE issue_id=$1`, extra)
	}
	svc := ReviewAgentService{Tasks: f.svc.TaskSvc}
	dueTime := time.Date(2026, 10, 18, 1, 30, 0, 0, time.UTC)
	if err := svc.summaryForAgent(context.Background(), summaryRow(t, f, agent), dueTime); err != nil {
		t.Fatal(err)
	}
	if n := summaryTaskCount(t, f, agent); n < 2 {
		t.Fatalf("oversized report was not split: %d tasks", n)
	}
	// Coverage is complete across every batch: no issue may be dropped just
	// because the note hit its size budget.
	rows, err := f.Pool.Query(context.Background(), `SELECT handoff_note FROM agent_task_queue WHERE agent_id=$1 AND context->>'review_summary'='true' ORDER BY created_at`, agent)
	if err != nil {
		t.Fatal(err)
	}
	var combined strings.Builder
	for rows.Next() {
		var note string
		if err := rows.Scan(&note); err != nil {
			t.Fatal(err)
		}
		combined.WriteString(note)
	}
	rows.Close()
	for _, title := range titles {
		if !strings.Contains(combined.String(), title) {
			t.Fatalf("split summary dropped %q", title)
		}
	}
	oversized := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE agent_id=$1 AND context->>'review_summary'='true' AND length(handoff_note) > $2`, agent, reviewSummaryNoteBudget)
	if oversized != 0 {
		t.Fatalf("%d batch notes exceed the byte budget", oversized)
	}
	if got := f.Count(t, `SELECT count(*) FROM review_agent_summary_state WHERE agent_id=$1 AND last_date='2026-10-18'`, agent); got != 1 {
		t.Fatalf("watermark rows = %d", got)
	}
}

func TestReviewAgentSummaryWaitsForPartialActiveCoverage(t *testing.T) {
	f, _, agent := summaryFixture(t, "09:30", "Asia/Shanghai")
	f.Cleanup(t, `DELETE FROM agent_task_queue WHERE agent_id=$1`, agent)
	// Issue A is under active review; issue B is not. A partial report that
	// covers only B and advances the watermark would drop A from the day's
	// report forever.
	blocked := f.Issue(t, "active covered", testutil.Cols{"status": "in_review"})
	f.Cleanup(t, `DELETE FROM agent_task_queue WHERE issue_id=$1`, blocked)
	runtime := f.Runtime(t, "partial active runtime")
	f.Exec(t, `UPDATE agent SET runtime_id=$2 WHERE id=$1`, agent, runtime)
	f.Task(t, agent, testutil.Cols{"issue_id": blocked, "runtime_id": runtime, "status": "running", "context": `{"review_agent":true}`})
	svc := ReviewAgentService{Tasks: f.svc.TaskSvc}
	dueTime := time.Date(2026, 10, 18, 1, 30, 0, 0, time.UTC)
	if err := svc.summaryForAgent(context.Background(), summaryRow(t, f, agent), dueTime); err != nil {
		t.Fatal(err)
	}
	if n := summaryTaskCount(t, f, agent); n != 0 {
		t.Fatalf("partial coverage queued %d summaries", n)
	}
	if got := f.Count(t, `SELECT count(*) FROM review_agent_summary_state WHERE agent_id=$1`, agent); got != 0 {
		t.Fatalf("watermark advanced on partial coverage: %d", got)
	}
	// The active run settles; the same local day is owed a COMPLETE report.
	f.Exec(t, `UPDATE agent_task_queue SET status='completed' WHERE agent_id=$1 AND context->>'review_agent'='true'`, agent)
	if err := svc.summaryForAgent(context.Background(), summaryRow(t, f, agent), dueTime.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n := summaryTaskCount(t, f, agent); n != 1 {
		t.Fatalf("settled day queued %d summaries", n)
	}
	note := summaryNote(t, f, agent)
	prefix := workspacePrefix(t, f)
	blockedNo := issueNumbers(t, f, "active covered")[0]
	if !strings.Contains(note, fmt.Sprintf("%s-%d", prefix, blockedNo)) {
		t.Fatalf("settled summary missing previously active issue %s-%d", prefix, blockedNo)
	}
	if got := f.Count(t, `SELECT count(*) FROM review_agent_summary_state WHERE agent_id=$1 AND last_date='2026-10-18'`, agent); got != 1 {
		t.Fatalf("watermark rows = %d", got)
	}
}

func TestReviewAgentSummaryTransactionFailureRollsBack(t *testing.T) {
	f, _, agent := summaryFixture(t, "09:30", "Asia/Shanghai")
	f.Cleanup(t, `DELETE FROM agent_task_queue WHERE agent_id=$1`, agent)
	// A test-only trigger injects a failure at the watermark write so the
	// whole summary transaction — tasks, event claims, watermark — must roll
	// back together.
	f.Exec(t, `CREATE TABLE IF NOT EXISTS review_summary_fail_flag(id int)`)
	f.Exec(t, `CREATE OR REPLACE FUNCTION review_summary_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF EXISTS (SELECT 1 FROM review_summary_fail_flag) THEN RAISE EXCEPTION 'injected summary failure'; END IF; RETURN NEW; END $$`)
	f.Exec(t, `DROP TRIGGER IF EXISTS review_summary_fail ON review_agent_summary_state`)
	f.Exec(t, `CREATE TRIGGER review_summary_fail BEFORE INSERT OR UPDATE ON review_agent_summary_state FOR EACH ROW EXECUTE FUNCTION review_summary_fail()`)
	t.Cleanup(func() {
		f.Exec(t, `DROP TRIGGER IF EXISTS review_summary_fail ON review_agent_summary_state`)
		f.Exec(t, `DROP FUNCTION IF EXISTS review_summary_fail()`)
		f.Exec(t, `DROP TABLE IF EXISTS review_summary_fail_flag`)
	})
	f.Exec(t, `INSERT INTO review_summary_fail_flag VALUES (1)`)
	svc := ReviewAgentService{Tasks: f.svc.TaskSvc}
	dueTime := time.Date(2026, 10, 18, 1, 30, 0, 0, time.UTC)
	if err := svc.summaryForAgent(context.Background(), summaryRow(t, f, agent), dueTime); err == nil {
		t.Fatal("injected failure did not surface")
	}
	if n := summaryTaskCount(t, f, agent); n != 0 {
		t.Fatalf("failed summary left %d tasks", n)
	}
	if n := f.Count(t, `SELECT count(*) FROM review_agent_summary_state WHERE agent_id=$1`, agent); n != 0 {
		t.Fatalf("failed summary left %d watermark rows", n)
	}
	// Recovery: without the injected failure the same tick path delivers.
	f.Exec(t, `DELETE FROM review_summary_fail_flag`)
	if err := svc.summaryForAgent(context.Background(), summaryRow(t, f, agent), dueTime); err != nil {
		t.Fatal(err)
	}
	if n := summaryTaskCount(t, f, agent); n != 1 {
		t.Fatalf("recovered summary queued %d tasks", n)
	}
}
