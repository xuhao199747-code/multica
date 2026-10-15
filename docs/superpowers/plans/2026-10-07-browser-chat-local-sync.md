# Browser Chat Local Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Synchronize browser-created Multica projects and direct-chat transcripts to a paired Mac directory.

**Architecture:** Reuse existing project, chat-session and message APIs. A new `multica sync` command polls browser-visible snapshots and atomically mirrors Markdown under an explicit local root. Later work may replace polling with an ordered event stream without changing the on-disk format.

**Tech Stack:** Go, PostgreSQL/sqlc, existing Multica CLI daemon configuration, Go tests.

## Global Constraints

- Reuse `chat_session.project_id`; do not add another association table.
- Browser mutations must succeed even if local sync is unavailable.
- Only a metadata-matched directory under the configured root may be deleted.

---

### Task 1: Local filesystem mirror

**Files:**
- Create: `server/internal/daemon/localsync/exporter.go`
- Create: `server/internal/daemon/localsync/exporter_test.go`

- [ ] Write failing filesystem tests for initial export, sanitized names, rename, atomic rewrite, project deletion move-to-未归类, empty-parent cleanup, and metadata-verified deletion.
- [ ] Implement root-constrained path resolution, Markdown rendering, metadata marker validation, and atomic writes.
- [ ] Run `go test ./internal/daemon/localsync`.

### Task 2: CLI daemon lifecycle

**Files:**
- Create: `server/cmd/multica/cmd_sync.go`
- Test: `server/cmd/multica/cmd_sync_test.go`

- [ ] Write failing CLI tests for `multica sync once --root` and `multica sync start --root`.
- [ ] Validate the root, fetch the existing project/session/message APIs, and start a retrying poll loop.
- [ ] Run focused CLI tests.

### Task 3: Browser project-chat workflow and documentation

**Files:**
- Modify: `apps/web/app/[workspaceSlug]/(dashboard)/chat/page.tsx`
- Modify: `packages/core/chat/mutations.ts`
- Test: matching Chat page and mutation test files
- Modify: `apps/docs/content/docs/chat.mdx`

- [ ] Write failing UI tests for creating a Chat with a project and moving a Chat between projects.
- [ ] Confirm the existing project-scoped create/move flow uses `project_id`; do not change Chat context isolation.
- [ ] Document browser snapshot syncing, filesystem behavior, deletion semantics, and recovery.
- [ ] Run focused web/core tests and the complete relevant Go suites.
