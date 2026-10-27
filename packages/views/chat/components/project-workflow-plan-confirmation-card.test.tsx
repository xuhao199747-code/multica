// @vitest-environment jsdom

import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import type { ComponentProps } from "react";
import { api } from "@multica/core/api";
import type { ProjectExecution } from "@multica/core/types";
import {
  projectAgentBindingsOptions,
  projectExecutionsOptions,
  projectWorkflowSettingsOptions,
} from "@multica/core/projects/workflow-queries";
import enProjects from "../../locales/en/projects.json";
import { ProjectWorkflowPlanConfirmationCard } from "./project-workflow-plan-confirmation-card";

vi.mock("@multica/core/api", () => ({
  api: {
    startProjectWorkflowExecution: vi.fn(),
  },
}));

vi.mock("../../navigation", () => ({
  AppLink: ({ href, children, ...props }: ComponentProps<"a">) => (
    <a href={href} {...props}>{children}</a>
  ),
}));

const workspaceId = "workspace-1";
const projectId = "project-1";
const sessionId = "session-1";
const agentId = "prd-1";
const messageId = "assistant-plan-message";
const snapshot = {
  goal: "Bind workflow start to a plan message",
  scope: ["chat renderer"],
  acceptance_criteria: ["ordinary replies cannot start work"],
  risks: ["stale browser state"],
};
const content = [
  "## Plan",
  "Review the proposed execution.",
  "<!-- multica-project-workflow-plan-confirmation",
  JSON.stringify({ version: 1, title: "Message-bound start", snapshot }),
  "-->",
].join("\n");

function response() {
  return {
    execution: {
      id: "execution-1",
      project_id: projectId,
      workspace_id: workspaceId,
      source_chat_session_id: sessionId,
      source_agent_id: agentId,
      snapshot,
      delivery_result: {},
      state: "development",
      review_round: 0,
      created_at: "2026-10-26T00:00:00Z",
      main_issue: { id: "issue-1", title: "Message-bound start", status: "in_progress", role: "main" },
      historical_issues: [],
    },
    issue: null,
    execution_issue: null,
    task_id: "task-1",
  };
}

function renderCard({
  messageContent = content,
  executions = [],
}: {
  messageContent?: string;
  executions?: ProjectExecution[];
} = {}) {
  const client = new QueryClient();
  client.setQueryData(projectWorkflowSettingsOptions(workspaceId, projectId).queryKey, {
    project_id: projectId,
    workspace_id: workspaceId,
    enabled: true,
    project_context: "",
    delivery_mode: "notify_only",
    delivery_config: {},
  });
  client.setQueryData(projectAgentBindingsOptions(workspaceId, projectId).queryKey, {
    bindings: [{ id: "binding-1", project_id: projectId, workspace_id: workspaceId, agent_id: agentId, role: "prd", source: "manual", created_at: "" }],
    total: 1,
  });
  client.setQueryData(projectExecutionsOptions(workspaceId, projectId).queryKey, {
    executions,
    total: executions.length,
  });

  return render(
    <I18nProvider locale="en" resources={{ en: { projects: enProjects } }}>
      <QueryClientProvider client={client}>
        <ProjectWorkflowPlanConfirmationCard
          workspaceId={workspaceId}
          projectId={projectId}
          sessionId={sessionId}
          agentId={agentId}
          messageId={messageId}
          content={messageContent}
          issueHref={(issueId) => `/${workspaceId}/issues/${issueId}`}
        />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("ProjectWorkflowPlanConfirmationCard", () => {
  afterEach(cleanup);

  beforeEach(() => {
    vi.mocked(api.startProjectWorkflowExecution).mockReset();
  });

  it("starts only the displayed plan snapshot and source message", async () => {
    vi.mocked(api.startProjectWorkflowExecution).mockResolvedValue(response());
    renderCard();

    fireEvent.click(screen.getByRole("button", { name: "Confirm and start" }));

    await waitFor(() => {
      expect(api.startProjectWorkflowExecution).toHaveBeenCalledWith(projectId, {
        source_chat_session_id: sessionId,
        source_agent_id: agentId,
        source_message_id: messageId,
        snapshot,
      });
    });
    expect(await screen.findByText("Execution started")).not.toBeNull();
    expect(screen.getByRole("link", { name: "Message-bound start" }).getAttribute("href")).toBe(
      "/workspace-1/issues/issue-1",
    );
  });

  it("does not render for a message without valid plan confirmation data", () => {
    const { container } = renderCard({ messageContent: "continue" });

    expect(container.innerHTML).toBe("");
  });

  it("shows a retryable inline error when starting fails", async () => {
    vi.mocked(api.startProjectWorkflowExecution).mockRejectedValue(new Error("Network unavailable"));
    renderCard();

    fireEvent.click(screen.getByRole("button", { name: "Confirm and start" }));

    expect(await screen.findByText("Network unavailable")).not.toBeNull();
    expect(screen.getByRole("button", { name: "Try again" }).hasAttribute("disabled")).toBe(false);
  });

  it("shows an active execution in place without another start button", () => {
    const view = renderCard({ executions: [response().execution] });

    expect(view.getByText("Development in progress")).not.toBeNull();
    expect(view.getByRole("link", { name: "Message-bound start" })).not.toBeNull();
    expect(view.queryByRole("button", { name: "Confirm and start" })).toBeNull();
  });
});
