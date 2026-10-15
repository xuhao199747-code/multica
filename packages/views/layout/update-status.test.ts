import { describe, expect, it } from "vitest";
import { classifyUpstreamUpdate } from "./update-status";

describe("classifyUpstreamUpdate", () => {
  it("marks the installation current when the custom branch is based on the latest upstream commit", () => {
    expect(
      classifyUpstreamUpdate("upstream-sha", { sha: "custom-sha", parents: [{ sha: "upstream-sha" }] }),
    ).toBe("current");
  });

  it("marks an update available when upstream has advanced beyond the custom branch base", () => {
    expect(
      classifyUpstreamUpdate("new-upstream-sha", { sha: "custom-sha", parents: [{ sha: "old-upstream-sha" }] }),
    ).toBe("update-available");
  });

  it("keeps the state unknown when GitHub does not provide a base commit", () => {
    expect(classifyUpstreamUpdate("upstream-sha", { sha: "custom-sha", parents: [] })).toBe("unknown");
  });
});
