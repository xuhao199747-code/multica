import { devices, expect, test } from "@playwright/test";
import { createTestApi, loginAsDefault } from "./helpers";

test.use({ ...devices["Pixel 7"] });

test("mobile sidebar navigates and dismisses its expanded menu", async ({ page }) => {
  const api = await createTestApi();
  try {
    await loginAsDefault(page);
    const destinations = [
      ["Inbox", "inbox"],
      ["My Issues", "my-issues"],
      ["Chat", "chat"],
      ["Projects", "projects"],
      ["Autopilot", "autopilots"],
      ["Agents", "agents"],
      ["Squads", "squads"],
      ["Skills", "skills"],
      ["Runtimes", "runtimes"],
      ["Analytics", "usage"],
      ["Settings", "settings"],
      ["Issues", "issues"],
    ];
    for (const [name, path] of destinations) {
      const rail = page.locator('[data-sidebar="compact-rail"]');
      await expect(rail).toBeVisible();
      await rail.getByRole("link", { name, exact: true }).tap();
      await expect(page).toHaveURL(new RegExp(`/${path}(?:[/?#]|$)`), { timeout: 60000 });
      await expect(rail).toBeVisible();
      await expect(page.locator("main")).toBeVisible();
    }
    await page.getByRole("button", { name: "Expand sidebar" }).tap();
    const menu = page.getByRole("dialog", { name: "Sidebar" });
    await expect(menu).toBeVisible();
    await menu.getByRole("link", { name: "Projects", exact: true }).tap();
    await expect(page).toHaveURL(/\/projects(?:[/?#]|$)/);
    await expect(menu).toBeHidden();
    await expect(page.locator('[data-sidebar="compact-rail"]')).toBeVisible();

    await page.getByRole("button", { name: "Ask Multica" }).tap();
    await expect(page.getByText("Chat with your agents")).toBeVisible();
    await page.locator('[data-sidebar="compact-rail"]').getByRole("link", { name: "Issues", exact: true }).tap();
    await expect(page).toHaveURL(/\/issues(?:[/?#]|$)/);
    await expect(page.getByRole("button", { name: "Ask Multica" })).toBeVisible();
    await expect(page.getByText("Chat with your agents").locator('xpath=ancestor::*[@aria-hidden][1]')).toHaveAttribute("aria-hidden", "true");
    await expect(page.getByRole("heading", { name: "Issues", exact: true })).toBeVisible();
  } finally {
    await api.cleanup();
  }
});
