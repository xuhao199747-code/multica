// DAWE-20 review agent types. A review agent is an ordinary user agent with an
// opt-in review role: it monitors committed review events in its scope and
// reports to a workspace-member recipient. Older backends omit every field
// here — consumers must treat absence as "no review configuration".

/** Review role configuration stored on an agent (agent.review_config). */
export interface ReviewAgentConfig {
  /** Workspace-member user id that receives notifications and summaries. */
  recipient_id: string;
  /** "workspace" monitors every project; "projects" only the listed ones. */
  scope: "workspace" | "projects";
  /** Required (1..100, unique) when scope is "projects"; empty otherwise. */
  project_ids?: string[];
  /** Dispatch a review run as soon as committed events arrive. */
  instant_enabled: boolean;
  /** Send one daily summary at summary_time in timezone. */
  summary_enabled: boolean;
  /** Local "HH:MM" in the agent's timezone; required when summary_enabled. */
  summary_time: string;
  /** IANA time zone, e.g. "Asia/Shanghai"; required when summary_enabled. */
  timezone: string;
}

/**
 * A user's review decision on an issue, pinned to the exact PR head it was
 * made against. Decisions are tracking state: they never grant merge or
 * deploy rights, and a newer decision supersedes earlier unresolved ones.
 */
export interface ReviewDecision {
  id: string;
  issue_id: string;
  /** Null when the decision covers the issue without a linked PR. */
  pull_request_id: string | null;
  head_sha: string;
  decision: "approved" | "changes_requested" | "deferred";
  note: string | null;
  decided_by: string;
  created_at: string;
  superseded_at?: string | null;
  resolved_at?: string | null;
  /**
   * True when the decision pins a PR head that no longer matches the linked
   * PR's current snapshot — the call was made against outdated evidence.
   */
  stale: boolean;
}

export interface ListReviewDecisionsResponse {
  decisions: ReviewDecision[];
}

export const EMPTY_REVIEW_DECISIONS_RESPONSE: ListReviewDecisionsResponse = {
  decisions: [],
};
