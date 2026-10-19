-- Repair loop state for actionable (needs_changes) review outcomes. One row
-- per repair round: review task -> repair task -> re-review task. Rounds are
-- bounded to 3 per review episode in application code; status transitions are
-- compare-and-swap through TransitionReviewRepair. No inline PRIMARY KEY and
-- no foreign keys per repository rules: 0597/0598/0599 build the indexes.
CREATE TABLE review_repair (
 id uuid NOT NULL DEFAULT gen_random_uuid(),
 workspace_id uuid NOT NULL,
 issue_id uuid NOT NULL,
 review_task_id uuid NOT NULL,
 review_agent_id uuid NOT NULL,
 recipient_id uuid NOT NULL,
 round integer NOT NULL DEFAULT 1,
 status text NOT NULL DEFAULT 'pending'
   CHECK (status IN ('pending','dispatched','awaiting_rereview','rereview_dispatched','passed','failed','blocked')),
 repair_task_id uuid,
 rereview_task_id uuid,
 findings jsonb NOT NULL DEFAULT '{}'::jsonb,
 blocked_reason text,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK (round BETWEEN 1 AND 3)
);
