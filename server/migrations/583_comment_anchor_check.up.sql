-- Exactly one anchor per comment: every comment belongs to either an issue or
-- a spec_version, never both, never neither. Matches the application-layer xor
-- invariant; existing rows all carry issue_id and satisfy it.
ALTER TABLE comment ADD CONSTRAINT comment_anchor_check
    CHECK (num_nonnulls(issue_id, spec_version_id) = 1);
