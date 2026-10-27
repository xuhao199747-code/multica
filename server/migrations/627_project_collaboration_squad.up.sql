-- Projects may reference a squad as their role configuration template.
-- No foreign key: squad deletion is handled at the application layer.
ALTER TABLE project ADD COLUMN collaboration_squad_id UUID;
