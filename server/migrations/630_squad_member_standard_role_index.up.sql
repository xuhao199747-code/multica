-- Enforce one member per standard role inside a squad. The dedupe of any
-- existing dirty data happens in application code before this index is built;
-- the WHERE clause keeps the index partial so unmapped members stay free.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_squad_member_standard_role_unique
ON squad_member (squad_id, standard_role)
WHERE standard_role IS NOT NULL;
