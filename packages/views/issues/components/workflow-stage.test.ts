// @vitest-environment node
import { describe, expect, it } from "vitest";
import { workflowStage } from "./workflow-stage";

describe("workflowStage", () => {
  it("shows review ownership for an issue waiting for review", () => {
    expect(workflowStage("in_review")).toEqual({ label: "等待审核", next: "审核智能体" });
  });
});
