# PWA Portrait Orientation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Request portrait orientation when the installed Multica web app launches.

**Architecture:** Add the standard Web App Manifest `orientation` member to the existing Next.js manifest route. The existing browser-level lock and landscape overlay remain as fallbacks for launches that cannot apply the manifest constraint.

**Tech Stack:** Next.js metadata route, TypeScript, Vitest.

## Global Constraints

- Keep the change limited to the web PWA manifest and its colocated test.
- Preserve the existing browser fallback for non-installed web sessions.
- Use the standard `"portrait"` Web App Manifest orientation value.

---

### Task 1: Declare and verify PWA portrait orientation

**Files:**
- Modify: `apps/web/app/manifest.test.ts`
- Modify: `apps/web/app/manifest.ts`

**Interfaces:**
- Consumes: `manifest(): MetadataRoute.Manifest`
- Produces: a manifest response with `orientation: "portrait"`

- [ ] **Step 1: Write the failing test**

```ts
it("requests portrait orientation for installed web apps", () => {
  expect(manifest().orientation).toBe("portrait");
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `pnpm --filter @multica/web test app/manifest.test.ts`

Expected: FAIL because `manifest().orientation` is undefined.

- [ ] **Step 3: Write minimal implementation**

```ts
display: "standalone",
orientation: "portrait",
```

- [ ] **Step 4: Run test to verify it passes**

Run: `pnpm --filter @multica/web test app/manifest.test.ts`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/web/app/manifest.ts apps/web/app/manifest.test.ts docs/superpowers/plans/2026-10-30-pwa-portrait-orientation.md
git commit -m "fix(web): request portrait PWA orientation"
```
