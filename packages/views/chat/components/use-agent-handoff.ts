import { useCallback, useEffect, useMemo, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { useCreateChatSession } from "@multica/core/chat/mutations";
import { chatKeys } from "@multica/core/chat/queries";
import type {
  Agent,
  ChatMessage,
  ChatSession,
  PendingChatTasksResponse,
} from "@multica/core/types";
import {
  buildAgentHandoffRequest,
  resolveAgentHandoff,
} from "../lib/agent-handoff";
import { deriveChatTitle } from "./use-chat-controller";

type PendingSource = {
  sessionId: string;
  taskId: string;
  target: Agent;
};

export type AgentHandoffDestination =
  | { kind: "new" }
  | { kind: "existing"; sessionId: string };

type HandoffDialogState = {
  sourceSessionId: string;
  target: Agent;
  draft: string;
  error: string | null;
};

export type AgentHandoffDialog = HandoffDialogState & {
  sessions: ChatSession[];
  busySessionIds: Set<string>;
};

export type AgentHandoffError = "source_failed" | "source_no_response" | null;

export function useAgentHandoff({
  wsId,
  activeSessionId,
  messages,
  sessions,
  pendingTasks,
  eligibleAgents,
}: {
  wsId: string;
  activeSessionId: string | null;
  messages: readonly ChatMessage[];
  sessions: readonly ChatSession[];
  pendingTasks: PendingChatTasksResponse | undefined;
  eligibleAgents: readonly Agent[];
}) {
  const queryClient = useQueryClient();
  const createChatSession = useCreateChatSession();
  const [pendingSource, setPendingSource] = useState<PendingSource | null>(null);
  const [dialogState, setDialogState] = useState<HandoffDialogState | null>(null);
  const [error, setError] = useState<AgentHandoffError>(null);
  const [isDelivering, setIsDelivering] = useState(false);

  const prepareOutgoing = useCallback(
    (content: string, sourceAgentId: string) => {
      const handoff = resolveAgentHandoff(content, eligibleAgents, sourceAgentId);
      if (handoff.kind !== "ready") return { content, target: null };
      return {
        content: buildAgentHandoffRequest(content, handoff.target.name),
        target: handoff.target,
      };
    },
    [eligibleAgents],
  );

  const registerAcceptedSource = useCallback((source: PendingSource) => {
    setError(null);
    setDialogState(null);
    setPendingSource(source);
  }, []);

  useEffect(() => {
    const sourceSessionId = pendingSource?.sessionId ?? dialogState?.sourceSessionId;
    if (!sourceSessionId || activeSessionId === sourceSessionId) return;
    setPendingSource(null);
    setDialogState(null);
    setError(null);
  }, [activeSessionId, dialogState?.sourceSessionId, pendingSource]);

  useEffect(() => {
    if (!pendingSource || activeSessionId !== pendingSource.sessionId) return;
    const response = messages.find(
      (message) => message.role === "assistant" && message.task_id === pendingSource.taskId,
    );
    if (!response) return;

    setPendingSource(null);
    if (response.failure_reason) {
      setError("source_failed");
      return;
    }
    if (response.message_kind === "no_response") {
      setError("source_no_response");
      return;
    }
    setDialogState({
      sourceSessionId: pendingSource.sessionId,
      target: pendingSource.target,
      draft: response.content,
      error: null,
    });
  }, [activeSessionId, messages, pendingSource]);

  const dialog = useMemo<AgentHandoffDialog | null>(() => {
    if (!dialogState) return null;
    const destinationSessions = sessions.filter(
      (session) => session.status === "active" && session.agent_id === dialogState.target.id,
    );
    return {
      ...dialogState,
      sessions: destinationSessions,
      busySessionIds: new Set(
        (pendingTasks?.tasks ?? []).map((task) => task.chat_session_id),
      ),
    };
  }, [dialogState, pendingTasks, sessions]);

  const closeDialog = useCallback(() => {
    setDialogState(null);
    setError(null);
  }, []);

  const deliver = useCallback(async (
    content: string,
    destination: AgentHandoffDestination,
  ) => {
    if (!content.trim()) throw new Error("Handoff content cannot be blank");
    if (!dialogState) throw new Error("No handoff is ready for delivery");

    setIsDelivering(true);
    try {
      const sessionId = destination.kind === "new"
        ? (await createChatSession.mutateAsync({
          agent_id: dialogState.target.id,
          title: deriveChatTitle(content),
        })).id
        : destination.sessionId;
      await api.sendChatMessage(sessionId, content);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: chatKeys.sessions(wsId) }),
        queryClient.invalidateQueries({ queryKey: chatKeys.messages(sessionId) }),
        queryClient.invalidateQueries({ queryKey: chatKeys.messagesPage(sessionId) }),
        queryClient.invalidateQueries({ queryKey: chatKeys.pendingTask(sessionId) }),
        queryClient.invalidateQueries({ queryKey: chatKeys.pendingTasks(wsId) }),
      ]);
      setDialogState(null);
    } catch (cause) {
      setDialogState((current) => current
        ? { ...current, error: cause instanceof Error ? cause.message : "Unable to deliver handoff" }
        : current);
      throw cause;
    } finally {
      setIsDelivering(false);
    }
  }, [createChatSession, dialogState, queryClient, wsId]);

  return {
    prepareOutgoing,
    registerAcceptedSource,
    dialog,
    closeDialog,
    deliver,
    error,
    isDelivering,
  };
}
