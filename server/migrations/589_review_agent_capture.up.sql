-- Capture references in the same transaction as the source change. The
-- dispatcher must re-check configuration, membership and current state.
CREATE FUNCTION capture_review_agent_event(p_issue uuid, p_type text, p_key text, p_agent uuid, p_task uuid)
RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO review_agent_event(workspace_id,agent_id,issue_id,event_key,event_type,source_task_id)
 SELECT i.workspace_id,a.id,i.id,p_key,p_type,p_task
 FROM issue i JOIN agent a ON a.workspace_id=i.workspace_id
 WHERE i.id=p_issue AND a.archived_at IS NULL
   AND a.review_config->'instant_enabled'='true'::jsonb
   AND (a.review_config->>'scope'='workspace' OR
        (a.review_config->>'scope'='projects' AND a.review_config->'project_ids' ? i.project_id::text))
   AND a.id IS DISTINCT FROM p_agent
 ON CONFLICT (agent_id,event_key) DO NOTHING;
END $$;

CREATE FUNCTION capture_review_issue_status() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER capture_review_issue_status AFTER INSERT OR UPDATE OF status ON issue FOR EACH ROW EXECUTE FUNCTION capture_review_issue_status();

CREATE FUNCTION capture_review_task_failure() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.issue_id IS NOT NULL AND NEW.status='failed' AND OLD.status IS DISTINCT FROM NEW.status THEN
  PERFORM capture_review_agent_event(NEW.issue_id,'task.failed',NEW.id::text||':failed',NEW.agent_id,NEW.id);
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER capture_review_task_failure AFTER UPDATE OF status ON agent_task_queue FOR EACH ROW EXECUTE FUNCTION capture_review_task_failure();
