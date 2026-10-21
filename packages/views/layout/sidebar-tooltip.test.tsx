import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { Sidebar, SidebarMenuButton, SidebarProvider, useSidebar } from "@multica/ui/components/ui/sidebar";
import { TooltipProvider } from "@multica/ui/components/ui/tooltip";

const viewport = vi.hoisted(() => ({ compact: true }));
vi.mock("@multica/ui/hooks/use-mobile", () => ({ useIsCompact: () => viewport.compact }));

describe("compact sidebar tooltip", () => {
  it("shows the label on hover and keyboard focus", async () => {
    viewport.compact = true;
    const user = userEvent.setup();
    render(
      <TooltipProvider delay={0}>
        <SidebarProvider>
          <Sidebar><SidebarMenuButton tooltip="Inbox">Inbox</SidebarMenuButton></Sidebar>
        </SidebarProvider>
      </TooltipProvider>,
    );
    const button = screen.getByRole("button", { name: "Inbox" });
    await user.hover(button);
    await waitFor(() => expect(document.querySelector('[data-slot="tooltip-content"][data-open]')).toHaveTextContent("Inbox"));
    await user.unhover(button);
    await waitFor(() => expect(document.querySelector('[data-slot="tooltip-content"][data-open]')).toBeNull());
    await user.tab();
    expect(button).toHaveFocus();
    await waitFor(() => expect(document.querySelector('[data-slot="tooltip-content"][data-open]')).toHaveTextContent("Inbox"));
  });

  it.each([
    ["expanded desktop", false, true, false, true],
    ["collapsed desktop", false, false, false, false],
    ["open compact sheet", true, true, true, true],
  ])("gates the tooltip in %s", async (_name, compact, open, mobileOpen, hidden) => {
    viewport.compact = compact;
    const user = userEvent.setup();
    function Controls() {
      const { setOpenMobile } = useSidebar();
      return <>
        <button onClick={() => setOpenMobile(mobileOpen)}>Set sheet</button>
        <SidebarMenuButton tooltip="Inbox">Inbox</SidebarMenuButton>
      </>;
    }
    render(<TooltipProvider delay={0}><SidebarProvider defaultOpen={open}><Controls /></SidebarProvider></TooltipProvider>);
    if (mobileOpen) await user.click(screen.getByRole("button", { name: "Set sheet" }));
    await user.hover(screen.getByRole("button", { name: "Inbox" }));
    await waitFor(() => {
      const popup = document.querySelector('[data-slot="tooltip-content"][data-open]');
      expect(popup).not.toBeNull();
      expect(popup?.hasAttribute("hidden")).toBe(hidden);
    });
  });
});
