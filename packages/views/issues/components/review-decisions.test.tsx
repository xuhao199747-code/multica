// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { GitHubPullRequest } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enIssues from "../../locales/en/issues.json";

const TEST_RESOURCES = { en: { common: enCommon, issues: enIssues } };

const mocks = vi.hoisted(() => ({
  listIssueReviewDecisions: vi.fn(),
  createIssueReviewDecision: vi.fn(),
  resolveIssueReviewDecision: vi.fn(),
}));

vi.mock("@multica/core/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/api")>()),
  api: mocks,
}));

import { ReviewDecisions } from "./review-decisions";

const pr: GitHubPullRequest = {
  id: "pr-1",
  workspace_id: "ws-1",
  repo_owner: "multica-ai",
  repo_name: "multica",
  number: 20,
  title: "Add review flow",
  state: "open",
  html_url: "https://github.com/multica-ai/multica/pull/20",
  branch: "feature/review",
  author_login: "octocat",
  author_avatar_url: null,
  merged_at: null,
  closed_at: null,
  pr_created_at: "2026-10-01T00:00:00Z",
  pr_updated_at: "2026-10-18T00:00:00Z",
  head_sha: "abc1234567890",
  link_source: "manual",
};

function renderPanel({ decisions }: { decisions: unknown[] }) {
  mocks.listIssueReviewDecisions.mockResolvedValue({ decisions });
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <ReviewDecisions issueId="issue-1" prs={[pr]} />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

describe("ReviewDecisions", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("shows no-decision state with a record action", async () => {
    renderPanel({ decisions: [] });

    expect(await screen.findByText("No decision")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Record decision" })).toBeInTheDocument();
    expect(screen.getByText("#20")).toBeInTheDocument();
    expect(screen.getByText("abc1234")).toBeInTheDocument();
  });

  it("records a decision against the PR's exact current head", async () => {
    renderPanel({ decisions: [] });
    await screen.findByText("No decision");

    fireEvent.click(screen.getByRole("button", { name: "Record decision" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Request changes" }));

    await waitFor(() => expect(mocks.createIssueReviewDecision).toHaveBeenCalledTimes(1));
    expect(mocks.createIssueReviewDecision).toHaveBeenCalledWith("issue-1", {
      pull_request_id: "pr-1",
      head_sha: "abc1234567890",
      decision: "changes_requested",
    });
  });

  it("shows the current approved call and the deploy hint", async () => {
    renderPanel({
      decisions: [
        {
          id: "d1",
          issue_id: "issue-1",
          pull_request_id: "pr-1",
          head_sha: "abc1234567890",
          decision: "approved",
          note: null,
          decided_by: "user-1",
          created_at: "2026-10-19T00:00:00Z",
          superseded_at: null,
          resolved_at: null,
          stale: false,
        },
      ],
    });

    expect(await screen.findByText("Approved")).toBeInTheDocument();
    expect(
      screen.getByText("Approved — you decide when to deploy."),
    ).toBeInTheDocument();
  });

  it("marks a decision made against an old head as stale", async () => {
    renderPanel({
      decisions: [
        {
          id: "d1",
          issue_id: "issue-1",
          pull_request_id: "pr-1",
          head_sha: "deadbeef",
          decision: "approved",
          note: null,
          decided_by: "user-1",
          created_at: "2026-10-19T00:00:00Z",
          superseded_at: null,
          resolved_at: null,
          stale: true,
        },
      ],
    });

    // The stale call does not count as the current decision.
    expect(await screen.findByText("No decision")).toBeInTheDocument();
    expect(screen.queryByText("Approved — you decide when to deploy.")).not.toBeInTheDocument();
  });

  it("resolves the current decision from its menu", async () => {
    renderPanel({
      decisions: [
        {
          id: "d1",
          issue_id: "issue-1",
          pull_request_id: "pr-1",
          head_sha: "abc1234567890",
          decision: "deferred",
          note: null,
          decided_by: "user-1",
          created_at: "2026-10-19T00:00:00Z",
          superseded_at: null,
          resolved_at: null,
          stale: false,
        },
      ],
    });
    await screen.findByText("Deferred");

    fireEvent.click(screen.getByRole("button", { name: "Record decision" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Mark resolved" }));

    await waitFor(() => expect(mocks.resolveIssueReviewDecision).toHaveBeenCalledWith("issue-1", "d1"));
  });
});
