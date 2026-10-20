CREATE TABLE IF NOT EXISTS agent_workflow_role (
  agent_id uuid NOT NULL,
  role text NOT NULL CHECK (role IN ('coordinator','prd','executor','reviewer')),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
