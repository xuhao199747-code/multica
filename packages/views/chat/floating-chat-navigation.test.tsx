import { render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({ pathname: "/acme/chat", mobile: true, isOpen: true, setOpen: vi.fn() }));
vi.mock("@multica/core/chat", () => ({ useChatStore: (selector: (s: unknown) => unknown) => selector({ floatingChatEnabled: true, isOpen: state.isOpen, setOpen: state.setOpen }) }));
vi.mock("@multica/core/paths", () => ({ useWorkspacePaths: () => ({ chat: () => "/acme/chat" }) }));
vi.mock("../navigation", () => ({ useNavigation: () => ({ pathname: state.pathname }) }));
vi.mock("@multica/ui/hooks/use-mobile", () => ({ useIsMobile: () => state.mobile }));
vi.mock("./components/chat-window", () => ({ ChatWindow: () => <div>floating window</div> }));
vi.mock("./components/chat-fab", () => ({ ChatFab: () => null }));

import { FloatingChat } from "./floating-chat";

describe("floating chat navigation", () => {
  beforeEach(() => { state.pathname = "/acme/chat"; state.mobile = true; state.isOpen = true; state.setOpen.mockReset(); });
  it("closes the full-screen floating chat when a phone navigates away from chat", () => {
    const view = render(<FloatingChat />);
    state.pathname = "/acme/issues";
    view.rerender(<FloatingChat />);
    expect(state.setOpen).toHaveBeenCalledWith(false);
  });
  it("keeps desktop floating chat open when navigating", () => {
    state.mobile = false;
    const view = render(<FloatingChat />);
    state.pathname = "/acme/issues";
    view.rerender(<FloatingChat />);
    expect(state.setOpen).not.toHaveBeenCalled();
  });
  it("does not close a floating chat when the route stays the same", () => {
    state.pathname = "/acme/issues";
    const view = render(<FloatingChat />);
    view.rerender(<FloatingChat />);
    expect(state.setOpen).not.toHaveBeenCalled();
  });
});
