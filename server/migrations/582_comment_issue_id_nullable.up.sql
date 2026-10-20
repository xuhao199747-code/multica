-- Spec-anchored comments carry no issue; exactly-one-of (issue_id,
-- spec_version_id) is enforced by comment_anchor_check (583).
ALTER TABLE comment ALTER COLUMN issue_id DROP NOT NULL;
