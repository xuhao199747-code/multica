/**
 * Specs — workspace-scoped requirement/design documents with versioned
 * content. A spec starts life as a `draft`, gains numbered `spec_version`
 * rows as its content is published, and points at the latest one through
 * `current_version_id`.
 *
 * Status stays a plain string: the server CHECK-constrains it to
 * draft | in_review | approved | deprecated, but clients must tolerate
 * unknown values from newer backends.
 */
export interface SpecVersion {
  id: string;
  number: number;
  content: string;
  changelog: string;
  created_by: string;
  created_at: string;
}

/**
 * List-endpoint shape (`GET /api/specs`). Deliberately carries no version
 * content; fetch the detail endpoint for `current_version`.
 */
export interface SpecSummary {
  id: string;
  workspace_id: string;
  /** Project the spec belongs to, null when unassigned. */
  project_id: string | null;
  title: string;
  /** Human-readable key minted from the workspace issue prefix, e.g. `MUL-S-1`. */
  identifier: string;
  status: string;
  current_version_id: string | null;
  current_version_number?: number;
  created_at: string;
  updated_at: string;
}

/** Detail shape (`GET /api/specs/:id`): summary plus the resolved current version. */
export interface SpecDetail extends SpecSummary {
  /** Null while the spec has no published versions. */
  current_version: SpecVersion | null;
}

/** Spec summary embedded in issue responses: enough to render a link chip. */
export interface SpecLink {
  id: string;
  identifier: string;
  title: string;
  status: string;
}

export interface SpecListResponse {
  specs: SpecSummary[];
  total: number;
}

/** One linked issue as returned by the spec-side link endpoints. */
export interface SpecIssueRef {
  id: string;
  identifier: string;
  title: string;
  status: string;
}

export interface SpecIssueListResponse {
  issues: SpecIssueRef[];
  total: number;
}

/** One spec subscription. Spec subscriptions are member-only. */
export interface SpecSubscriber {
  spec_id: string;
  user_id: string;
  created_at: string;
}

export interface SpecSubscriberListResponse {
  subscribers: SpecSubscriber[];
  total: number;
}

export interface CreateSpecRequest {
  title: string;
  project_id?: string;
}

export interface UpdateSpecRequest {
  title?: string;
  /** Omit to leave membership unchanged; pass null to clear it. */
  project_id?: string | null;
}
