import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithI18n } from "../test/i18n";
import { SpecCommentComposer } from "./spec-comment-composer";

const mocks = vi.hoisted(() => ({ blocked: false, send: vi.fn() }));
vi.mock("../issues/components/use-comment-uploads", () => ({
  useCommentUploads: () => ({ attachments: [{ id: "a1", url: "/api/attachments/a1/download" }], handleUpload: vi.fn(), gate: { uploading: mocks.blocked, isBlocked: () => mocks.blocked } }),
}));
vi.mock("../editor", async () => {
  const React = await import("react");
  return {
    useUploadGate: () => ({ onUploadingChange: vi.fn(), uploading: mocks.blocked, isBlocked: () => mocks.blocked }),
    ContentEditor: React.forwardRef(function Editor({ defaultValue, onUpdate }: { defaultValue: string; onUpdate: (value: string) => void }, ref) {
      const [value, setValue] = React.useState(defaultValue);
      React.useImperativeHandle(ref, () => ({ getMarkdown: () => value, flushPendingUpdate: () => value, clearContent: () => setValue(""), uploadFile: vi.fn() }));
      return <textarea aria-label="Comment" value={value} onChange={event => { setValue(event.target.value); onUpdate(event.target.value); }} />;
    }),
  };
});

describe("spec comment composer", () => {
  beforeEach(() => { mocks.blocked = false; mocks.send.mockReset(); });
  it("does not send while an attachment is uploading", async () => {
    mocks.blocked = true;
    const user = userEvent.setup();
    renderWithI18n(<SpecCommentComposer specId="s1" versionId="v1" onSend={mocks.send} />);
    await user.type(screen.getByRole("textbox", { name: "Comment" }), "Draft");
    expect(screen.getByRole("button", { name: "Send" })).toBeDisabled();
    expect(mocks.send).not.toHaveBeenCalled();
  });
  it("sends referenced attachment IDs with the comment", async () => {
    mocks.send.mockResolvedValue(undefined);
    const user = userEvent.setup();
    renderWithI18n(<SpecCommentComposer specId="s2" versionId="v2" onSend={mocks.send} />);
    await user.type(screen.getByRole("textbox", { name: "Comment" }), "See /api/attachments/a1/download");
    await user.click(screen.getByRole("button", { name: "Send" }));
    expect(mocks.send).toHaveBeenCalledWith("See /api/attachments/a1/download", ["a1"]);
  });
  it("retains the draft when sending fails", async () => {
    mocks.send.mockRejectedValueOnce(new Error("offline"));
    const user = userEvent.setup();
    renderWithI18n(<SpecCommentComposer specId="s3" versionId="v3" onSend={mocks.send} />);
    const input = screen.getByRole("textbox", { name: "Comment" });
    await user.type(input, "Please review");
    await user.click(screen.getByRole("button", { name: "Send" }));
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    expect(input).toHaveValue("Please review");
  });
});
