"use client";

import { describe, it, expect, vi, beforeEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, act, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { useCommentComposerStore } from "@multica/core/issues/stores";

const apiListAgents = vi.hoisted(() => vi.fn());
const apiListMembers = vi.hoisted(() => vi.fn());
const currentWorkspace = vi.hoisted(() => ({
  value: { id: "ws-1", slug: "acme", name: "Acme" },
}));

vi.mock("@multica/core/api", () => ({
  api: {
    listAgents: apiListAgents,
    listMembers: apiListMembers,
  },
}));

vi.mock("@multica/core/auth", async () => {
  const actual = await vi.importActual<typeof import("@multica/core/auth")>(
    "@multica/core/auth",
  );
  return {
    ...actual,
    useAuthStore: (selector: (s: { user: { id: string } | null }) => unknown) =>
      selector({ user: { id: "user-1" } }),
  };
});

vi.mock("@multica/core/paths", async () => {
  const actual = await vi.importActual<typeof import("@multica/core/paths")>(
    "@multica/core/paths",
  );
  return {
    ...actual,
    useCurrentWorkspace: () => currentWorkspace.value,
  };
});

import { useSendAsAgent } from "./use-send-as-agent";

const mika = {
  id: "agent-mika",
  system_key: "mika",
  name: "Mika",
  archived_at: null,
  runtime_id: "rt-1",
  permission_mode: "public_to",
  invocation_targets: [{ target_type: "workspace", target_id: null }],
  owner_id: null,
};

const builder = {
  id: "agent-builder",
  system_key: undefined,
  name: "Builder",
  archived_at: null,
  runtime_id: "rt-2",
  permission_mode: "public_to",
  invocation_targets: [{ target_type: "workspace", target_id: null }],
  owner_id: null,
};

function wrapper({ children }: { children: ReactNode }) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  );
}

describe("useSendAsAgent", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    currentWorkspace.value = { id: "ws-1", slug: "acme", name: "Acme" };
    useCommentComposerStore.setState({ sendAsByWorkspace: {} });
    apiListAgents.mockResolvedValue([mika, builder]);
    apiListMembers.mockResolvedValue([{ user_id: "user-1", role: "owner" }]);
  });

  it("explicitly mentions the selected default agent when a choice is available", async () => {
    const { result } = renderHook(() => useSendAsAgent(), { wrapper });
    await waitFor(() => expect(result.current.defaultAgent?.id).toBe("agent-mika"));
    expect(result.current.selectedId).toBe("agent-mika");
    expect(result.current.selectedMention).toBe(
      "[@Mika](mention://agent/agent-mika)",
    );
    expect(result.current.hasChoice).toBe(true);
  });

  it("emits a mention link when a non-default agent is selected", async () => {
    const { result } = renderHook(() => useSendAsAgent(), { wrapper });
    await waitFor(() => expect(result.current.choices).toHaveLength(2));
    act(() => result.current.setSelectedId("agent-builder"));
    expect(result.current.selectedId).toBe("agent-builder");
    expect(result.current.selectedMention).toBe(
      "[@Builder](mention://agent/agent-builder)",
    );
  });

  it("persists the non-default pick per workspace and clears it on default", async () => {
    const { result } = renderHook(() => useSendAsAgent(), { wrapper });
    await waitFor(() => expect(result.current.choices).toHaveLength(2));
    act(() => result.current.setSelectedId("agent-builder"));
    expect(useCommentComposerStore.getState().sendAsByWorkspace["ws-1"]).toBe(
      "agent-builder",
    );
    act(() => result.current.setSelectedId("agent-mika"));
    expect(useCommentComposerStore.getState().sendAsByWorkspace["ws-1"]).toBeNull();
  });

  it("resets to the new workspace's choice instead of carrying an old target across", async () => {
    const { result, rerender } = renderHook(() => useSendAsAgent(), { wrapper });
    await waitFor(() => expect(result.current.choices).toHaveLength(2));
    act(() => result.current.setSelectedId("agent-builder"));
    expect(result.current.selectedId).toBe("agent-builder");

    currentWorkspace.value = { id: "ws-2", slug: "beta", name: "Beta" };
    rerender();

    await waitFor(() => expect(result.current.selectedId).toBe("agent-mika"));
  });

  it("hides agents the member cannot invoke", async () => {
    apiListAgents.mockResolvedValue([
      mika,
      { ...builder, id: "agent-secret", name: "Secret", permission_mode: "private", owner_id: "someone-else" },
    ]);
    const { result } = renderHook(() => useSendAsAgent(), { wrapper });
    await waitFor(() => expect(result.current.choices.length).toBeGreaterThan(0));
    expect(result.current.choices.map((c) => c.id)).toEqual(["agent-mika"]);
    expect(result.current.hasChoice).toBe(false);
  });

  it("hides archived and runtime-unbound agents", async () => {
    apiListAgents.mockResolvedValue([
      mika,
      { ...builder, archived_at: new Date().toISOString() },
      { ...builder, id: "agent-unbound", name: "Unbound", runtime_id: "" },
    ]);
    const { result } = renderHook(() => useSendAsAgent(), { wrapper });
    await waitFor(() => expect(result.current.choices.length).toBeGreaterThan(0));
    expect(result.current.choices.map((c) => c.id)).toEqual(["agent-mika"]);
  });

  it("offers no choice and no selector when Mika is the only visible agent", async () => {
    apiListAgents.mockResolvedValue([mika]);
    const { result } = renderHook(() => useSendAsAgent(), { wrapper });
    await waitFor(() => expect(result.current.defaultAgent?.id).toBe("agent-mika"));
    expect(result.current.hasChoice).toBe(false);
  });
});
