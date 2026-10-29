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
  destination: AgentHandoffDestination;
  error: AgentHandoffDeliveryError | null;
  deliverySession: DeliverySession | null;
};

type DeliverySession = {
  id: string;
  title: string;
};

export type AgentHandoffDialog = HandoffDialogState & {
  availableSessions: ChatSession[];
  busySessionIds: Set<string>;
  targetAvailable: boolean;
};

export type AgentHandoffError = "source_failed" | "source_no_response" | null;
type AgentHandoffDeliveryError =
  | "blank"
  | "destination_unavailable"
  | "destination_busy"
  | "delivery_failed"
  | "delivery_ambiguous";
export type AgentHandoffFeedback =
  | { kind: "source_failed" | "source_no_response" }
  | { kind: "delivered"; targetName: string; conversationTitle: string }
  | null;

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
  const [feedback, setFeedback] = useState<AgentHandoffFeedback>(null);
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
    setFeedback(null);
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
      (message) =>
        message.role === "assistant" &&
        message.task_id === pendingSource.taskId &&
        message.chat_session_id === pendingSource.sessionId,
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
      destination: { kind: "new" },
      error: null,
      deliverySession: null,
    });
  }, [activeSessionId, messages, pendingSource]);

  const availableSessions = useMemo(
    () => !dialogState ? [] : sessions.filter(
      (session) => session.status === "active" && session.agent_id === dialogState.target.id,
    ),
    [dialogState, sessions],
  );
  const targetAvailable = !!dialogState && eligibleAgents.some(
    (agent) => agent.id === dialogState.target.id,
  );
  const busySessionIds = useMemo(
    () => new Set((pendingTasks?.tasks ?? []).map((task) => task.chat_session_id)),
    [pendingTasks],
  );
  const dialog = useMemo<AgentHandoffDialog | null>(() => {
    if (!dialogState) return null;
    return {
      ...dialogState,
      availableSessions,
      busySessionIds,
      targetAvailable,
    };
  }, [availableSessions, busySessionIds, dialogState, targetAvailable]);

  const closeDialog = useCallback(() => {
    setDialogState(null);
    setError(null);
  }, []);

  const setDraft = useCallback((draft: string) => {
    setDialogState((current) => current ? { ...current, draft, error: null } : current);
  }, []);

  const setDestination = useCallback((destination: AgentHandoffDestination) => {
    setDialogState((current) => current ? { ...current, destination, error: null } : current);
  }, []);

  const deliver = useCallback(async () => {
    if (!dialogState) throw new Error("No handoff is ready for delivery");
    const destination = dialogState.destination;
    const failDelivery = (message: AgentHandoffDeliveryError): never => {
      setDialogState((current) => current ? { ...current, error: message } : current);
      throw new Error(message);
    };
    if (!dialogState.draft.trim()) failDelivery("blank");
    if (!targetAvailable) failDelivery("destination_unavailable");
    if (destination.kind === "existing") {
      const isAvailable = availableSessions.some(
        (session) => session.id === destination.sessionId,
      );
      if (!isAvailable) failDelivery("destination_unavailable");
      if (busySessionIds.has(destination.sessionId)) {
        failDelivery("destination_busy");
      }
    }

    setIsDelivering(true);
    let deliverySession: DeliverySession | null = null;
    try {
      const sessionId = destination.kind === "new"
        ? (await createChatSession.mutateAsync({
          agent_id: dialogState.target.id,
          title: deriveChatTitle(dialogState.draft),
        })).id
        : destination.sessionId;
      deliverySession = destination.kind === "new"
        ? { id: sessionId, title: deriveChatTitle(dialogState.draft) }
        : (() => {
          const session = availableSessions.find((candidate) => candidate.id === sessionId);
          return { id: sessionId, title: session?.title ?? sessionId };
        })();
      setDialogState((current) => current ? { ...current, deliverySession } : current);
      await api.sendChatMessage(sessionId, dialogState.draft);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: chatKeys.sessions(wsId) }),
        queryClient.invalidateQueries({ queryKey: chatKeys.session(wsId, sessionId) }),
        queryClient.invalidateQueries({ queryKey: chatKeys.messages(sessionId) }),
        queryClient.invalidateQueries({ queryKey: chatKeys.messagesPage(sessionId) }),
        queryClient.invalidateQueries({ queryKey: chatKeys.pendingTask(sessionId) }),
        queryClient.invalidateQueries({ queryKey: chatKeys.pendingTasks(wsId) }),
      ]);
      setFeedback({
        kind: "delivered",
        targetName: dialogState.target.name,
        conversationTitle: deliverySession.title,
      });
      setDialogState(null);
    } catch (cause) {
      const deliveryError: AgentHandoffDeliveryError = deliverySession
        ? "delivery_ambiguous"
        : "delivery_failed";
      setDialogState((current) => current
        ? { ...current, error: deliveryError, deliverySession: deliverySession ?? current.deliverySession }
        : current);
      throw new Error(deliveryError);
    } finally {
      setIsDelivering(false);
    }
  }, [availableSessions, busySessionIds, createChatSession, dialogState, queryClient, targetAvailable, wsId]);

  const clearFeedback = useCallback(() => setFeedback(null), []);
  const clearError = useCallback(() => setError(null), []);

  return {
    prepareOutgoing,
    registerAcceptedSource,
    pendingSource,
    dialog,
    closeDialog,
    setDraft,
    setDestination,
    deliver,
    error,
    clearError,
    feedback,
    clearFeedback,
    isDelivering,
  };
}
