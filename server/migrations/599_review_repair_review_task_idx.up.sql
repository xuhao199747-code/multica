-- One repair row per review task: the completion hook replays must never
-- create a second round for the same review outcome.
CREATE UNIQUE INDEX CONCURRENTLY review_repair_review_task_idx ON review_repair(review_task_id);
