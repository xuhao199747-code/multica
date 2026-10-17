-- Anchor spec comments: a comment may belong to a spec_version instead of an
-- issue (DAWE-8). No foreign key by policy — the application validates that
-- the version exists under a spec in the same workspace.
ALTER TABLE comment ADD COLUMN IF NOT EXISTS spec_version_id UUID;
