import { test, expect } from "@playwright/test";
import { createTestApi, loginAsDefault } from "./helpers";

test("creates and publishes a project spec with comments", async ({ page }) => {
  const api = await createTestApi();
  const project = await api.createProject(`Spec E2E ${Date.now()}`);
  let specId: string | undefined;
  try {
    const slug = await loginAsDefault(page);
    await page.goto(`/${slug}/projects/${project.id}`, { waitUntil: "domcontentloaded" });
    await page.getByRole("button", { name: "New spec" }).click();
    await page.getByLabel("Spec title").fill("E2E requirements");
    await page.getByRole("button", { name: "Create", exact: true }).click();
    await page.waitForURL(/\/specs\//, { waitUntil: "domcontentloaded" });
    specId = decodeURIComponent(new URL(page.url()).pathname.split("/").pop()!);
    await expect(page.getByRole("heading", { name: "E2E requirements" })).toBeVisible();
    await page.getByRole("button", { name: "Publish version" }).first().click();
    await page.locator("form").filter({ has: page.getByLabel("Change summary") }).locator(".tiptap[contenteditable=true]").fill("The spec body");
    await page.getByLabel("Change summary").fill("Initial draft");
    await page.getByRole("button", { name: "Publish version" }).last().click();
    await expect(page.getByText("Initial draft")).toBeVisible();
    await page.getByRole("button", { name: "Submit for review" }).click();
    await expect(page.getByText("In review", { exact: true })).toBeVisible();
    await page.getByRole("group", { name: "Comment" }).locator(".tiptap[contenteditable=true]").fill("Please review");
    const upload = page.waitForResponse(response => response.url().includes("/api/upload-file") && response.request().method() === "POST");
    await page.locator('input[type="file"]').setInputFiles({ name: "review-note.txt", mimeType: "text/plain", buffer: Buffer.from("review attachment") });
    expect((await upload).ok()).toBeTruthy();
    await expect(page.getByText("review-note.txt").first()).toBeVisible();
    await page.getByRole("button", { name: "Send" }).click();
    await expect(page.getByText("Please review")).toBeVisible();
    await page.getByRole("button", { name: "Reply" }).first().click();
    await page.getByRole("group", { name: "Comment" }).locator(".tiptap[contenteditable=true]").fill("Thanks for reviewing");
    await page.getByRole("button", { name: "Send" }).click();
    await expect(page.getByText("Thanks for reviewing")).toBeVisible();
    await page.reload();
    await expect(page.getByText("Please review")).toBeVisible();
    await expect(page.getByText("Thanks for reviewing")).toBeVisible();
    await expect(page.getByText("Initial draft")).toBeVisible();
    await expect(page.getByText("review-note.txt").first()).toBeVisible();
  } finally {
    if (specId) await api.cleanupSpec(specId);
    await api.cleanup();
  }
});
