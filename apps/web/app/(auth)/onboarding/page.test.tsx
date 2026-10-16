import { render, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({
  user: { id: "user-1" } as { id: string } | null,
  isLoading: false,
  hasOnboarded: false,
  workspaces: [{ id: "ws-acme", slug: "acme" }],
  ready: true,
  replace: vi.fn(),
}));

vi.mock("next/navigation", () => ({
  useRouter: () => ({ replace: state.replace, push: vi.fn() }),
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (auth: { user: typeof state.user; isLoading: boolean }) => unknown) =>
    selector({ user: state.user, isLoading: state.isLoading }),
}));

vi.mock("@multica/core/paths", () => ({
  paths: { login: () => "/login" },
  resolvePostAuthDestination: (workspaces: typeof state.workspaces) =>
    workspaces[0] ? `/${workspaces[0].slug}/issues` : "/workspaces/new",
  useHasOnboarded: () => state.hasOnboarded,
}));

vi.mock("@multica/core/workspace", () => ({
  useWorkspaceList: () => ({ workspaces: state.workspaces, ready: state.ready }),
}));

vi.mock("@multica/views/onboarding", () => ({
  CliInstallInstructions: () => null,
  OnboardingFlow: () => <div data-testid="onboarding-flow" />,
}));

import OnboardingPage from "./page";

describe("OnboardingPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    state.user = { id: "user-1" };
    state.isLoading = false;
    state.hasOnboarded = false;
    state.workspaces = [{ id: "ws-acme", slug: "acme" }];
    state.ready = true;
  });

  it("returns a user with an existing workspace to its workbench even when onboarding is incomplete", async () => {
    render(<OnboardingPage />);

    await waitFor(() => {
      expect(state.replace).toHaveBeenCalledWith("/acme/issues");
    });
  });
});
