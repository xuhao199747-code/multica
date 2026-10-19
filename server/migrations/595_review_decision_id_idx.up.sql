-- Single-row lookups (resolve, workspace-scoped get) go through id.
CREATE UNIQUE INDEX CONCURRENTLY review_decision_id_idx ON review_decision(id);
