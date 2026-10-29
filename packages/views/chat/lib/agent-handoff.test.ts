// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { Agent } from "@multica/core/types";
import {
  buildAgentHandoffRequest,
  isStructuredHandoffDraft,
  resolveAgentHandoff,
} from "./agent-handoff";

const agents: Pick<Agent, "id" | "name">[] = [
  { id: "prd", name: "PRD Agent" },
  { id: "code", name: "Code Agent" },
];

describe("resolveAgentHandoff", () => {
  it("ignores plain text and non-Agent mentions", () => {
    expect(resolveAgentHandoff("Ask @Code or [@Sam](mention://member/sam)", agents, "prd"))
      .toEqual({ kind: "none" });
  });

  it("uses one persisted Agent mention as target", () => {
    expect(resolveAgentHandoff(
      "[@Code Agent](mention://agent/code)", agents, "prd",
    )).toEqual({ kind: "ready", target: agents[1] });
  });

  it("rejects two Agent targets without a source send", () => {
    expect(resolveAgentHandoff(
      "[@PRD](mention://agent/prd) [@Code](mention://agent/code)", agents, "prd",
    )).toEqual({ kind: "multiple" });
  });

  it("rejects repeated persisted Agent mentions as multiple", () => {
    expect(resolveAgentHandoff(
      "[@Code](mention://agent/code) [@Code](mention://agent/code)", agents, "prd",
    )).toEqual({ kind: "multiple" });
  });

  it("rejects a self mention", () => {
    expect(resolveAgentHandoff("[@PRD](mention://agent/prd)", agents, "prd"))
      .toEqual({ kind: "self" });
  });

  it("rejects an Agent mention outside the eligible list", () => {
    expect(resolveAgentHandoff("[@Offline](mention://agent/offline)", agents, "prd"))
      .toEqual({ kind: "unavailable" });
  });
});

describe("buildAgentHandoffRequest", () => {
  it("appends the required handoff instruction", () => {
    expect(buildAgentHandoffRequest("Please hand this off.", "Code Agent")).toBe(
      `Please hand this off.

请基于本次对话整理一份可直接交给 Code Agent 的交接内容。只输出以下字段：
目标：
已确认的需求：
需要实现：
不要改：
验收条件：
参考位置：
不确定项：

没有依据时写“不确定”，不要编造。不要写普通回复或寒暄。`,
    );
  });

  it("inserts target names containing replacement tokens literally", () => {
    expect(buildAgentHandoffRequest("Please hand this off.", "Code $& Agent"))
      .toContain("可直接交给 Code $& Agent 的交接内容");
  });
});

describe("isStructuredHandoffDraft", () => {
  it("accepts every trimmed required field label", () => {
    expect(isStructuredHandoffDraft(`
      目标：完成交接
      已确认的需求：已确认
      需要实现：功能
      不要改：范围
      验收条件：测试通过
      参考位置：packages/views/chat
      不确定项：不确定
    `)).toBe(true);
  });

  it("rejects malformed drafts missing a required label", () => {
    expect(isStructuredHandoffDraft(`
      目标：完成交接
      已确认的需求：已确认
      需要实现：功能
      不要改：范围
      验收条件：测试通过
      参考位置：packages/views/chat
    `)).toBe(false);
  });
});
