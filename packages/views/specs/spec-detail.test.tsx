import React from "react";
import { describe, expect, it, vi } from "vitest";
import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithI18n } from "../test/i18n";
import { SpecDetail } from "./spec-detail";

const mocks = vi.hoisted(() => ({ publish: vi.fn(), edit: vi.fn(), resolve: vi.fn(), remove: vi.fn(), comments: [] as unknown[] }));

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws1" }));
vi.mock("@multica/core/auth", () => ({ useAuthStore: (selector: (state: { user: { id: string } }) => unknown) => selector({ user: { id: "u1" } }) }));
vi.mock("@multica/core/config", () => ({ useConfigStore: (selector: (state: { commentDeleteKeepRepliesSupported: boolean }) => unknown) => selector({ commentDeleteKeepRepliesSupported: true }) }));
vi.mock("@multica/core/paths", () => ({ useWorkspacePaths: () => ({ issueDetail: (id: string) => `/acme/issues/${id}` }) }));
vi.mock("@multica/core/workspace/hooks", () => ({ useActorName: () => ({ getActorName: () => "Alice" }) }));
vi.mock("@multica/core/specs", () => ({
  specDetailOptions: () => ({ queryKey: ["detail"] }), specVersionsOptions: () => ({ queryKey: ["versions"] }),
  specIssuesOptions: () => ({ queryKey: ["issues"] }), specCommentsOptions: () => ({ queryKey: ["comments"] }),
  useSpecActions: () => ({ publish: { mutateAsync: mocks.publish }, transition: { mutateAsync: vi.fn() }, comment: { mutateAsync: vi.fn() }, editComment: { mutateAsync: mocks.edit }, resolveComment: { mutateAsync: mocks.resolve }, deleteComment: { mutateAsync: mocks.remove } }),
}));
vi.mock("@tanstack/react-query", () => ({ useQuery: ({ queryKey }: { queryKey: string[] }) => {
  switch (queryKey[0]) {
    case "detail": return { data: { id: "s1", identifier: "AC-S-1", title: "Requirements", status: "draft", current_version_id: "v1", current_version: { id: "v1", number: 1, content: "Body", changelog: "Initial", created_by: "u1", created_at: "2026-01-01T00:00:00Z" } }, isLoading: false };
    case "versions": return { data: { versions: [{ id: "v1", number: 1, content: "Body", changelog: "Initial", created_by: "u1", created_at: "2026-01-01T00:00:00Z" }] } };
    case "issues": return { data: { issues: [{ id: "i1", identifier: "AC-1", title: "Task", status: "open" }] } };
    default: return { data: mocks.comments };
  }
} }));
vi.mock("../navigation", () => ({ AppLink: ({ href, children }: { href: string; children: React.ReactNode }) => <a href={href}>{children}</a> }));
vi.mock("../editor", async () => {
  const React = await import("react");
  return {
    ContentEditor: React.forwardRef(function MockEditor({ defaultValue, onUpdate }: { defaultValue: string; onUpdate: (value: string) => void }, ref) {
      const [value, setValue] = React.useState(defaultValue);
      React.useImperativeHandle(ref, () => ({ getMarkdown: () => value }));
      return <textarea aria-label="Editor" value={value} onChange={event => { setValue(event.target.value); onUpdate(event.target.value); }} />;
    }),
    ReadonlyContent: ({ content }: { content: string }) => <div>{content}</div>,
  };
});

describe("spec detail", () => {
  it("asks before deleting a comment and retains its replies when supported", async () => {
    mocks.comments = [{ id: "c1", author_type: "member", author_id: "u1", content: "Review", parent_id: null, attachments: [], reactions: [], resolved_at: null }];
    mocks.remove.mockResolvedValueOnce(undefined);
    const user = userEvent.setup();
    renderWithI18n(<SpecDetail id="s1" />);
    await user.click(screen.getByRole("button", { name: "Delete comment" }));
    expect(mocks.remove).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Delete" }));
    expect(mocks.remove).toHaveBeenCalledWith({ id: "c1", keepReplies: true });
    mocks.comments = [];
  });

  it("resolves an open comment", async () => {
    mocks.comments = [{ id: "c1", author_type: "member", author_id: "u1", content: "Review", parent_id: null, attachments: [], reactions: [], resolved_at: null }];
    mocks.resolve.mockResolvedValueOnce(undefined);
    const user = userEvent.setup();
    renderWithI18n(<SpecDetail id="s1" />);
    await user.click(screen.getByRole("button", { name: "Resolve" }));
    expect(mocks.resolve).toHaveBeenCalledWith({ id: "c1", resolved: false });
    mocks.comments = [];
  });

  it("allows authors to edit their comment while preserving attachments", async () => {
    mocks.comments = [{ id: "c1", author_type: "member", author_id: "u1", content: "Original", parent_id: null, attachments: [{ id: "a1" }], reactions: [], resolved_at: null, revision: 2 }];
    mocks.edit.mockResolvedValueOnce(undefined);
    const user = userEvent.setup();
    renderWithI18n(<SpecDetail id="s1" />);
    await user.click(screen.getByRole("button", { name: "Edit comment" }));
    const input = screen.getByLabelText("Edit comment");
    await user.clear(input);
    await user.type(input, "Updated");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(mocks.edit).toHaveBeenCalledWith({ id: "c1", content: "Updated", attachmentIds: ["a1"], revision: 2 });
    mocks.comments = [];
  });

  it("keeps changes made while a version is publishing", async () => {
    let finish!: () => void;
    mocks.publish.mockImplementationOnce(() => new Promise<void>(resolve => { finish = resolve; }));
    const user = userEvent.setup();
    renderWithI18n(<SpecDetail id="s1" />);
    await user.click(screen.getByRole("button", { name: "Publish version" }));
    await user.type(screen.getByLabelText("Change summary"), "First");
    await user.click(screen.getAllByRole("button", { name: "Publish version" })[1]!);
    await user.type(screen.getByLabelText("Editor"), " plus more");
    await act(async () => finish());
    await waitFor(() => expect(screen.getByLabelText("Editor")).toHaveValue("Body plus more"));
  });

  it("shows the version and links related issues", () => {
    renderWithI18n(<SpecDetail id="s1" />);
    expect(screen.getByText("Requirements")).toBeInTheDocument();
    expect(screen.getByText("Initial")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /AC-1.*Task/ })).toHaveAttribute("href", "/acme/issues/i1");
  });
});
