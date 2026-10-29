# Single-user Settings Cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove task-management and multi-member Settings UI that no longer belongs in the single-user agent console.

**Architecture:** Keep the cleanup at the shared Settings composition boundary used by web and desktop. Remove obsolete entries from the Settings page and its search-index source, then reduce Preferences to account/device settings that remain reachable from the retained navigation.

**Tech Stack:** React, TypeScript, Vitest, Testing Library, i18next.

## Global Constraints

- Change only shared web/desktop Settings UI; do not alter backend APIs, routes, or persisted stores.
- Preserve account profile, region, appearance, floating chat, notifications, shortcuts, tokens, workspace general, connections, billing, plugins, and device tabs.
- Keep direct URLs to removed tabs safe through the existing first-entry fallback.
- Use a failing focused test before each behavior change.
- Leave the existing `AGENTS.md` runtime modification untouched.

---

### Task 1: Remove obsolete Settings pages from composition and search

**Files:**
- Modify: `packages/views/settings/components/settings-page.tsx`
- Modify: `packages/views/settings/components/use-settings-search-index.ts`
- Modify: `packages/views/settings/components/settings-page.test.tsx`

**Interfaces:**
- Consumes: `SettingsPage`'s `groups` entry list and `useSettingsSearchIndex(pages)` visible-page filtering.
- Produces: Navigation and search results that omit `members`, `issue-statuses`, `wakeups`, `labels`, `properties`, and `quick-actions`.

- [ ] **Step 1: Write the failing Settings shell regression test**

```tsx
it("keeps task-management and member pages out of navigation and search", () => {
  renderWithI18n(<SettingsPage />);

  for (const name of ["Members", "Issue Statuses", "Wakeups", "Labels", "Properties", "Quick actions"]) {
    expect(screen.queryByRole("link", { name })).toBeNull();
  }

  fireEvent.change(screen.getByRole("searchbox", { name: "Search settings" }), {
    target: { value: "side preview" },
  });
  expect(screen.getByText("No matching settings")).toBeInTheDocument();
});
```

- [ ] **Step 2: Run the focused test to verify it fails**

Run: `pnpm exec vitest run packages/views/settings/components/settings-page.test.tsx`

Expected: FAIL because `Members` and task-management pages still render.

- [ ] **Step 3: Remove obsolete entries and their search-index rows**

```tsx
// settings-page.tsx: retain the workspace general and optional billing entries,
// then retain only the Connections & extensions subgroup.
entries: [
  entry("workspace", t(($) => $.page.tabs.general), Settings, <WorkspaceTab />, {
    adminOnly: true,
  }),
  ...(billingEnabled ? [entry("billing", t(($) => $.page.tabs.billing), CreditCard, <BillingTab />)] : []),
],
```

Delete the imports and `groups` entries for `MembersTab`, `IssueStatusesTab`,
`WakeupsTab`, `LabelsTab`, `PropertiesTab`, and `QuickActionsTab`. Delete the
corresponding `useSettingsSearchIndex` rows, including the task-opening search
row that Preferences will no longer render.

- [ ] **Step 4: Run the focused test to verify it passes**

Run: `pnpm exec vitest run packages/views/settings/components/settings-page.test.tsx`

Expected: PASS.

- [ ] **Step 5: Commit the completed task**

```bash
git add packages/views/settings/components/settings-page.tsx packages/views/settings/components/use-settings-search-index.ts packages/views/settings/components/settings-page.test.tsx
git commit -m "feat(settings): hide collaboration controls"
```

### Task 2: Reduce Preferences to retained account and device settings

**Files:**
- Modify: `packages/views/settings/components/preferences-tab.tsx`
- Modify: `packages/views/settings/components/preferences-tab.test.tsx`

**Interfaces:**
- Consumes: `PreferencesTab` and retained `useChatStore` floating-chat preference.
- Produces: Region, appearance, and floating-chat controls without issue/comment/create-task controls.

- [ ] **Step 1: Write the failing Preferences regression test**

```tsx
it("keeps only the floating-chat control from the retired task preferences", () => {
  render(<PreferencesTab />, { wrapper: I18nWrapper });

  expect(screen.getByRole("switch", { name: enSettings.chat.floating_label })).toBeInTheDocument();
  expect(screen.queryByRole("switch", { name: "Pin comment bar to bottom" })).toBeNull();
  expect(screen.queryByRole("combobox", { name: "When replying to a running agent" })).toBeNull();
  expect(screen.queryByRole("group", { name: "Clicking a card or row opens" })).toBeNull();
  expect(screen.queryByRole("table")).toBeNull();
});
```

- [ ] **Step 2: Run the focused test to verify it fails**

Run: `pnpm exec vitest run packages/views/settings/components/preferences-tab.test.tsx`

Expected: FAIL because the retired controls still render.

- [ ] **Step 3: Remove task-only Preferences sections and dependencies**

```tsx
<SettingsSection
  title={t(($) => $.preferences.comments_title)}
  scope="device"
  anchor="comments"
>
  <SettingsCard>
    <FloatingChatRow />
  </SettingsCard>
</SettingsSection>
```

Delete `StickyCommentBarRow`, `RunningAgentReplyRow`, `IssueOpeningRow`, and
`IssueFieldsMatrix`, along with their imports from task-specific stores and UI
controls. Keep `FloatingChatRow`, language, timezone, theme, and platform
appearance rows.

- [ ] **Step 4: Run the focused test to verify it passes**

Run: `pnpm exec vitest run packages/views/settings/components/preferences-tab.test.tsx`

Expected: PASS.

- [ ] **Step 5: Commit the completed task**

```bash
git add packages/views/settings/components/preferences-tab.tsx packages/views/settings/components/preferences-tab.test.tsx
git commit -m "feat(settings): simplify personal preferences"
```

### Task 3: Verify shared Settings behavior

**Files:**
- Verify: `packages/views/settings/components/settings-page.test.tsx`
- Verify: `packages/views/settings/components/preferences-tab.test.tsx`

**Interfaces:**
- Consumes: the two completed Settings UI changes.
- Produces: evidence that retained Settings controls, removed controls, and TypeScript all agree.

- [ ] **Step 1: Run both focused component suites**

Run: `pnpm exec vitest run packages/views/settings/components/settings-page.test.tsx packages/views/settings/components/preferences-tab.test.tsx`

Expected: PASS with zero failed tests.

- [ ] **Step 2: Run type checking**

Run: `pnpm typecheck`

Expected: PASS with exit code 0.

- [ ] **Step 3: Inspect the final diff and whitespace**

Run: `git diff HEAD~2..HEAD --check && git status --short`

Expected: no whitespace errors; only the preserved `AGENTS.md` runtime modification remains uncommitted.
