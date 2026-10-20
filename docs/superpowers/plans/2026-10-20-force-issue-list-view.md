# Force Issue Surfaces to List View Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Render every task-oriented issue surface in list mode only and remove every user-facing alternate-mode control.

**Architecture:** The shared `IssueViewState` defaults to `list` and normalizes persisted legacy mode values during rehydration. Every production task-surface caller supplies only `list`, so the existing surface-controller fallback repairs any stale in-memory selection. `IssueDisplayControls` no longer renders a view picker. Project overview display preferences remain unchanged because they use the independent `ProjectViewMode` store.

**Tech Stack:** TypeScript, React, Zustand, Vitest, pnpm/Turbo.

## Global Constraints

- Limit the change to web/desktop shared task issue surfaces; leave `apps/mobile/` unchanged.
- Keep project overview (`packages/views/projects/components/projects-page.tsx`) compact/cards selector unchanged.
- Preserve non-list renderer code and saved-view data; make those modes unreachable rather than deleting unrelated behavior.
- Use test-first red-green cycles and report the commands actually run.

---

### Task 1: Enforce the list-only invariant in the shared issue view store

**Files:**
- Modify: `packages/core/issues/stores/view-store.ts:365,399,804-851`
- Test: `packages/core/issues/stores/view-store-status.test.ts:89-156`

**Interfaces:**
- Consumes: `viewStoreSlice(set): IssueViewState` and `mergeViewStatePersisted<T>(persisted, current): T`.
- Produces: New and rehydrated production issue view state begins in `"list"`.

- [ ] **Step 1: Write the failing store tests**

  Add these cases inside `describe("issue view defaults")`:

  ```ts
  it("starts in list view", () => {
    expect(store.getState().viewMode).toBe("list");
  });

  it("normalizes a persisted alternate view mode to list", () => {
    const defaults = createStore<IssueViewState>()((set) => viewStoreSlice(set)).getState();

    expect(mergeViewStatePersisted({ viewMode: "board" }, defaults).viewMode).toBe("list");
  });
  ```

- [ ] **Step 2: Run the focused test to verify it fails**

  Run: `pnpm exec vitest run packages/core/issues/stores/view-store-status.test.ts`

  Expected: FAIL because the default is `board` and persisted `board` is merged through.

- [ ] **Step 3: Implement the smallest invariant**

  In `viewStoreSlice`, replace the default with:

  ```ts
  viewMode: "list",
  ```

  In `mergeViewStatePersisted`, add `viewMode: "list"` after `...p` in the `merged` object so a legacy persisted value cannot override the new default. Leave `setViewMode` unchanged: production callers constrain modes to list, while isolated renderer tests retain their existing setup capability.

- [ ] **Step 4: Run the focused test to verify it passes**

  Run: `pnpm exec vitest run packages/core/issues/stores/view-store-status.test.ts`

  Expected: PASS with zero failures.

- [ ] **Step 5: Commit the invariant and regression tests**

  ```bash
  git add packages/core/issues/stores/view-store.ts packages/core/issues/stores/view-store-status.test.ts
  git commit -m "feat: force issue views to list mode"
  ```

### Task 2: Restrict every task surface and remove the alternate-mode menu

**Files:**
- Modify: `packages/views/issues/components/issues-page.tsx:66`
- Modify: `packages/views/my-issues/components/my-issues-page.tsx:40`
- Modify: `packages/views/projects/components/project-detail.tsx:553`
- Modify: `packages/views/issues/components/issues-header.tsx:1-24,100,1891-1912,2402-2478`
- Test: `packages/views/issues/components/issues-page.test.tsx`

**Interfaces:**
- Consumes: `IssueSurfaceProps.modes: IssueSurfaceMode[]` and shared `IssueDisplayControls`.
- Produces: workspace, My Issues, and project-detail task surfaces pass `modes={["list"]}`; no `IssueDisplayControls` view-mode dropdown is rendered.

- [ ] **Step 1: Write the failing page regression test**

  In `packages/views/issues/components/issues-page.test.tsx`, change the shared fixture's initial `mockViewState.viewMode` to `"list"`; rename `renders board column headers` to `renders list status sections`; retain its existing status-heading assertions. Add the following regression after it:

  ```ts
  it("does not expose an alternate view picker", async () => {
    renderWithQuery(<IssuesPage />);

    await screen.findByText("Issues");
    expect(screen.queryByRole("menuitemradio", { name: "Board" })).not.toBeInTheDocument();
    expect(screen.queryByRole("menuitemradio", { name: "Table" })).not.toBeInTheDocument();
    expect(screen.queryByRole("menuitemradio", { name: "Swimlane" })).not.toBeInTheDocument();
  });
  ```

  Delete the board-specific `groups board columns by assignee` test because all production workspace task surfaces are list-only. Keep the table-branch test only if list grouping continues to exercise it; otherwise rewrite it to assert the list's status-group table request instead of board grouping.

- [ ] **Step 2: Run the focused page test to verify it fails**

  Run: `pnpm exec vitest run packages/views/issues/components/issues-page.test.tsx`

  Expected: FAIL because the display control still exposes the alternate-mode menu.

- [ ] **Step 3: Make the task surfaces list-only**

  Replace each task-surface `modes` prop with exactly:

  ```tsx
  modes={["list"]}
  ```

  Apply this to the workspace issues page, My Issues page, and project detail surface. Do not change the independent project overview page.

  In `issues-header.tsx`, remove the `viewMenuOpen` state and the imports used only by the mode selector (`ChartGantt`, `Columns3`, `List`, `Table2`, `Waves`, and `ViewMode`). Delete the entire `/* View toggle. */` conditional from `IssueDisplayControls`. Keep filter, display, date, and saved-view controls intact.

- [ ] **Step 4: Run the focused page and controller tests to verify they pass**

  Run: `pnpm exec vitest run packages/views/issues/components/issues-page.test.tsx packages/views/issues/surface/use-issue-surface-controller.test.tsx`

  Expected: PASS with zero failures. Do not change isolated controller tests that intentionally exercise non-list renderer behavior; those modes remain test-only, not production-reachable.

- [ ] **Step 5: Commit the surface wiring and tests**

  ```bash
  git add packages/views/issues/components/issues-page.tsx packages/views/my-issues/components/my-issues-page.tsx packages/views/projects/components/project-detail.tsx packages/views/issues/components/issues-header.tsx packages/views/issues/components/issues-page.test.tsx packages/views/issues/surface/use-issue-surface-controller.test.tsx
  git commit -m "feat: hide alternate issue view modes"
  ```

### Task 3: Run final scoped validation

**Files:**
- Verify only; no source changes expected.

**Interfaces:**
- Consumes: the list-only store invariant and list-only task-surface props.
- Produces: fresh test, typecheck, lint, and whitespace evidence for delivery.

- [ ] **Step 1: Run all directly affected tests**

  Run: `pnpm exec vitest run packages/core/issues/stores/view-store-status.test.ts packages/views/issues/components/issues-page.test.tsx packages/views/issues/surface/use-issue-surface-controller.test.tsx`

  Expected: PASS with zero failures.

- [ ] **Step 2: Run frontend type checking and linting**

  Run: `pnpm typecheck && pnpm lint`

  Expected: both commands exit 0. If either command finds a pre-existing unrelated failure, report the exact command and failure rather than suppressing it.

- [ ] **Step 3: Check the final patch scope**

  Run: `git diff --check HEAD^..HEAD && git status --short`

  Expected: no whitespace errors; report only files changed by this work and leave unrelated user changes untouched.
