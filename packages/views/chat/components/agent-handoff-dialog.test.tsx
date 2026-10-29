import { describe, expect, it, vi } from "vitest";
import { useState, type ComponentProps } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@multica/core/i18n/react";
import type { ChatSession } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enChat from "../../locales/en/chat.json";
import { AgentHandoffDialog } from "./agent-handoff-dialog";
import type {
  AgentHandoffDestination,
  AgentHandoffDialog as AgentHandoffDialogState,
} from "./use-agent-handoff";

const TEST_RESOURCES = { en: { common: enCommon, chat: enChat } };

function session(overrides: Partial<ChatSession> = {}): ChatSession {
  return {
    id: "b-chat",
    workspace_id: "ws-1",
    agent_id: "agent-b",
    creator_id: "user-1",
    title: "Code implementation",
    status: "active",
    has_unread: false,
    created_at: "2026-10-28T00:00:00Z",
    updated_at: "2026-10-29T00:00:00Z",
    ...overrides,
  };
}

function dialog(overrides: Partial<AgentHandoffDialogState> = {}): AgentHandoffDialogState {
  return {
    sourceSessionId: "a-chat",
    target: { id: "agent-b", name: "Code Agent" } as AgentHandoffDialogState["target"],
    draft: "目标：\nShip the dialog\n已确认的需求：\nEditable\n需要实现：\nA dialog\n不要改：\nMobile\n验收条件：\nTests pass\n参考位置：\nchat\n不确定项：\nNone",
    destination: { kind: "new" },
    error: null,
    deliverySession: null,
    availableSessions: [session()],
    busySessionIds: new Set(),
    targetAvailable: true,
    ...overrides,
  };
}

function renderDialog(overrides: Partial<ComponentProps<typeof AgentHandoffDialog>> = {}) {
  const onDraftChange = vi.fn();
  const onDestinationChange = vi.fn();
  const onCancel = vi.fn();
  const onSend = vi.fn().mockResolvedValue(undefined);
  const props = {
    dialog: dialog(),
    onDraftChange,
    onDestinationChange,
    onCancel,
    onSend,
    ...overrides,
  };

  function Harness() {
    const [currentDialog, setCurrentDialog] = useState(props.dialog);
    const handleDraftChange = (draft: string) => {
      props.onDraftChange(draft);
      setCurrentDialog((current) => ({ ...current, draft }));
    };
    const handleDestinationChange = (destination: AgentHandoffDestination) => {
      props.onDestinationChange(destination);
      setCurrentDialog((current) => ({ ...current, destination }));
    };
    return (
      <AgentHandoffDialog
        {...props}
        dialog={currentDialog}
        onDraftChange={handleDraftChange}
        onDestinationChange={handleDestinationChange}
      />
    );
  }

  const result = render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <Harness />
    </I18nProvider>,
  );

  return { ...result, props };
}

describe("AgentHandoffDialog", () => {
  it("defaults to a new B chat and delivers edited text", async () => {
    const user = userEvent.setup();
    const { props } = renderDialog();

    expect(screen.getByRole("radio", { name: /new handoff conversation/i })).toBeChecked();
    await user.clear(screen.getByLabelText(/^handoff$/i));
    await user.type(screen.getByLabelText(/^handoff$/i), "edited handoff");
    expect(props.onDraftChange).toHaveBeenLastCalledWith("edited handoff");
    await user.click(screen.getByRole("button", { name: /send to code agent/i }));

    expect(props.onSend).toHaveBeenCalledWith("edited handoff", { kind: "new" });
  });

  it("disables a busy B conversation", () => {
    renderDialog({ dialog: dialog({ busySessionIds: new Set(["b-chat"]) }) });

    expect(screen.getByRole("radio", { name: /code implementation/i })).toBeDisabled();
    expect(screen.getByText(/currently busy/i)).toBeVisible();
  });

  it("disables every destination and explains when B is no longer available", () => {
    renderDialog({ dialog: dialog({ targetAvailable: false }) });

    expect(screen.getByRole("radio", { name: /new handoff conversation/i })).toBeDisabled();
    expect(screen.getByRole("radio", { name: /code implementation/i })).toBeDisabled();
    expect(screen.getByRole("button", { name: /send to code agent/i })).toBeDisabled();
    expect(screen.getByText(/no longer available/i)).toBeVisible();
  });

  it("explains how to inspect a possibly delivered message", () => {
    renderDialog({ dialog: dialog({
      error: "delivery_ambiguous",
      deliverySession: { id: "new-b-chat", title: "New B handoff" },
    }) });

    expect(screen.getByRole("alert")).toHaveTextContent(/may already have been delivered/i);
    expect(screen.getByRole("alert")).toHaveTextContent("New B handoff");
    expect(screen.getByRole("alert")).toHaveTextContent("new-b-chat");
    expect(screen.getByRole("alert")).toHaveTextContent(/before retrying manually/i);
  });

  it("keeps the dialog and draft open after delivery fails", async () => {
    const user = userEvent.setup();
    const onSend = vi.fn().mockRejectedValue(new Error("network down"));
    const { props } = renderDialog({ onSend });

    await user.click(screen.getByRole("button", { name: /send to code agent/i }));

    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("network down"));
    expect(screen.getByRole("dialog")).toBeVisible();
    expect(screen.getByLabelText(/^handoff$/i)).toHaveValue(props.dialog.draft);
    expect(props.onCancel).not.toHaveBeenCalled();
  });

  it("disables blank and in-flight delivery", async () => {
    const user = userEvent.setup();
    let resolveSend: (() => void) | undefined;
    const onSend = vi.fn(() => new Promise<void>((resolve) => { resolveSend = resolve; }));
    const { props } = renderDialog({ onSend, dialog: dialog({ draft: "   " }) });

    expect(screen.getByRole("button", { name: /send to code agent/i })).toBeDisabled();
    await user.type(screen.getByLabelText(/^handoff$/i), "ready");
    await user.click(screen.getByRole("button", { name: /send to code agent/i }));

    expect(screen.getByRole("button", { name: /sending to code agent/i })).toHaveAttribute("aria-busy", "true");
    expect(screen.getByRole("button", { name: /sending to code agent/i })).toBeDisabled();
    resolveSend?.();
    await waitFor(() => expect(props.onSend).toHaveBeenCalled());
  });
});
