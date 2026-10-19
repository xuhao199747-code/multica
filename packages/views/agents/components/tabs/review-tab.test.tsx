// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { Agent, MemberWithUser } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../../locales/en/common.json";
import enAgents from "../../../locales/en/agents.json";

const TEST_RESOURCES = { en: { common: enCommon, agents: enAgents } };

const mockProjects = vi.hoisted(() => [
  { id: "proj-1", title: "Alpha" },
  { id: "proj-2", title: "Beta" },
]);

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/projects", () => ({
  projectListOptions: (wsId: string) => ({
    queryKey: ["projects", wsId, "list"],
    queryFn: async () => ({ projects: mockProjects }),
    select: (data: { projects: unknown[] }) => data.projects,
  }),
}));

import { ReviewTab } from "./review-tab";

const baseAgent: Agent = {
  id: "agent-1",
  workspace_id: "ws-1",
  runtime_id: "runtime-1",
  name: "Reviewer",
  description: "",
  instructions: "",
  avatar_url: null,
  runtime_mode: "local",
  runtime_config: {},
  custom_args: [],
  visibility: "workspace",
  permission_mode: "public_to",
  invocation_targets: [{ target_type: "workspace", target_id: null }],
  status: "idle",
  max_concurrent_tasks: 1,
  model: "",
  owner_id: "user-1",
  skills: [],
  archived_at: null,
  archived_by: null,
  created_at: "2026-05-28T00:00:00Z",
  updated_at: "2026-05-28T00:00:00Z",
};

const members: MemberWithUser[] = [
  {
    id: "member-1",
    workspace_id: "ws-1",
    user_id: "user-1",
    role: "owner",
    created_at: "2026-05-28T00:00:00Z",
    name: "Ada",
    email: "ada@example.test",
    avatar_url: null,
  },
  {
    id: "member-2",
    workspace_id: "ws-1",
    user_id: "user-2",
    role: "member",
    created_at: "2026-05-28T00:00:00Z",
    name: "Ben",
    email: "ben@example.test",
    avatar_url: null,
  },
];

function renderTab({
  agent = baseAgent,
}: {
  agent?: Agent;
} = {}) {
  const onSave = vi.fn<(updates: Record<string, unknown>) => Promise<void>>()
    .mockResolvedValue(undefined);
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const result = render(
    <QueryClientProvider client={queryClient}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <ReviewTab agent={agent} members={members} onSave={onSave} />
      </I18nProvider>
    </QueryClientProvider>,
  );
  return { ...result, onSave };
}

describe("ReviewTab", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders disabled by default and saves nothing until enabled", () => {
    const { onSave } = renderTab();
    expect(screen.queryByRole("switch", { name: "Enable review role" })).toBeInTheDocument();
    expect(screen.queryByText("Recipient")).not.toBeInTheDocument();
    expect(onSave).not.toHaveBeenCalled();
  });

  it("enabling saves a config with the first member as recipient", async () => {
    const user = userEvent.setup();
    const { onSave } = renderTab();

    await user.click(screen.getByRole("switch", { name: "Enable review role" }));
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));
    const updates = onSave.mock.calls[0]![0] as { review_config: { recipient_id: string } };
    expect(updates.review_config.recipient_id).toBe("user-1");
  });

  it("clears the review role when disabled", async () => {
    const user = userEvent.setup();
    const agent: Agent = {
      ...baseAgent,
      review_config: {
        recipient_id: "user-2",
        scope: "workspace",
        instant_enabled: true,
        summary_enabled: false,
        summary_time: "09:30",
        timezone: "UTC",
      },
    };
    const { onSave } = renderTab({ agent });

    await user.click(screen.getByRole("switch", { name: "Enable review role" }));
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));
    expect(onSave.mock.calls[0]![0]).toEqual({ review_config: null });
  });

  it("blocks save with an error when project scope has no project", async () => {
    const user = userEvent.setup();
    const { onSave } = renderTab();

    await user.click(screen.getByRole("switch", { name: "Enable review role" }));
    // Switch scope to "Selected projects".
    await user.click(screen.getByRole("combobox", { name: "Scope" }));
    await user.click(screen.getByRole("option", { name: "Selected projects" }));

    expect(screen.getByText("Select at least one project.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    expect(onSave).not.toHaveBeenCalled();
  });

  it("saves the daily summary schedule when enabled", async () => {
    const user = userEvent.setup();
    const { onSave } = renderTab();

    await user.click(screen.getByRole("switch", { name: "Enable review role" }));
    await user.click(screen.getByRole("switch", { name: "Daily summary" }));
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));
    const updates = onSave.mock.calls[0]![0] as {
      review_config: {
        summary_enabled: boolean;
        summary_time: string;
        timezone: string;
      };
    };
    expect(updates.review_config.summary_enabled).toBe(true);
    expect(updates.review_config.summary_time).toMatch(/^\d{2}:\d{2}$/);
    expect(updates.review_config.timezone).not.toBe("");
  });

  it("keeps an existing config unchanged until edits happen", () => {
    const agent: Agent = {
      ...baseAgent,
      review_config: {
        recipient_id: "user-2",
        scope: "workspace",
        instant_enabled: true,
        summary_enabled: false,
        summary_time: "09:30",
        timezone: "UTC",
      },
    };
    renderTab({ agent });
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });
});
