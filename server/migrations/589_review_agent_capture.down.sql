DROP TRIGGER IF EXISTS capture_review_task_failure ON agent_task_queue;
DROP FUNCTION IF EXISTS capture_review_task_failure();
DROP TRIGGER IF EXISTS capture_review_issue_status ON issue;
DROP FUNCTION IF EXISTS capture_review_issue_status();
DROP FUNCTION IF EXISTS capture_review_agent_event(uuid,text,text,uuid,uuid);
