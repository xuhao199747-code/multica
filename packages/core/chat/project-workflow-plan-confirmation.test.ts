// @vitest-environment node

import { describe, expect, it } from "vitest";
import {
  extractProjectWorkflowPlanConfirmation,
  stripProjectWorkflowPlanConfirmationProtocol,
} from "./project-workflow-plan-confirmation";

const SNAPSHOT = {
  goal: "Make plan confirmation message-bound",
  scope: ["chat UI", "workflow start API"],
  acceptance_criteria: ["Only a complete plan can start execution"],
  risks: ["Old clients can send stale payloads"],
};

function confirmation(content: unknown): string {
  return [
    "The proposed plan is ready for confirmation.",
    "<!-- multica-project-workflow-plan-confirmation",
    JSON.stringify({ version: 1, title: "Message-bound execution", snapshot: content }),
    "-->",
  ].join("\n");
}

describe("project workflow plan confirmation protocol", () => {
  it("extracts the exact complete snapshot from one valid assistant plan marker", () => {
    const content = confirmation(SNAPSHOT);

    expect(extractProjectWorkflowPlanConfirmation(content)).toEqual({
      title: "Message-bound execution",
      snapshot: SNAPSHOT,
    });
  });

  it("rejects a marker that omits a required complete-plan field", () => {
    expect(extractProjectWorkflowPlanConfirmation(confirmation({
      goal: SNAPSHOT.goal,
      scope: SNAPSHOT.scope,
      acceptance_criteria: SNAPSHOT.acceptance_criteria,
    }))).toBeNull();
  });

  it("removes the machine confirmation marker from rendered content", () => {
    const content = confirmation(SNAPSHOT);

    expect(stripProjectWorkflowPlanConfirmationProtocol(content)).toBe(
      "The proposed plan is ready for confirmation.",
    );
  });
});
