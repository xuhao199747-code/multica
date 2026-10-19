ALTER TABLE review_repair DROP COLUMN IF EXISTS issue_revision;

CREATE OR REPLACE FUNCTION capture_review_issue_status() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE source_id uuid; source_agent uuid; event_type text;
BEGIN
 IF TG_OP='UPDATE' AND NEW.status IS NOT DISTINCT FROM OLD.status THEN RETURN NEW; END IF;
 event_type := CASE WHEN NEW.status='in_review' THEN 'review.started'
                    WHEN NEW.status='blocked' THEN 'issue.blocked'
                    WHEN TG_OP='UPDATE' AND OLD.status='in_review' THEN 'review.ended' END;
 IF event_type IS NULL THEN RETURN NEW; END IF;
 source_id := NULLIF(current_setting('multica.source_task_id',true),'')::uuid;
 SELECT agent_id INTO source_agent FROM agent_task_queue WHERE id=source_id;
 IF source_agent IS NULL AND current_setting('multica.actor_type',true)='agent' THEN
  source_agent := NULLIF(current_setting('multica.actor_id',true),'')::uuid;
 END IF;
 PERFORM capture_review_agent_event(NEW.id,event_type,gen_random_uuid()::text,source_agent,source_id);
 RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION capture_review_task_failure() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.issue_id IS NOT NULL AND NEW.status='failed' AND OLD.status IS DISTINCT FROM NEW.status THEN
  PERFORM capture_review_agent_event(NEW.issue_id,'task.failed',NEW.id::text||':failed',NEW.agent_id,NEW.id);
 END IF;
 RETURN NEW;
END $$;
