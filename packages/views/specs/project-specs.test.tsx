import React from "react";
import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithI18n } from "../test/i18n";
import { ProjectSpecs } from "./project-specs";

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws1" }));
vi.mock("@multica/core/paths", () => ({ useWorkspacePaths: () => ({ specDetail: (id: string) => `/acme/specs/${id}` }) }));
vi.mock("@multica/core/specs", () => ({
  specListOptions: () => ({ queryKey: ["specs"] }),
  useCreateSpec: () => ({ mutateAsync: vi.fn(), isPending: false }),
}));
vi.mock("@tanstack/react-query", () => ({
  useQuery: () => ({ data: { specs: [{ id: "s1", identifier: "AC-S-1", title: "Requirements", status: "draft", updated_at: "2026-01-01T00:00:00Z" }] }, isLoading: false, isError: false }),
}));
vi.mock("../navigation", () => ({ AppLink: ({ href, children }: { href: string; children: React.ReactNode }) => <a href={href}>{children}</a>, useNavigation: () => ({ push: vi.fn() }) }));

describe("project specs", () => {
  it("links project specs to their detail pages", () => {
    renderWithI18n(<ProjectSpecs projectId="p1" />);
    expect(screen.getByRole("link", { name: /AC-S-1.*Requirements/ })).toHaveAttribute("href", "/acme/specs/s1");
  });
});
