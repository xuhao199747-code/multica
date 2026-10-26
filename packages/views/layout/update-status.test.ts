import { createElement } from "react";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, ApiError } from "@multica/core/api";
import type { SelfUpdateJob, SelfUpdateStatus } from "@multica/core/types";
import { SidebarMenu, SidebarProvider } from "@multica/ui/components/ui/sidebar";
import enLayout from "../locales/en/layout.json";
import { classifyUpstreamUpdate, UpdateStatus } from "./update-status";

vi.mock("@multica/core/api", async (importOriginal) => ({
  ...await importOriginal<typeof import("@multica/core/api")>(),
  api: {
    getSelfUpdateStatus: vi.fn(),
    createSelfUpdateJob: vi.fn(),
    getSelfUpdateJob: vi.fn(),
  },
}));

vi.mock("../i18n", () => ({
  useT: () => ({
    t: (selector: (r: typeof enLayout) => string, vars?: Record<string, string>) =>
      selector(enLayout).replace(/\{\{(\w+)\}\}/g, (_, key) => vars?.[key] ?? ""),
  }),
}));

const job: SelfUpdateJob = {
  id: "update-job", phase: "queued", step: "", target_tag: "v0.7.0",
  target_sha: "target-sha", source_sha: "source-sha", repair_count: 0,
  retry_count: 0, created_at: "", updated_at: "", finished_at: null,
};

function status(overrides: Partial<SelfUpdateStatus> = {}): SelfUpdateStatus {
  return {
    capability: "ready", can_update: true,
    latest_release: { tag: "v0.7.0", sha: "target-sha", url: "" },
    deployed_release: "v0.6.0", deployed_custom_sha: "source-sha",
    active_job: null, update_available: true, check_failed: false,
    ...overrides,
  };
}

function renderStatus() {
  return render(createElement(SidebarProvider, null,
    createElement(SidebarMenu, null, createElement(UpdateStatus))));
}

function renderIsolatedStatus() {
  return render(createElement(SidebarProvider, null, createElement(UpdateStatus)));
}

function waitForTimers(callback: (...args: never[]) => unknown, expectedCalls: number) {
  return waitFor(() => {
    expect(callback).toHaveBeenCalledTimes(expectedCalls);
  }, { timeout: 15000, interval: 100 });
}

beforeEach(() => {
  vi.mocked(api.getSelfUpdateStatus).mockResolvedValue(status());
  vi.mocked(api.createSelfUpdateJob).mockResolvedValue(job);
  vi.mocked(api.getSelfUpdateJob).mockResolvedValue(job);
  vi.stubGlobal("fetch", vi.fn());
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.resetAllMocks();
  vi.unstubAllGlobals();
});

describe("UpdateStatus", () => {
  it("requires confirmation before creating an update and disables actions while creating", async () => {
    let resolveJob!: (value: SelfUpdateJob) => void;
    vi.mocked(api.createSelfUpdateJob).mockReturnValue(new Promise((resolve) => { resolveJob = resolve; }));
    renderStatus();
    fireEvent.click(await screen.findByRole("button", { name: "Update to v0.7.0" }));
    expect(screen.getByRole("alertdialog")).toHaveTextContent(/database backup/i);
    expect(api.createSelfUpdateJob).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Start update" }));
    expect(api.createSelfUpdateJob).toHaveBeenCalledWith("v0.7.0", expect.stringMatching(/^self-update-/));
    expect(screen.getByRole("button", { name: "Start update" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Start update" })).toHaveAttribute("aria-busy", "true");
    expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
    await act(async () => { resolveJob(job); });
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    expect(screen.getByText("Update queued")).toBeInTheDocument();
  });

  it.each([
    ["worker_offline", "Update worker offline"],
    ["needs_setup", "Update needs setup"],
  ] as const)("shows %s without allowing updates", async (capability, label) => {
    vi.mocked(api.getSelfUpdateStatus).mockResolvedValue(status({ capability, can_update: false }));
    renderStatus();
    fireEvent.click(await screen.findByText(label));
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    expect(api.createSelfUpdateJob).not.toHaveBeenCalled();
  });

  it("shows release-check failure without offering an update", async () => {
    vi.mocked(api.getSelfUpdateStatus).mockResolvedValue(status({ check_failed: true }));
    renderStatus();
    fireEvent.click(await screen.findByText("Update check failed"));
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    expect(fetch).not.toHaveBeenCalled();
  });

  it("respects server permissions even when a newer release is available", async () => {
    vi.mocked(api.getSelfUpdateStatus).mockResolvedValue(status({ can_update: false }));
    renderStatus();
    fireEvent.click(await screen.findByText("Update to v0.7.0"));
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  });

  it("uses GitHub comparison only for an explicit ApiError 404", async () => {
    vi.mocked(api.getSelfUpdateStatus).mockRejectedValue(new ApiError("Not found", 404, "Not Found"));
    vi.mocked(fetch)
      .mockResolvedValueOnce(new Response(JSON.stringify({ sha: "new-upstream" })))
      .mockResolvedValueOnce(new Response(JSON.stringify({ sha: "custom", parents: [{ sha: "old-upstream" }] })));
    renderStatus();
    expect(await screen.findByText("Official update available")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "View official updates" })).toHaveAttribute("href", "https://github.com/multica-ai/multica/commits/main");
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it.each([
    new ApiError("Forbidden", 403, "Forbidden"),
    new ApiError("Unavailable", 503, "Unavailable"),
    Object.assign(new Error("Not found"), { status: 404 }),
  ])("does not hide API errors behind GitHub checks (%s)", async (error) => {
    vi.mocked(api.getSelfUpdateStatus).mockRejectedValue(error);
    renderStatus();
    expect(await screen.findByText("Update check failed")).toBeInTheDocument();
    expect(fetch).not.toHaveBeenCalled();
  });

  it("shows malformed status responses as unknown", async () => {
    vi.mocked(api.getSelfUpdateStatus).mockResolvedValue(null);
    renderStatus();
    expect(await screen.findByText("Update check failed")).toBeInTheDocument();
    expect(fetch).not.toHaveBeenCalled();
  });

  it("keeps the dialog open on malformed creation and reuses its idempotency key on retry", async () => {
    vi.mocked(api.createSelfUpdateJob).mockResolvedValueOnce(null).mockResolvedValueOnce(job);
    renderStatus();
    fireEvent.click(await screen.findByRole("button", { name: "Update to v0.7.0" }));
    fireEvent.click(screen.getByRole("button", { name: "Start update" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Could not start update. Try again.");
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Start update" }));
    await waitFor(() => expect(api.createSelfUpdateJob).toHaveBeenCalledTimes(2));
    expect(vi.mocked(api.createSelfUpdateJob).mock.calls[0]).toEqual(vi.mocked(api.createSelfUpdateJob).mock.calls[1]);
  });

  it.each(["succeeded", "failed", "needs_decision"])("polls a deploying job every five seconds, refreshes status and stops at %s", async (phase) => {
    vi.mocked(api.getSelfUpdateStatus).mockResolvedValueOnce(status({ active_job: { ...job, phase: "deploying" } })).mockResolvedValue(status({ update_available: false }));
    vi.mocked(api.getSelfUpdateJob).mockResolvedValue({ ...job, phase });
    renderIsolatedStatus();
    expect(await screen.findByText("Deploying update")).toBeInTheDocument();
    await waitForTimers(api.getSelfUpdateJob, 1);
    expect(api.getSelfUpdateJob).toHaveBeenCalledWith(job.id);
    expect(api.getSelfUpdateStatus).toHaveBeenCalledTimes(2);
  }, 10000);

  it("retains and retries a running job when the service temporarily restarts", async () => {
    vi.mocked(api.getSelfUpdateStatus).mockResolvedValue(status({ active_job: { ...job, phase: "deploying" } }));
    vi.mocked(api.getSelfUpdateJob).mockRejectedValueOnce(new Error("Restarting")).mockResolvedValue({ ...job, phase: "verifying" });
    renderIsolatedStatus();
    await screen.findByText("Deploying update");
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 50)); });
    await waitForTimers(api.getSelfUpdateJob, 1);
    expect(screen.getByRole("status")).toHaveTextContent("Update check failed");
    await waitForTimers(api.getSelfUpdateJob, 2);
    expect(screen.getByText("Verifying update")).toBeInTheDocument();
  }, 15000);
});

describe("classifyUpstreamUpdate", () => {
  it("marks the installation current when the custom branch is based on the latest upstream commit", () => {
    expect(
      classifyUpstreamUpdate("upstream-sha", { sha: "custom-sha", parents: [{ sha: "upstream-sha" }] }),
    ).toBe("current");
  });

  it("marks an update available when upstream has advanced beyond the custom branch base", () => {
    expect(
      classifyUpstreamUpdate("new-upstream-sha", { sha: "custom-sha", parents: [{ sha: "old-upstream-sha" }] }),
    ).toBe("update-available");
  });

  it("keeps the state unknown when GitHub does not provide a base commit", () => {
    expect(classifyUpstreamUpdate("upstream-sha", { sha: "custom-sha", parents: [] })).toBe("unknown");
  });
});
