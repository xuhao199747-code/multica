import React from "react";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Agent, ChatMessage, ChatSession, PendingChatTasksResponse } from "@multica/core/types";

const h = vi.hoisted(() => {
  const store = {
    isOpen: true,
    isExpanded: false,
    activeSessionId: "a-chat" as string | null,
    selectedAgentId: "agent-a" as string | null,
    selectedProjectId: null as string | null,
    setOpen: vi.fn(),
    setActiveSession: vi.fn((id: string | null) => { store.activeSessionId = id; }),
    setSelectedAgentId: vi.fn((id: string | null) => { store.selectedAgentId = id; }),
    setSelectedProjectId: vi.fn(),
  };
  return {
    store,
    agents: [] as Agent[],
    sessions: [] as ChatSession[],
    messages: [] as ChatMessage[],
    pendingTasks: { tasks: [] } as PendingChatTasksResponse,
    createSession: vi.fn(),
    queryClient: {
      getQueryData: vi.fn(),
      setQueryData: vi.fn(),
      invalidateQueries: vi.fn(),
    },
  };
});

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (state: { user: { id: string } }) => unknown) => selector({ user: { id: "user-1" } }),
}));
vi.mock("@multica/core/workspace/queries", () => ({
  agentListOptions: () => ({ queryKey: ["agents"] }),
  memberListOptions: () => ({ queryKey: ["members"] }),
}));
vi.mock("@multica/core/projects/queries", () => ({ projectListOptions: () => ({ queryKey: ["projects"] }) }));
vi.mock("../../issues/components/pickers/assignee-picker", () => ({ canAssignAgent: () => true }));
vi.mock("../../common/actor-avatar", () => ({ ActorAvatar: () => null }));
vi.mock("@multica/core/agents", () => ({
  isAgentRuntimeBound: () => true,
  useAgentPresenceDetail: () => "online",
  useCustomizeConversationStartersHref: () => null,
  useWorkspaceAgentAvailability: () => "available",
}));
vi.mock("@multica/core/api", () => ({ api: { sendChatMessage: vi.fn() }, dispatchReasonCode: () => undefined }));
vi.mock("@multica/core/chat/mutations", () => ({
  useCreateChatSession: () => ({ mutateAsync: h.createSession }),
  useMarkChatSessionRead: () => ({ mutate: vi.fn() }),
  useRegenerateChatQuickActions: () => ({ mutateAsync: vi.fn() }),
  useSetChatSessionArchived: () => ({ mutate: vi.fn() }),
  useSetChatSessionProject: () => ({ mutate: vi.fn(), isPending: false }),
  useUpdateChatSession: () => ({ mutateAsync: vi.fn() }),
}));
vi.mock("@multica/core/chat", () => ({
  useChatStore: Object.assign(
    (selector: (state: typeof h.store) => unknown) => selector(h.store),
    { getState: () => h.store },
  ),
}));
vi.mock("@multica/core/chat/message-cache", () => ({ upsertChatMessageToCaches: vi.fn() }));
vi.mock("@multica/core/logger", () => ({
  createLogger: () => ({ info: vi.fn(), warn: vi.fn(), error: vi.fn(), debug: vi.fn() }),
  noopLogger: { info: vi.fn(), warn: vi.fn(), error: vi.fn(), debug: vi.fn() },
}));
vi.mock("../../common/use-app-foreground", () => ({ useAppForeground: () => true }));
vi.mock("sonner", () => ({ toast: { error: vi.fn() } }));
vi.mock("motion/react", () => ({ motion: { div: ({ children, ...props }: React.HTMLAttributes<HTMLDivElement>) => <div {...props}>{children}</div> } }));
vi.mock("@multica/ui/hooks/use-mobile", () => ({ useIsMobile: () => false }));
vi.mock("./use-chat-draft-restore", () => ({ useChatDraftRestore: () => ({ restoreDraftRequest: null, enqueueLocalRestore: vi.fn(), handleRestoreDraftApplied: vi.fn() }) }));
vi.mock("./use-chat-task-actions", () => ({ useChatTaskActions: () => ({ cancelChatTask: vi.fn(), handleEditQueuedTask: vi.fn(), handleRemoveQueuedTask: vi.fn(), handleClearQueuedTasks: vi.fn(), handleSendQueuedTaskNow: vi.fn() }) }));
vi.mock("./use-chat-input-focus", () => ({ useChatInputFocus: () => ({ focusRequest: 0, requestInputFocus: vi.fn() }) }));
vi.mock("./use-chat-context-items", () => ({ useChatContextItems: () => [] }));
vi.mock("./use-chat-resize", () => ({ useChatResize: () => ({ renderWidth: 480, renderHeight: 640, isAtMax: false, boundsReady: true, isDragging: false, toggleExpand: vi.fn(), startDrag: vi.fn() }) }));
vi.mock("./use-visual-viewport-keyboard", () => ({ useVisualViewportKeyboard: () => null }));
vi.mock("./use-chat-project-context-support", () => ({ useChatProjectContextSupport: () => true }));
vi.mock("./use-quick-actions-failure-toast", () => ({ useQuickActionsFailureToast: vi.fn() }));
vi.mock("@multica/core/chat/use-quick-actions-pending-timeout", () => ({ useQuickActionsPendingTimeout: vi.fn() }));
vi.mock("../../i18n", () => ({ useLocale: () => "en", useT: () => ({ t: () => "text" }) }));
vi.mock("./chat-message-list", () => ({ ChatMessageList: () => null, ChatMessageSkeleton: () => null }));
vi.mock("./chat-queue", () => ({ ChatQueue: () => null }));
vi.mock("./chat-empty-state", () => ({ EmptyState: () => null }));
vi.mock("./chat-resize-handles", () => ({ ChatResizeHandles: () => null }));
vi.mock("./offline-banner", () => ({ OfflineBanner: () => null }));
vi.mock("./no-agent-banner", () => ({ NoAgentBanner: () => null }));
vi.mock("./archived-agent-banner", () => ({ ArchivedAgentBanner: () => null }));
vi.mock("./agent-access-revoked-banner", () => ({ AgentAccessRevokedBanner: () => null }));
vi.mock("./runtime-required-banner", () => ({ RuntimeRequiredBanner: () => null }));
vi.mock("./chat-input", () => ({
  ChatInput: ({ onSend }: { onSend: (content: string) => Promise<boolean> }) => (
    <button type="button" onClick={() => void onSend("[@Beta](mention://agent/agent-b) implement the PRD")}>send source</button>
  ),
}));
vi.mock("./agent-handoff-dialog", () => ({
  AgentHandoffDialog: ({ dialog, onDraftChange, onDestinationChange, onSend }: {
    dialog: { draft: string; availableSessions: ChatSession[]; destination: { kind: string; sessionId?: string } };
    onDraftChange: (draft: string) => void;
    onDestinationChange: (destination: { kind: "existing"; sessionId: string }) => void;
    onSend: () => Promise<void>;
  }) => (
    <section aria-label="handoff dialog">
      <textarea aria-label="handoff draft" value={dialog.draft} onChange={(event) => onDraftChange(event.target.value)} />
      {dialog.availableSessions.map((session) => (
        <label key={session.id}>
          <input
            type="radio"
            name="destination"
            checked={dialog.destination.kind === "existing" && dialog.destination.sessionId === session.id}
            onChange={() => onDestinationChange({ kind: "existing", sessionId: session.id })}
          />
          {session.title}
        </label>
      ))}
      <button type="button" onClick={() => void onSend()}>deliver handoff</button>
    </section>
  ),
}));
vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>();
  return {
    ...actual,
    useQuery: (options: { queryKey?: unknown[] }) => {
      const key = options.queryKey ?? [];
      if (key.includes("agents")) return { data: h.agents };
      if (key.includes("members")) return { data: [{ user_id: "user-1", role: "admin" }] };
      if (key.includes("sessions")) return { data: h.sessions, isSuccess: true };
      if (key.includes("projects")) return { data: [], isSuccess: true };
      if (key.includes("pending-tasks")) return { data: h.pendingTasks };
      return { data: null };
    },
    useInfiniteQuery: () => ({ data: { pages: [{ messages: h.messages }] }, isLoading: false, fetchNextPage: vi.fn(), hasNextPage: false, isFetchingNextPage: false }),
    useQueryClient: () => h.queryClient,
  };
});

import { api } from "@multica/core/api";
import { ChatWindow } from "./chat-window";

function agent(id: string, name: string): Agent {
  return { id, name, runtime_id: `runtime-${id}`, runtime_bound: true } as Agent;
}

function session(id: string, agentId: string, title: string): ChatSession {
  return {
    id,
    workspace_id: "ws-1",
    agent_id: agentId,
    creator_id: "user-1",
    title,
    status: "active",
    has_unread: false,
    unread_count: 0,
    last_message: null,
    pinned: false,
    created_at: "2026-10-29T00:00:00Z",
    updated_at: "2026-10-29T00:00:00Z",
  };
}

describe("ChatWindow agent handoffs", () => {
  beforeEach(() => {
    h.store.activeSessionId = "a-chat";
    h.store.selectedAgentId = "agent-a";
    h.agents = [agent("agent-a", "Alpha"), agent("agent-b", "Beta")];
    h.sessions = [
      session("a-chat", "agent-a", "Source"),
      session("b-newest", "agent-b", "Newest B conversation"),
      session("b-named", "agent-b", "Named B conversation"),
    ];
    h.messages = [];
    h.pendingTasks = { tasks: [] };
    h.createSession.mockReset();
    vi.mocked(api.sendChatMessage).mockReset();
    vi.mocked(api.sendChatMessage)
      .mockResolvedValueOnce({ message_id: "source-message", task_id: "source-task", created_at: "2026-10-29T00:00:00Z" })
      .mockResolvedValueOnce({ message_id: "target-message", task_id: "target-task", created_at: "2026-10-29T00:00:01Z" });
  });

  it("opens after A replies and delivers edited text to the explicitly selected existing B session", async () => {
    const view = render(<ChatWindow />);

    fireEvent.click(screen.getByRole("button", { name: "send source" }));
    await waitFor(() => expect(api.sendChatMessage).toHaveBeenCalledWith(
      "a-chat",
      expect.stringContaining("只输出以下字段"),
      undefined,
    ));

    h.messages = [{
      id: "a-reply",
      chat_session_id: "a-chat",
      role: "assistant",
      task_id: "source-task",
      content: "目标：\n交给 Beta 实现。",
      created_at: "2026-10-29T00:00:02Z",
    } as ChatMessage];
    view.rerender(<ChatWindow />);

    await waitFor(() => expect(screen.getByRole("region", { name: "handoff dialog" })).toBeInTheDocument());
    fireEvent.click(screen.getByLabelText("Named B conversation"));
    fireEvent.change(screen.getByLabelText("handoff draft"), { target: { value: "final edited handoff" } });
    fireEvent.click(screen.getByRole("button", { name: "deliver handoff" }));

    await waitFor(() => expect(api.sendChatMessage).toHaveBeenLastCalledWith("b-named", "final edited handoff"));
    expect(api.sendChatMessage).not.toHaveBeenCalledWith("b-newest", "final edited handoff");
    expect(h.createSession).not.toHaveBeenCalled();
  });
});
