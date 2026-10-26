export interface ProjectWorkflowPlanConfirmation {
  title?: string;
  snapshot: Record<string, unknown>;
}

const PLAN_CONFIRMATION_PATTERN =
  /<!--\s*multica-project-workflow-plan-confirmation\s*\n?([\s\S]*?)\s*-->/g;

function isNonEmptyString(value: unknown): value is string {
  return typeof value === "string" && value.trim().length > 0;
}

function isStringArray(value: unknown, requireItem: boolean): value is string[] {
  return Array.isArray(value)
    && (!requireItem || value.length > 0)
    && value.every(isNonEmptyString);
}

function isCompletePlanSnapshot(value: unknown): value is Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) return false;
  const snapshot = value as Record<string, unknown>;
  return isNonEmptyString(snapshot.goal)
    && isStringArray(snapshot.scope, true)
    && isStringArray(snapshot.acceptance_criteria, true)
    && isStringArray(snapshot.risks, false);
}

/**
 * Reads the narrow, versioned confirmation payload emitted beneath a project
 * workflow plan. The marker intentionally stays in message content so the
 * server can re-validate the snapshot before it starts an execution.
 */
export function extractProjectWorkflowPlanConfirmation(
  content: string,
): ProjectWorkflowPlanConfirmation | null {
  const matches = [...content.matchAll(PLAN_CONFIRMATION_PATTERN)];
  if (matches.length !== 1) return null;

  try {
    const payload = JSON.parse(matches[0]?.[1] ?? "") as Record<string, unknown>;
    if (payload.version !== 1 || !isCompletePlanSnapshot(payload.snapshot)) return null;
    if (payload.title !== undefined && !isNonEmptyString(payload.title)) return null;
    return {
      ...(typeof payload.title === "string" ? { title: payload.title.trim() } : {}),
      snapshot: payload.snapshot,
    };
  } catch {
    return null;
  }
}

/** Hides machine confirmation data while preserving the visible plan prose. */
export function stripProjectWorkflowPlanConfirmationProtocol(content: string): string {
  return content.replace(PLAN_CONFIRMATION_PATTERN, "").trim();
}
