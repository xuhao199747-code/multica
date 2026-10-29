import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Agent, ChatMessage, ChatSession, PendingChatTasksResponse } from "@multica/core/types";
import { api } from "@multica/core/api";
import { chatKeys } from "@multica/core/chat/queries";
import { useAgentHandoff } from "./use-agent-handoff";

const h = vi.hoisted(() => ({
  createSession: vi.fn(),
}));

vi.mock("@multica/core/chat/mutations", () => ({
  useCreateChatSession: () => ({ mutateAsync: h.createSession }),
}));

vi.mock("@multica/core/api", () => ({
  api: { sendChatMessage: vi.fn() },
}));

const sourceAgent = { id: "agent-a", name: "Analyst" } as Agent;
const codeAgent = { id: "agent-b", name: "Code" } as Agent;

function assistant(overrides: Partial<ChatMessage> = {}): ChatMessage {
  return {
    id: "assistant-message",
    chat_session_id: "a-chat",
    role: "assistant",
    content: "目标：\n交给 Code 实现。",
    task_id: "task-a",
    created_at: "2026-10-29T00:00:00Z",
    ...overrides,
  };
}

function session(overrides: Partial<ChatSession> = {}): ChatSession {
  return {
    id: "b-chat",
    workspace_id: "ws-1",
    agent_id: codeAgent.id,
    creator_id: "user-1",
    title: "Existing code work",
    status: "active",
    has_unread: false,
    created_at: "2026-10-28T00:00:00Z",
    updated_at: "2026-10-29T00:00:00Z",
    ...overrides,
  };
}

function renderAgentHandoff(overrides: Partial<Parameters<typeof useAgentHandoff>[0]> = {}) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const props = {
    wsId: "ws-1",
    activeSessionId: "a-chat",
    messages: [] as ChatMessage[],
    sessions: [] as ChatSession[],
    pendingTasks: { tasks: [] } as PendingChatTasksResponse,
    eligibleAgents: [sourceAgent, codeAgent],
    ...overrides,
  };
  const wrapper = ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  );
  return {
    queryClient,
    ...renderHook((nextProps: typeof props) => useAgentHandoff(nextProps), {
      initialProps: props,
      wrapper,
    }),
  };
}

describe("useAgentHandoff", () => {
  beforeEach(() => {
    h.createSession.mockReset();
    vi.mocked(api.sendChatMessage).mockReset();
  });

  it("opens only for the assistant reply for the registered A task", async () => {
    const { result, rerender } = renderAgentHandoff();
    act(() => {
      result.current.registerAcceptedSource({
        sessionId: "a-chat",
        taskId: "task-a",
        target: codeAgent,
      });
    });

    rerender({
      wsId: "ws-1",
      activeSessionId: "a-chat",
      messages: [assistant({ task_id: "other", content: "ignore" })],
      sessions: [],
      pendingTasks: { tasks: [] },
      eligibleAgents: [sourceAgent, codeAgent],
    });
    expect(result.current.dialog).toBeNull();

    rerender({
      wsId: "ws-1",
      activeSessionId: "a-chat",
      messages: [assistant({ chat_session_id: "other-chat", content: "ignore" })],
      sessions: [],
      pendingTasks: { tasks: [] },
      eligibleAgents: [sourceAgent, codeAgent],
    });
    expect(result.current.dialog).toBeNull();

    const draft = "目标：\n交给 Code 实现。";
    rerender({
      wsId: "ws-1",
      activeSessionId: "a-chat",
      messages: [assistant({ content: draft })],
      sessions: [],
      pendingTasks: { tasks: [] },
      eligibleAgents: [sourceAgent, codeAgent],
    });
    await waitFor(() => expect(result.current.dialog?.draft).toBe(draft));
  });

  it("reports a source failure or no-response without opening delivery", async () => {
    const { result, rerender } = renderAgentHandoff();
    act(() => result.current.registerAcceptedSource({ sessionId: "a-chat", taskId: "task-a", target: codeAgent }));
    rerender({
      wsId: "ws-1", activeSessionId: "a-chat", messages: [assistant({ failure_reason: "agent_error" })],
      sessions: [], pendingTasks: { tasks: [] }, eligibleAgents: [sourceAgent, codeAgent],
    });
    await waitFor(() => expect(result.current.error).toBe("source_failed"));
    expect(result.current.dialog).toBeNull();

    rerender({
      wsId: "ws-1", activeSessionId: "a-chat", messages: [],
      sessions: [], pendingTasks: { tasks: [] }, eligibleAgents: [sourceAgent, codeAgent],
    });
    act(() => result.current.registerAcceptedSource({ sessionId: "a-chat", taskId: "task-a", target: codeAgent }));
    rerender({
      wsId: "ws-1", activeSessionId: "a-chat", messages: [assistant({ message_kind: "no_response" })],
      sessions: [], pendingTasks: { tasks: [] }, eligibleAgents: [sourceAgent, codeAgent],
    });
    await waitFor(() => expect(result.current.error).toBe("source_no_response"));
  });

  it("clears a pending source and dialog when leaving A", async () => {
    const { result, rerender } = renderAgentHandoff();
    act(() => result.current.registerAcceptedSource({ sessionId: "a-chat", taskId: "task-a", target: codeAgent }));
    rerender({
      wsId: "ws-1", activeSessionId: "a-chat", messages: [assistant()], sessions: [],
      pendingTasks: { tasks: [] }, eligibleAgents: [sourceAgent, codeAgent],
    });
    await waitFor(() => expect(result.current.dialog).not.toBeNull());

    rerender({
      wsId: "ws-1", activeSessionId: "other-chat", messages: [assistant()], sessions: [],
      pendingTasks: { tasks: [] }, eligibleAgents: [sourceAgent, codeAgent],
    });
    await waitFor(() => expect(result.current.dialog).toBeNull());
  });

  it("cancels delivery without retaining the dialog", async () => {
    const { result, rerender } = renderAgentHandoff();
    act(() => result.current.registerAcceptedSource({ sessionId: "a-chat", taskId: "task-a", target: codeAgent }));
    rerender({
      wsId: "ws-1", activeSessionId: "a-chat", messages: [assistant()], sessions: [],
      pendingTasks: { tasks: [] }, eligibleAgents: [sourceAgent, codeAgent],
    });
    await waitFor(() => expect(result.current.dialog).not.toBeNull());
    act(() => result.current.closeDialog());
    expect(result.current.dialog).toBeNull();
  });

  it("offers only active B conversations and marks busy destinations", async () => {
    const { result, rerender } = renderAgentHandoff({
      sessions: [session(), session({ id: "b-archived", status: "archived" }), session({ id: "a-other", agent_id: sourceAgent.id })],
      pendingTasks: { tasks: [{ task_id: "busy-task", status: "running", chat_session_id: "b-chat" }] },
    });
    act(() => result.current.registerAcceptedSource({ sessionId: "a-chat", taskId: "task-a", target: codeAgent }));
    rerender({
      wsId: "ws-1", activeSessionId: "a-chat", messages: [assistant()],
      sessions: [session(), session({ id: "b-archived", status: "archived" }), session({ id: "a-other", agent_id: sourceAgent.id })],
      pendingTasks: { tasks: [{ task_id: "busy-task", status: "running", chat_session_id: "b-chat" }] },
      eligibleAgents: [sourceAgent, codeAgent],
    });
    await waitFor(() => expect(result.current.dialog?.availableSessions).toEqual([session()]));
    expect(result.current.dialog?.busySessionIds).toEqual(new Set(["b-chat"]));
  });

  it("prepares only ready agent mentions for an outgoing source request", () => {
    const { result } = renderAgentHandoff();
    expect(result.current.prepareOutgoing("Please ask [@Code](mention://agent/agent-b)", sourceAgent.id))
      .toEqual(expect.objectContaining({ target: codeAgent, content: expect.stringContaining("只输出以下字段") }));
    expect(result.current.prepareOutgoing("No handoff", sourceAgent.id)).toEqual({ content: "No handoff", target: null });
  });

  it("creates a new B conversation then delivers the draft", async () => {
    h.createSession.mockResolvedValue({ id: "new-b-chat" });
    vi.mocked(api.sendChatMessage).mockResolvedValue({} as Awaited<ReturnType<typeof api.sendChatMessage>>);
    const { result, rerender, queryClient } = renderAgentHandoff();
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");
    act(() => result.current.registerAcceptedSource({ sessionId: "a-chat", taskId: "task-a", target: codeAgent }));
    rerender({ wsId: "ws-1", activeSessionId: "a-chat", messages: [assistant()], sessions: [], pendingTasks: { tasks: [] }, eligibleAgents: [sourceAgent, codeAgent] });
    await waitFor(() => expect(result.current.dialog).not.toBeNull());

    expect(result.current.dialog?.destination).toEqual({ kind: "new" });
    act(() => result.current.setDraft("edited handoff"));
    await act(async () => result.current.deliver());
    expect(h.createSession).toHaveBeenCalledWith({ agent_id: codeAgent.id, title: "edited handoff" });
    expect(api.sendChatMessage).toHaveBeenCalledWith("new-b-chat", "edited handoff");
    expect(invalidate).toHaveBeenCalledWith({ queryKey: chatKeys.session("ws-1", "new-b-chat") });
    expect(result.current.dialog).toBeNull();
  });

  it("delivers to an existing B conversation without creating one", async () => {
    vi.mocked(api.sendChatMessage).mockResolvedValue({} as Awaited<ReturnType<typeof api.sendChatMessage>>);
    const { result, rerender } = renderAgentHandoff({ sessions: [session()] });
    act(() => result.current.registerAcceptedSource({ sessionId: "a-chat", taskId: "task-a", target: codeAgent }));
    rerender({ wsId: "ws-1", activeSessionId: "a-chat", messages: [assistant()], sessions: [session()], pendingTasks: { tasks: [] }, eligibleAgents: [sourceAgent, codeAgent] });
    await waitFor(() => expect(result.current.dialog).not.toBeNull());

    act(() => {
      result.current.setDraft("edited handoff");
      result.current.setDestination({ kind: "existing", sessionId: "b-chat" });
    });
    await act(async () => result.current.deliver());
    expect(h.createSession).not.toHaveBeenCalled();
    expect(api.sendChatMessage).toHaveBeenCalledWith("b-chat", "edited handoff");
  });

  it("rejects blank delivery and preserves dialog state after a delivery error", async () => {
    vi.mocked(api.sendChatMessage).mockRejectedValue(new Error("network down"));
    const { result, rerender } = renderAgentHandoff({ sessions: [session()] });
    act(() => result.current.registerAcceptedSource({ sessionId: "a-chat", taskId: "task-a", target: codeAgent }));
    rerender({ wsId: "ws-1", activeSessionId: "a-chat", messages: [assistant()], sessions: [session()], pendingTasks: { tasks: [] }, eligibleAgents: [sourceAgent, codeAgent] });
    await waitFor(() => expect(result.current.dialog).not.toBeNull());

    await act(async () => {
      result.current.setDestination({ kind: "existing", sessionId: "b-chat" });
      result.current.setDraft("   ");
    });
    await act(async () => {
      await expect(result.current.deliver()).rejects.toThrow("blank");
      result.current.setDraft("edited handoff");
    });
    await act(async () => {
      await expect(result.current.deliver()).rejects.toThrow("network down");
    });
    expect(result.current.dialog?.draft).toBe("edited handoff");
    expect(result.current.dialog?.destination).toEqual({ kind: "existing", sessionId: "b-chat" });
    expect(result.current.dialog?.error).toBe("network down");
  });

  it("rejects an unavailable or busy existing destination without sending", async () => {
    const { result, rerender } = renderAgentHandoff({
      sessions: [session()],
      pendingTasks: { tasks: [{ task_id: "busy-task", status: "running", chat_session_id: "b-chat" }] },
    });
    act(() => result.current.registerAcceptedSource({ sessionId: "a-chat", taskId: "task-a", target: codeAgent }));
    rerender({
      wsId: "ws-1", activeSessionId: "a-chat", messages: [assistant()], sessions: [session()],
      pendingTasks: { tasks: [{ task_id: "busy-task", status: "running", chat_session_id: "b-chat" }] },
      eligibleAgents: [sourceAgent, codeAgent],
    });
    await waitFor(() => expect(result.current.dialog).not.toBeNull());

    act(() => result.current.setDestination({ kind: "existing", sessionId: "other-agent-chat" }));
    await act(async () => expect(result.current.deliver()).rejects.toThrow("unavailable"));
    expect(api.sendChatMessage).not.toHaveBeenCalled();
    expect(result.current.dialog?.error).toContain("unavailable");

    act(() => result.current.setDestination({ kind: "existing", sessionId: "b-chat" }));
    await act(async () => expect(result.current.deliver()).rejects.toThrow("busy"));
    expect(api.sendChatMessage).not.toHaveBeenCalled();
    expect(result.current.dialog?.error).toContain("busy");
  });
});
