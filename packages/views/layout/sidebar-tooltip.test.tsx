import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { Sidebar, SidebarMenuButton, SidebarProvider } from "@multica/ui/components/ui/sidebar";
import { TooltipProvider } from "@multica/ui/components/ui/tooltip";

vi.mock("@multica/ui/hooks/use-mobile", () => ({ useIsCompact: () => true }));

describe("compact sidebar tooltip", () => {
  it("shows the label on hover and keyboard focus", async () => {
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
});
