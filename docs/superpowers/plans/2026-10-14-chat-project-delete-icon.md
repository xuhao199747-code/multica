# Chat Project Delete Icon Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a deletion icon and confirmation flow to Chat's project-tree row.

**Architecture:** `ChatThreadList` owns the project-row action affordance and delegates deletion intent to `ChatPage`. `ChatPage` owns dialog state and calls the existing `useDeleteProject` mutation, so server and local-sync behavior remain centralized.

**Tech Stack:** React, TypeScript, TanStack Query mutation, existing Multica UI Button/Dialog primitives, Vitest and Testing Library.

## Global Constraints

- The icon appears between rename and new-chat controls when the project row is hovered or keyboard-focused.
- Use a destructive icon-only button with an accessible `删除项目<项目名>` label.
- Confirm before mutation; do not close the dialog until mutation succeeds.
- Existing chat records remain server-owned and become unclassified after project deletion; browser UI must not access local files.
- Do not add an API, local store, or permanent client-side deletion path.

---

### Task 1: Project-row delete action and confirmation

**Files:**
- Modify: `packages/views/chat/components/chat-thread-list.tsx:92-111,563-623`
- Modify: `packages/views/chat/components/chat-thread-list.test.tsx:82-151`
- Modify: `packages/views/chat/chat-page.tsx:4-35,85-120,180-245,370-405`
- Test: `packages/views/chat/components/chat-thread-list.test.tsx`

**Interfaces:**
- `ChatThreadList` adds optional `onDeleteProject?: (project: Project) => void`.
- `ChatPage` passes `onDeleteProject`, owns `deletingProject: Project | null`, and invokes `useDeleteProject().mutate(project.id, callbacks)`.

- [ ] **Step 1: Write the failing component test**

Extend `renderList` with `onDeleteProject = vi.fn()`, pass it to `ChatThreadList`, and add:

```tsx
it("delegates the project delete icon without toggling the project", () => {
  const project = { id: "project-1", title: "论文助手", workspace_id: "ws-1" } as Project;
  const { onDeleteProject } = renderList(null, { projects: [project] });

  fireEvent.click(screen.getByRole("button", { name: "删除项目论文助手" }));

  expect(onDeleteProject).toHaveBeenCalledWith(project);
  expect(screen.getByText("暂无聊天")).toBeInTheDocument();
});
```

- [ ] **Step 2: Verify RED**

Run: `pnpm --filter @multica/views test chat-thread-list.test.tsx`

Expected: FAIL because no delete button or `onDeleteProject` prop exists.

- [ ] **Step 3: Add the row action**

Import `Trash2`. Add `onDeleteProject` to the component props. Between the existing Pencil and Plus controls, render:

```tsx
{onDeleteProject && (
  <button
    type="button"
    aria-label={`删除项目${project.title}`}
    title="删除项目"
    onClick={() => onDeleteProject(project)}
    className="inline-flex size-7 shrink-0 items-center justify-center rounded-sm text-muted-foreground opacity-0 transition-opacity hover:bg-destructive/10 hover:text-destructive focus-visible:opacity-100 group-hover/project:opacity-100"
  >
    <Trash2 className="size-3.5" />
  </button>
)}
```

- [ ] **Step 4: Add parent-owned confirmation and mutation**

In `ChatPage`, import `Trash2` and `useDeleteProject`; create `const deleteProject = useDeleteProject()` and `const [deletingProject, setDeletingProject] = useState<Project | null>(null)`. Pass `onDeleteProject={setDeletingProject}` to `ChatThreadList`.

Add a controlled `Dialog` beside the existing project dialog. Its title is `删除项目`; its description is `项目中的聊天会保留并归入未归类。本机同步的项目文件夹会移入废纸篓。`; its Cancel button clears state. Its destructive Confirm button calls:

```tsx
deleteProject.mutate(deletingProject.id, {
  onSuccess: () => setDeletingProject(null),
  onError: () => toast.error("删除项目失败，请稍后重试"),
});
```

Disable confirm while pending and set `aria-busy={deleteProject.isPending}`. The dialog remains open on error.

- [ ] **Step 5: Verify GREEN and type checks**

Run: `pnpm --filter @multica/views test chat-thread-list.test.tsx && pnpm typecheck`

Expected: PASS.

- [ ] **Step 6: Commit**

Run: `git add packages/views/chat/components/chat-thread-list.tsx packages/views/chat/components/chat-thread-list.test.tsx packages/views/chat/chat-page.tsx && git commit -m "feat: add chat project delete action"`

## Plan self-review

- Spec coverage: the single task adds the requested inline icon, confirmation, existing mutation use, user-visible local Trash consequence, and regression coverage.
- Placeholder scan: no deferred steps or unspecified code paths remain.
- Type consistency: the optional callback takes the same `Project` object passed from the row to `ChatPage`.

