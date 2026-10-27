-- Tracks whether a project agent binding came from the squad template
-- ('squad') or from a project-local manual override ('manual').
ALTER TABLE project_agent_binding ADD COLUMN source TEXT NOT NULL DEFAULT 'manual';
