export type ProjectDeliveryMode =
  | "notify_only"
  | "pull_request"
  | "manual_release"
  | "automated_release";

export type ProjectAgentRole = "mika" | "prd" | "developer" | "reviewer";

export interface ProjectWorkflowSettings {
  project_id: string;
  workspace_id: string;
  enabled: boolean;
  project_context: string;
  delivery_mode: ProjectDeliveryMode;
  delivery_config: Record<string, unknown>;
  updated_at?: string;
}

export interface UpdateProjectWorkflowSettingsRequest {
  enabled: boolean;
  project_context: string;
  delivery_mode: ProjectDeliveryMode;
  delivery_config?: Record<string, unknown>;
}

export interface ProjectAgentBinding {
  id: string;
  project_id: string;
  workspace_id: string;
  agent_id: string;
  role: ProjectAgentRole;
  /** 'squad' rows came from the squad template; 'manual' rows are project-local overrides. */
  source: ProjectAgentBindingSource;
  created_at: string;
}

export type ProjectAgentBindingSource = "squad" | "manual";

export interface CreateProjectAgentBindingRequest {
  agent_id: string;
  role: ProjectAgentRole;
}

export interface ListProjectAgentBindingsResponse {
  bindings: ProjectAgentBinding[];
  total: number;
}

export interface ProjectExecution {
  id: string;
  project_id: string;
  workspace_id: string;
  source_chat_session_id: string;
  source_agent_id: string;
  snapshot: Record<string, unknown>;
	/** Commit, tests, review, and project delivery evidence captured for this execution. */
  delivery_result: Record<string, unknown>;
  state: string;
  review_round: number;
  created_at: string;
  main_issue: ProjectExecutionIssue | null;
  historical_issues: ProjectExecutionIssue[];
}

export interface ProjectExecutionIssue {
  id: string;
  title: string;
  status: string;
  role: string;
}

export interface ListProjectExecutionsResponse {
  executions: ProjectExecution[];
  total: number;
}

export interface StartProjectWorkflowExecutionRequest {
  source_chat_session_id: string;
  source_message_id: string;
  source_agent_id?: string;
  snapshot: Record<string, unknown>;
}

export interface StartProjectWorkflowExecutionResponse {
  execution: ProjectExecution;
  issue: Record<string, unknown> | null;
  execution_issue: Record<string, unknown> | null;
  task_id: string | null;
}

export interface SetProjectCollaborationSquadRequest {
  squad_id: string | null;
}

export interface ProjectRoleSourceDiffEntry {
  role: ProjectAgentRole;
  from?: { id: string; agent_id: string; agent_name?: string; archived?: boolean };
  to?: { id: string; agent_id: string; agent_name?: string; archived?: boolean };
  removing?: { id: string; agent_id: string; agent_name?: string; archived?: boolean };
}

export interface ProjectRoleSourceDiff {
  changes: ProjectRoleSourceDiffEntry[];
  removals: ProjectRoleSourceDiffEntry[];
  adds: ProjectRoleSourceDiffEntry[];
  unchanged: string[];
  diff_hash: string;
}

export interface ProjectRoleSource {
  project_id: string;
  squad_id: string | null;
  squad_name: string | null;
  squad_archived: boolean;
  squad_missing: boolean;
  mapping_incomplete?: boolean;
  overrides_present: boolean;
  bindings: ProjectAgentBinding[];
  diff: ProjectRoleSourceDiff;
}
