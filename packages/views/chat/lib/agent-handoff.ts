import type { Agent } from "@multica/core/types";

export type HandoffAgent = Pick<Agent, "id" | "name">;

export type AgentHandoff =
  | { kind: "none" }
  | { kind: "ready"; target: HandoffAgent }
  | { kind: "multiple" }
  | { kind: "self" }
  | { kind: "unavailable" };

const AGENT_MENTION_RE = /\[[^\]]*\]\(mention:\/\/agent\/([^\s)]+)\)/g;

const HANDOFF_INSTRUCTION = `请基于本次对话整理一份可直接交给 \${targetName} 的交接内容。只输出以下字段：
目标：
已确认的需求：
需要实现：
不要改：
验收条件：
参考位置：
不确定项：

没有依据时写“不确定”，不要编造。不要写普通回复或寒暄。`;

const HANDOFF_LABELS = [
  "目标：",
  "已确认的需求：",
  "需要实现：",
  "不要改：",
  "验收条件：",
  "参考位置：",
  "不确定项：",
];

export function resolveAgentHandoff(
  content: string,
  eligibleAgents: readonly HandoffAgent[],
  sourceAgentId: string,
): AgentHandoff {
  const targetIds = [...content.matchAll(AGENT_MENTION_RE)].map((match) => match[1]);

  if (targetIds.length === 0) return { kind: "none" };
  if (targetIds.length > 1) return { kind: "multiple" };

  const targetId = targetIds[0];
  if (targetId === sourceAgentId) return { kind: "self" };

  const target = eligibleAgents.find((agent) => agent.id === targetId);
  return target ? { kind: "ready", target } : { kind: "unavailable" };
}

export function buildAgentHandoffRequest(content: string, targetName: string): string {
  return `${content}\n\n${HANDOFF_INSTRUCTION.replace("${targetName}", () => targetName)}`;
}

export function isStructuredHandoffDraft(content: string): boolean {
  const lines = content.split(/\r?\n/).map((line) => line.trim());
  return HANDOFF_LABELS.every((label) => lines.some((line) => line.startsWith(label)));
}
