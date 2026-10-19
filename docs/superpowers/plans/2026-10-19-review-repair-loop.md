# Review Repair Loop Implementation Plan

**Goal:** Automatically route actionable review findings to the original execution agent and re-review verified repairs.

**Architecture:** Persist and validate structured per-issue review outcomes and loop state; atomically enqueue authorized repair and re-review tasks, with idempotency and bounded retries. Route errors and decisions to the existing inbox.

**Tech stack:** Go, PostgreSQL/sqlc, existing scheduler/task queue, React shared views.

## Global constraints

- Work only in this isolated checkout and isolated database from `.env.worktree`; never run tests against the production database.
- Maximum three repair rounds; unknown or malformed outcomes stop and notify; no merge or deploy.
- No real-agent smoke tests without explicit authorization.

### Task 1: Review outcomes and workflow state
- [ ] Add failing tests for actionable, passed, unknown, failed and daily-summary results; implement strict structured outcomes with unambiguous issue association and safe fallback.
- [ ] Add persisted round, participant and task identity with idempotent, atomic transitions; add concurrency and duplicate callback tests.

### Task 2: Repair and re-review
- [ ] Add failing tests for original executor selection, workspace/runtime authorization, missing executor, failures and three-round limit.
- [ ] Enqueue repair and subsequent re-review with required evidence; ensure tasks do not self-review and do not recurse from summaries.
- [ ] Report progress and escalations to recipient, and update issue state only when warranted by successful transitions.

### Task 3: Verify and deliver
- [ ] Run targeted isolated database and component tests; verify migration and existing behavior.
- [ ] Record real limitations and do not deploy incomplete or unverified functionality.
