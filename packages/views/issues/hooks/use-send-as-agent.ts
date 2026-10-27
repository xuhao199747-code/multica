"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useAuthStore } from "@multica/core/auth";
import { useCurrentWorkspace } from "@multica/core/paths";
import { agentListOptions, memberListOptions } from "@multica/core/workspace/queries";
import { canAssignAgentToIssue } from "@multica/core/permissions";
import { isAgentRuntimeBound } from "@multica/core/agents";
import { isMikaAgent } from "@multica/core/onboarding";
import { useCommentComposerStore } from "@multica/core/issues/stores";
import type { Agent } from "@multica/core/types";

// Reads the current user's id without throwing when the auth store has not
// been registered (test environments, peek surfaces that render the composer
// before the app shell boots). A missing user simply means "no per-user
// permission filtering" — the selector falls back to no choices.
function useMaybeUserId(): string | null {
  try {
    return useAuthStore((s) => s.user?.id) ?? null;
  } catch {
    return null;
  }
}

/**
 * A workspace agent the member may explicitly target from the composer.
 * `defaultAgent` labels the workspace default in the picker; it does not
 * replace normal comment routing until the member selects it.
 */
export interface SendAsChoice {
  agent: Agent;
  isDefault: boolean;
}

export interface SendAsState {
  /** Workspace default (Mika) — what a fresh selector preselects. */
  defaultAgent: Agent | null;
  /** All visible, runtime-bound agents (may be just Mika). */
  choices: Agent[];
  /** Explicit target, or an empty string while the existing routing is used. */
  selectedId: string;
  /** The full mention link injected for the explicit target. */
  selectedMention: string | null;
  hasChoice: boolean;
  setSelectedId: (id: string | null) => void;
}

function mentionLinkFor(agent: Agent): string {
  const name = agent.name.replace(/[[\]\\]/g, "\\$&");
  return `[@${name}](mention://agent/${agent.id})`;
}

/**
 * Powers the "send as agent" selector in the comment composers.
 *
 * Trigger model: the server routes comment triggers off `mention://agent/…`
 * links, and an implicit-mention injection has zero backend surface — the
 * selector prepends a mention link only after the member chooses an agent.
 * Explicit mentions are exclusive on the server, so an explicit selection
 * must be represented in the submitted payload. The unselected state keeps
 * the issue/thread's established routing unchanged.
 *
 * Visibility mirrors AssigneePicker / chat's availability hook: archived,
 * runtime-unbound, or non-invokable (private, non-owned) agents never render.
 * An unbound Mika (never bound / runtime deleted) is not offered either — a
 * mention of it would be a guaranteed no-op, so the selector hides instead
 * of offering a broken default.
 */
export function useSendAsAgent(): SendAsState {
  // Deliberately tolerant of missing workspace context (peek surfaces render
  // the composer outside a workspace route; tests render it bare). Without a
  // resolved workspace the selector degrades to "no choices" instead of
  // crashing the composer.
  const ws = useCurrentWorkspace();
  const wsId = ws?.id ?? "";
  const userId = useMaybeUserId();
  const { data: agents } = useQuery({ ...agentListOptions(wsId), enabled: !!wsId });
  const { data: members } = useQuery({ ...memberListOptions(wsId), enabled: !!wsId });

  const rawRole = members?.find((m) => m.user_id === userId)?.role;
  const role =
    rawRole === "owner" || rawRole === "admin" || rawRole === "member"
      ? rawRole
      : null;

  const choices = useMemo(() => {
    const list = agents ?? [];
    return list.filter(
      (a) =>
        !a.archived_at &&
        isAgentRuntimeBound(a) &&
        canAssignAgentToIssue(a, { userId: userId ?? null, role }).allowed,
    );
  }, [agents, userId, role]);

  const defaultAgent = useMemo(() => {
    const mika = choices.find(isMikaAgent);
    return mika ?? choices[0] ?? null;
  }, [choices]);

  const persisted = useCommentComposerStore((s) => s.sendAsByWorkspace[wsId]);
  const setSendAs = useCommentComposerStore((s) => s.setSendAs);

  // A persisted pick survives only while the agent stays visible. No pick is
  // intentionally distinct from the workspace default: it preserves legacy
  // assignee and conversation routing until the member opts in.
  const initial = persisted && choices.some((c) => c.id === persisted) ? persisted : null;

  const [selectedId, setSelectedId] = useState<string | null>(initial);

  // Reconcile against this workspace's persisted explicit target whenever the
  // workspace or visible agents change. Peek composers do not always remount
  // on workspace navigation, so retaining a prior target can route a comment
  // to an agent from the wrong workspace.
  useEffect(() => {
    const next = persisted && choices.some((c) => c.id === persisted)
      ? persisted
      : null;
    setSelectedId((previous) => (previous === next ? previous : next));
  }, [wsId, persisted, choices]);

  const handleSelect = useCallback(
    (id: string | null) => {
      setSelectedId(id);
      setSendAs(wsId, id);
    },
    [setSendAs, wsId],
  );

  const resolved = choices.find((c) => c.id === selectedId) ?? null;
  const hasChoice = choices.length > 1 && defaultAgent !== null;

  return {
    defaultAgent,
    choices,
    selectedId: resolved?.id ?? "",
    selectedMention: resolved ? mentionLinkFor(resolved) : null,
    hasChoice,
    setSelectedId: handleSelect,
  };
}
