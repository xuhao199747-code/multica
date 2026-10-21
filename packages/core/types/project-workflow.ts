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
  created_at: string;
}

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
  state: string;
  review_round: number;
  created_at: string;
}

export interface ListProjectExecutionsResponse {
  executions: ProjectExecution[];
  total: number;
}
