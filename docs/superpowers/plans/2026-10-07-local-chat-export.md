# Local Chat Export Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep a local Markdown mirror of each Desktop Chat and remove it when the chat is deleted.

**Architecture:** The Web Chat client observes its existing create/message/update/delete mutations and calls a narrowly scoped Desktop preload API only when available. Electron main owns all filesystem access, validates a user-selected root, and atomically maintains one ID-stable directory per chat.

**Tech Stack:** TypeScript, Electron IPC, React Query mutations, Vitest.

## Global Constraints

- Export only when a user has selected an absolute local root directory.
- Use the first effective user message as the initial title.
- Preserve a short chat-session ID suffix in every directory name.
- Never expose unrestricted filesystem APIs to the renderer.
- A local-write failure must not block a remote Multica mutation.

---

### Task 1: Electron exporter and its tests

**Files:**
- Create: `apps/desktop/src/main/chat-exporter.ts`
- Test: `apps/desktop/src/main/chat-exporter.test.ts`
- Modify: `apps/desktop/src/main/local-directory.ts`
- Modify: `apps/desktop/src/main/index.ts`

**Interfaces:**
- Produces `ChatExporter.sync(input)` and `ChatExporter.delete(sessionID)`.
- Produces preload IPC handlers `chat-export:configure`, `chat-export:sync`, and `chat-export:delete`.

- [ ] Write tests for title sanitization, first-message directory creation, atomic Markdown rewrite, rename without ID change, collision suffixes, and metadata-verified deletion.
- [ ] Run the focused test and confirm it fails because the exporter does not exist.
- [ ] Implement the exporter using `mkdir`, temporary-file-plus-`rename`, and a `.multica-chat-export.json` metadata marker.
- [ ] Register only validated IPC handlers in Electron main and run the focused test to green.

### Task 2: Narrow preload bridge

**Files:**
- Modify: `apps/desktop/src/preload/index.ts`
- Modify: `apps/desktop/src/preload/index.d.ts`
- Test: `apps/desktop/src/preload/index.test.ts` (create if no matching coverage exists)

**Interfaces:**
- Exposes `window.desktopAPI.chatExport.configure()`, `sync(payload)`, and `delete(sessionID)`.

- [ ] Write a failing preload test asserting only the three typed chat-export calls are exposed.
- [ ] Implement the bridge with fixed channel names and typed payload validation at the main-process boundary.
- [ ] Run the preload and exporter tests.

### Task 3: Chat lifecycle synchronization

**Files:**
- Modify: `packages/core/chat/mutations.ts`
- Modify: `packages/core/chat/mutations.test.tsx`
- Modify: `packages/core/chat/queries.ts` or the Chat message owner identified during implementation

**Interfaces:**
- Consumes the optional `window.desktopAPI.chatExport` bridge.
- Calls `sync` after successful first user message, message completion, and title update; calls `delete` after confirmed session deletion.

- [ ] Add failing mutation tests for first-message title selection, rename synchronization, and best-effort deletion.
- [ ] Implement a best-effort synchronizer that serializes visible messages to Markdown and never changes API mutation success/failure behavior.
- [ ] Run package-core chat tests.

### Task 4: Configuration UX and verification

**Files:**
- Modify: the existing Desktop settings/local-directory surface located during Task 2
- Modify: `apps/desktop/src/main/local-directory.test.ts`
- Modify: `apps/docs/content/docs/desktop.mdx` or the matching desktop documentation file

- [ ] Add a directory picker labelled for local chat backups and display the selected path.
- [ ] Add tests for cancel, invalid path, and persistence across relaunch.
- [ ] Document the export location, title rules, archive behavior, and destructive-delete behavior.
- [ ] Run focused desktop and core tests, then the relevant package test suites.
