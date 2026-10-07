# Chat Enter-to-Send Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Enter send a chat message and Shift+Enter insert a newline in both Web and Desktop clients.

**Architecture:** Both clients render the shared `@multica/views/chat` package, so the behavior belongs in its composer inside `ChatWindow`. A small event predicate isolates keyboard semantics from the sending mutation; the same shared view delivers the behavior to both hosts.

**Tech Stack:** React 19, TypeScript, Vitest, Testing Library, pnpm workspaces.

## Global Constraints

- Apply only to the shared chat composer; do not alter generic textareas or message APIs.
- Enter sends only when the event is not composing, Shift is not held, and the existing send action is enabled.
- Shift+Enter preserves the textarea's native newline behavior.
- Preserve IME composition behavior for Chinese, Japanese, and Korean input.

---

### Task 1: Shared chat-composer keyboard behavior

**Files:**
- Modify: `packages/views/chat/components/chat-window.tsx`
- Create: `packages/views/chat/components/chat-window.test.tsx`

**Interfaces:**
- Consumes: the composer textarea's existing draft state and existing submit/send callback in `ChatWindow`.
- Produces: a textarea `onKeyDown` handler that calls the existing send callback only for an eligible Enter event.

- [ ] **Step 1: Write the failing tests**

Create `packages/views/chat/components/chat-window.test.tsx` with a focused composer harness. Mock only the existing data hooks needed to render the composer, then exercise its textarea with `@testing-library/user-event`.

```tsx
it("sends the draft when Enter is pressed", async () => {
  renderChatComposer({ draft: "hello", canSend: true });
  await userEvent.type(screen.getByRole("textbox"), "{Enter}");
  expect(sendMessage).toHaveBeenCalledWith("hello");
});

it("keeps a newline when Shift+Enter is pressed", async () => {
  renderChatComposer({ draft: "first", canSend: true });
  await userEvent.type(screen.getByRole("textbox"), "{Shift>}{Enter}{/Shift}second");
  expect(sendMessage).not.toHaveBeenCalled();
  expect(screen.getByRole("textbox")).toHaveValue("first\\nsecond");
});

it("does not send Enter while an IME composition is active", () => {
  const textarea = renderChatComposer({ draft: "你", canSend: true });
  fireEvent.keyDown(textarea, { key: "Enter", isComposing: true });
  expect(sendMessage).not.toHaveBeenCalled();
});

it("does not send an unavailable draft", () => {
  const textarea = renderChatComposer({ draft: "   ", canSend: false });
  fireEvent.keyDown(textarea, { key: "Enter" });
  expect(sendMessage).not.toHaveBeenCalled();
});
```

- [ ] **Step 2: Run the focused test and verify it fails**

Run:

```bash
pnpm --filter @multica/views test -- chat-window.test.tsx
```

Expected: the Enter-send assertion fails because the current textarea leaves Enter to its default newline action.

- [ ] **Step 3: Add the minimal handler in the shared composer**

In `ChatWindow`, attach the handler to the existing composer textarea. Reuse the existing guard that decides whether sending is allowed; do not duplicate mutation logic.

```tsx
const handleComposerKeyDown = (event: React.KeyboardEvent<HTMLTextAreaElement>) => {
  if (
    event.key !== "Enter" ||
    event.shiftKey ||
    event.nativeEvent.isComposing ||
    !canSend
  ) {
    return;
  }

  event.preventDefault();
  void handleSend();
};
```

Pass `onKeyDown={handleComposerKeyDown}` to the existing textarea. Ensure `canSend` includes the component's existing trimmed-draft, disabled, and pending-send conditions.

- [ ] **Step 4: Run the focused tests and verify they pass**

Run:

```bash
pnpm --filter @multica/views test -- chat-window.test.tsx
```

Expected: PASS for all four keyboard cases.

- [ ] **Step 5: Run shared-view quality checks**

Run:

```bash
pnpm --filter @multica/views typecheck
pnpm --filter @multica/views lint
```

Expected: both commands exit 0.

- [ ] **Step 6: Commit the implementation**

```bash
git add packages/views/chat/components/chat-window.tsx packages/views/chat/components/chat-window.test.tsx
git commit -m "feat: send chat messages with Enter"
```

### Task 2: Validate both hosts consume the shared behavior

**Files:**
- Modify: none
- Test: `apps/web/app/[workspaceSlug]/(dashboard)/chat/page.tsx`
- Test: `apps/desktop/src/renderer/src/routes.tsx`

**Interfaces:**
- Consumes: `ChatPage` and `ChatWindow` exported by `@multica/views/chat`.
- Produces: evidence that neither Web nor Desktop owns a competing composer implementation.

- [ ] **Step 1: Confirm both hosts render the shared chat page**

Run:

```bash
rg -n '@multica/views/chat|ChatPage|ChatWindow' \
  'apps/web/app/[workspaceSlug]/(dashboard)/chat/page.tsx' \
  apps/desktop/src/renderer/src
```

Expected: Web and Desktop route their chat UI through the shared `@multica/views/chat` implementation; no second composer key handler exists.

- [ ] **Step 2: Typecheck each host**

Run:

```bash
pnpm --filter @multica/web typecheck
pnpm --filter @multica/desktop typecheck
```

Expected: both commands exit 0.

- [ ] **Step 3: Commit only if a host integration adjustment was required**

```bash
git add apps/web apps/desktop
git commit -m "test: verify shared chat composer integration"
```

Skip this commit when Task 1's shared implementation required no host-file change.

## Self-review

- Spec coverage: Task 1 covers Enter send, Shift+Enter newline, IME safety, and unavailable drafts. Task 2 verifies the shared implementation reaches Web and Desktop.
- Scope: no message API, settings, or unrelated editor behavior changes are included.
- Type consistency: the handler uses React's existing `KeyboardEvent<HTMLTextAreaElement>` contract and delegates to the component's existing send callback.
