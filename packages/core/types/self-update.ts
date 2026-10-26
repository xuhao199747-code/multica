export type SelfUpdateCapability =
  | "unsupported"
  | "needs_setup"
  | "worker_offline"
  | "ready";

export interface SelfUpdateRelease {
  tag: string;
  sha: string;
  url: string;
}

export interface SelfUpdateJob {
  id: string;
  phase: string;
  step: string;
  target_tag: string;
  target_sha: string;
  source_sha: string;
  repair_count: number;
  retry_count: number;
  created_at: string;
  updated_at: string;
  finished_at: string | null;
}

export interface SelfUpdateStatus {
  capability: SelfUpdateCapability;
  can_update: boolean;
  latest_release: SelfUpdateRelease | null;
  deployed_release: string;
  deployed_custom_sha: string;
  active_job: SelfUpdateJob | null;
  update_available: boolean;
  check_failed: boolean;
}
