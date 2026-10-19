UPDATE review_repair SET status='blocked' WHERE status='superseded';
ALTER TABLE review_repair DROP CONSTRAINT review_repair_status_check, ADD CONSTRAINT review_repair_status_check CHECK (status IN ('pending','dispatched','awaiting_rereview','rereview_dispatched','passed','failed','blocked'));
