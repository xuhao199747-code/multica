# Project Mica Delivery Workflow Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a project optionally use a project-scoped Mica coordination flow that turns a confirmed PRD/chat handoff into evidence-backed development, review, and project-specific delivery.

**Architecture:** Keep agents reusable globally, but bind their role and chat context to one project. Persist an immutable execution record for each user-confirmed handoff; Mica, developers, and reviewers act through server-enforced transitions on that record rather than through instructions alone. Keep existing Spec data and routes intact. Spec is deliberately deferred to a later, separate design for projects that do not enable the Mica workflow but still coordinate several agents; it is not the PRD source for this workflow.

**Tech Stack:** Go/Chi, PostgreSQL/sqlc, WebSocket events, TypeScript, TanStack Query, Zustand, shared `packages/core` and `packages/views`, Next.js and Electron adapters.

## Global Constraints

- A project is the required container for every newly-created chat; legacy chats remain readable but cannot be created or moved to `project_id = null`.
- Enabling collaboration is a project setting named “启用 Mica 协作”; it creates/opens only the project Mica chat, never auto-creates PRD, developer, or reviewer agents.
- Agents are reusable identities. Their project chats, role bindings, execution records, and accessible data are project-scoped.
- PRD remains authored in the PRD-agent chat and its Skill. An execution record is an immutable handoff/audit record, not a replacement PRD or a new canonical Spec system. A later Spec design may support non-workflow multi-agent collaboration, but is out of scope until this workflow is working.
- Mica recommends required roles and prepares an execution confirmation; it cannot start development, review, release, or create a role binding without the member’s confirmation.
- Backend authorization, not prompt text, controls role-sensitive mutations. Mica cannot edit code; reviewers cannot edit code or dispatch; developers cannot change scope, release, or self-approve.
- Workflow-created issues and follow-up issues inherit the execution record’s project and may not be moved across projects by an agent.
- A developer must supply commit/reference, test evidence, and change summary before review can begin. Review checks the execution snapshot as well as code and evidence.
- A record receives one initial review plus at most two automatic repair/review rounds. A new review requires a new developer delivery; otherwise it becomes `needs_member_decision`.
- Delivery is configured per project. Default is `notify_only`; no project gets Docker, container, URL, PR, or deployment actions by implication.
- Do not merge `codex/spec-integration` or revive DAWE-3 through DAWE-9 / DAWE-21 through DAWE-24 as part of this work. Preserve their data and code as input to the later, separate Spec decision.
- Follow repository package boundaries, use zod `parseWithFallback` at web/desktop API boundaries, add sqlc migrations with concurrent indexes in their own files, and run `make sqlc` after SQL changes.

---

## File structure

| Area | Responsibility |
| --- | --- |
| `server/migrations/606_*` onward | Add project workflow state, project-scoped roles, execution records, immutable snapshots, and record-to-issue links. |
| `server/pkg/db/queries/project_workflow.sql` | sqlc queries scoped by `workspace_id` and `project_id`; no foreign keys or cascades. |
| `server/internal/service/project_workflow.go` | Single transition engine, role checks, review-round guard, and evidence validation. |
| `server/internal/handler/project_workflow.go` | Project settings, agent binding, confirmation, delivery, and record APIs. |
| `server/internal/handler/chat.go`, `issue.go`, `issue_review_coordination.go` | Enforce project chat/issue inheritance and replace workspace-global review handoff with record-aware orchestration. |
| `packages/core/types/project-workflow.ts`, `api/schemas.ts`, `api/client.ts`, `project-workflow/*` | Shared contract, schemas, requests, queries, and mutations. |
| `packages/views/projects/components/project-workflow-*` | Project settings, Mica enablement, team binding prompts, execution confirmation, and delivery-record list/detail. |
| `packages/views/chat/*` | Current-chat-only `@Mica` handoff and confirmation-card entry point. |
| `apps/web` / `apps/desktop` route wiring | Expose the shared delivery-record view on both platforms without platform business logic. |

### Task 1: Persist project workflow settings, role bindings, and execution records

**Files:**
- Create: `server/migrations/606_project_workflow_settings.up.sql`
- Create: `server/migrations/607_project_workflow_settings_project_idx.up.sql`
- Create: `server/migrations/608_project_agent_binding.up.sql`
- Create: `server/migrations/609_project_agent_binding_unique_idx.up.sql`
- Create: `server/migrations/610_project_execution_record.up.sql`
- Create: `server/migrations/611_project_execution_record_project_idx.up.sql`
- Create: `server/migrations/612_project_execution_issue.up.sql`
- Create: `server/migrations/613_project_execution_issue_unique_idx.up.sql`
- Create: matching `.down.sql` files
- Create: `server/pkg/db/queries/project_workflow.sql`
- Modify: `server/pkg/db/generated/*` (generated only)
- Test: `server/internal/handler/project_workflow_test.go`

**Interfaces:**
- Produces `ProjectWorkflowSettings`, `ProjectAgentBinding`, `ProjectExecutionRecord`, and `ProjectExecutionIssue` sqlc models.
- `role` values: `mika`, `prd`, `developer`, `reviewer`.
- `delivery_mode` values: `notify_only`, `pull_request`, `manual_release`, `automated_release`.
- `record_state` values: `awaiting_confirmation`, `development`, `review`, `repair`, `needs_member_decision`, `delivering`, `completed`, `blocked`, `cancelled`.

- [ ] **Step 1: Write migration-level handler tests**

Add tests that create two projects in one workspace and assert that a workflow setting, role binding, record, and record-to-issue link from project A cannot be loaded through project B. Add a test that a second binding of the same `(project_id, agent_id, role)` is rejected, while the same agent may bind to another project.

- [ ] **Step 2: Run the focused tests and verify failure**

Run: `cd server && go test ./internal/handler -run 'TestProjectWorkflow' -count=1`

Expected: FAIL because the workflow API and sqlc queries do not yet exist.

- [ ] **Step 3: Add the tables and isolated concurrent indexes**

Use tables shaped as follows; do not add foreign keys or cascading deletes:

```sql
CREATE TABLE project_workflow_settings (
  project_id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL,
  enabled boolean NOT NULL DEFAULT false,
  project_context text NOT NULL DEFAULT '',
  delivery_mode text NOT NULL DEFAULT 'notify_only',
  delivery_config jsonb NOT NULL DEFAULT '{}'::jsonb,
  updated_at timestamptz NOT NULL DEFAULT now()
);
```

```sql
CREATE TABLE project_agent_binding (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL,
  project_id uuid NOT NULL,
  agent_id uuid NOT NULL,
  role text NOT NULL CHECK (role IN ('mika','prd','developer','reviewer')),
  created_at timestamptz NOT NULL DEFAULT now()
);
```

```sql
CREATE TABLE project_execution_record (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL,
  project_id uuid NOT NULL,
  source_chat_session_id uuid NOT NULL,
  source_agent_id uuid NOT NULL,
  snapshot jsonb NOT NULL,
  state text NOT NULL,
  review_round integer NOT NULL DEFAULT 0,
  delivery_result jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_by uuid NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
```

Create each `CREATE [UNIQUE] INDEX CONCURRENTLY` in its own migration. Generate sqlc after adding queries for workspace-scoped reads and updates guarded by the expected prior state.

- [ ] **Step 4: Implement query contracts and minimal handler fixtures**

Provide queries named `GetProjectWorkflowSettings`, `UpsertProjectWorkflowSettings`, `ListProjectAgentBindings`, `CreateProjectAgentBinding`, `CreateProjectExecutionRecord`, `GetProjectExecutionRecord`, `AdvanceProjectExecutionRecord`, `CreateProjectExecutionIssue`, and `ListProjectExecutionRecords`. Every query that reads or updates a project record takes both `workspace_id` and `project_id`.

- [ ] **Step 5: Run migrations, sqlc, and focused tests**

Run: `make sqlc && cd server && go test ./internal/handler -run 'TestProjectWorkflow' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add server/migrations server/pkg/db/queries/project_workflow.sql server/pkg/db/generated server/internal/handler/project_workflow_test.go
git commit -m "feat: persist project workflow records"
```

### Task 2: Add project-scoped API contracts and enforce project containment

**Files:**
- Create: `server/internal/handler/project_workflow.go`
- Modify: `server/cmd/server/router.go`
- Modify: `server/internal/handler/project.go`
- Modify: `server/internal/handler/chat.go`
- Modify: `server/internal/handler/issue.go`
- Modify: `packages/core/types/project.ts`
- Create: `packages/core/types/project-workflow.ts`
- Modify: `packages/core/types/index.ts`
- Modify: `packages/core/api/schemas.ts`
- Modify: `packages/core/api/schema.test.ts`
- Modify: `packages/core/api/client.ts`
- Test: `server/internal/handler/chat_project_context_test.go`
- Test: `server/internal/handler/issue_update_project_scope_test.go`

**Interfaces:**

```go
type ProjectWorkflowSettingsRequest struct {
    Enabled        bool            `json:"enabled"`
    ProjectContext string          `json:"project_context"`
    DeliveryMode   string          `json:"delivery_mode"`
    DeliveryConfig json.RawMessage `json:"delivery_config"`
}

type ConfirmExecutionRequest struct {
    SourceChatSessionID string `json:"source_chat_session_id"`
    SourceAgentID       string `json:"source_agent_id"`
    Snapshot            string `json:"snapshot"`
}
```

- [ ] **Step 1: Write failing API tests**

Cover: enabling workflow creates no PRD/developer/reviewer binding; creating a new chat without `project_id` returns 400; an agent-originated issue tied to an execution record receives that record’s project even if the payload names another project; a member may still read legacy project-less chats.

- [ ] **Step 2: Run the focused tests and verify failure**

Run: `cd server && go test ./internal/handler -run 'Test(CreateChatRequiresProject|WorkflowIssueProjectInheritance|EnableProjectWorkflow)' -count=1`

Expected: FAIL because project workflow routes and guards are absent.

- [ ] **Step 3: Implement routes and schemas**

Add only these endpoints:

```text
GET/PATCH /api/projects/{id}/workflow
GET/POST/DELETE /api/projects/{id}/workflow/agents
GET /api/projects/{id}/executions
GET /api/projects/{id}/executions/{executionID}
POST /api/projects/{id}/executions/preview
POST /api/projects/{id}/executions/{executionID}/confirm
```

`preview` validates the current chat belongs to the project and returns a non-persisted confirmation payload. `confirm` persists the immutable snapshot and creates no work until the member confirms it. Reject unknown delivery modes and malformed config with 400.

- [ ] **Step 4: Enforce containment in existing handlers**

In `CreateChatSession`, reject a missing `project_id` for new sessions. In chat moves, reject null/project changes unless the caller is a member moving an ordinary, non-workflow chat. In `CreateIssue` and `UpdateIssue`, read the execution-record linkage first; when present, write its `project_id` and reject a conflicting payload. Do not change ordinary member-created issue behavior outside an execution record.

- [ ] **Step 5: Add shared zod contracts and client methods**

Export `ProjectWorkflowSettings`, `ProjectAgentBinding`, `ProjectExecutionPreview`, `ProjectExecutionRecord`, and `ProjectDeliveryResult`. Parse every new response through `parseWithFallback`; unknown record states render as `blocked`, and unknown delivery modes render as `notify_only` in clients.

- [ ] **Step 6: Run focused backend and core checks**

Run: `cd server && go test ./internal/handler -run 'Test(CreateChatRequiresProject|WorkflowIssueProjectInheritance|EnableProjectWorkflow)' -count=1 && pnpm test -- packages/core/api/schema.test.ts`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add server/cmd/server/router.go server/internal/handler packages/core/types packages/core/api
git commit -m "feat: add project workflow contracts"
```

### Task 3: Build the project settings and project-only chat entry points

**Files:**
- Modify: `packages/views/modals/create-project.tsx`
- Modify: `packages/views/projects/components/project-detail.tsx`
- Create: `packages/views/projects/components/project-workflow-settings.tsx`
- Create: `packages/views/projects/components/project-workflow-settings.test.tsx`
- Modify: `packages/views/chat/components/use-chat-controller.tsx`
- Modify: `packages/views/chat/components/chat-thread-list.tsx`
- Modify: `packages/views/locales/en/projects.json`
- Modify: `packages/views/locales/zh-Hans/projects.json`
- Modify: `packages/views/locales/fr/projects.json`
- Modify: `packages/views/locales/ja/projects.json`
- Modify: `packages/views/locales/ko/projects.json`

**Interfaces:**

```ts
type WorkflowEnablement = {
  enabled: boolean;
  project_context: string;
  delivery_mode: "notify_only" | "pull_request" | "manual_release" | "automated_release";
};
```

- [ ] **Step 1: Write failing shared-view tests**

Test that project creation opens its first chat inside the new project; that the workflow setting is off by default; enabling it shows only “打开项目 Mica” and no automatic team creation; and each delivery mode displays only its relevant fields.

- [ ] **Step 2: Run the focused test and verify failure**

Run: `pnpm test -- packages/views/projects/components/project-workflow-settings.test.tsx`

Expected: FAIL because the settings panel does not exist.

- [ ] **Step 3: Implement the minimal settings UI**

Put “启用 Mica 协作” in the project detail settings area, not agent settings. On enable, ask for an existing runtime only if the selected Mica identity has no runnable runtime. Open/create the Mica **chat session with this project_id**; do not duplicate the global agent row. Keep project context optional and editable. Keep delivery default at “只通知我”.

- [ ] **Step 4: Make chat creation project-first**

The new-chat action in a project supplies its project id automatically. The global new-chat action first asks the user to select/create a project, then creates the session. Do not present an “unclassified” option.

- [ ] **Step 5: Run focused UI tests and typecheck**

Run: `pnpm test -- packages/views/projects/components/project-workflow-settings.test.tsx packages/views/chat && pnpm typecheck`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add packages/views/projects packages/views/chat packages/views/locales
git commit -m "feat: configure Mica collaboration per project"
```

### Task 4: Create immutable execution snapshots and a member confirmation card

**Files:**
- Create: `server/internal/service/project_execution.go`
- Create: `server/internal/service/project_execution_test.go`
- Modify: `server/internal/handler/chat.go`
- Create: `packages/core/project-workflow/queries.ts`
- Create: `packages/core/project-workflow/mutations.ts`
- Create: `packages/views/chat/components/project-execution-confirmation.tsx`
- Create: `packages/views/chat/components/project-execution-confirmation.test.tsx`

**Interfaces:**

```go
type ExecutionSnapshot struct {
    Goal               string   `json:"goal"`
    Scope              string   `json:"scope"`
    AcceptanceCriteria []string `json:"acceptance_criteria"`
    Risks              []string `json:"risks"`
    SourceMessageIDs   []string `json:"source_message_ids"`
    SourceChatSessionID string  `json:"source_chat_session_id"`
}

func (s *ProjectExecutionService) Preview(ctx context.Context, projectID, chatSessionID, mikaAgentID pgtype.UUID) (ExecutionSnapshot, error)
func (s *ProjectExecutionService) Confirm(ctx context.Context, input ConfirmExecutionInput) (db.ProjectExecutionRecord, error)
```

- [ ] **Step 1: Write failing service and component tests**

Cover: `@Mica` from a PRD chat includes only the current chat by default; explicitly selected additional chats are included in the preview; confirmation freezes message ids and rendered snapshot text; editing the PRD later does not mutate the saved snapshot; no development issue exists before confirmation.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `cd server && go test ./internal/service -run TestProjectExecutionSnapshot -count=1 && pnpm test -- packages/views/chat/components/project-execution-confirmation.test.tsx`

Expected: FAIL because preview/confirmation are absent.

- [ ] **Step 3: Implement snapshot and confirmation service**

Resolve source messages server-side from the project-owned sessions; never accept client-supplied message bodies as the persisted snapshot. Persist `snapshot` exactly once in the same transaction that changes `awaiting_confirmation` to `development`. If the source PRD changes later, create a new preview/record rather than mutate an existing record.

- [ ] **Step 4: Implement the chat card**

The card shows goal, scope, acceptance criteria, risks, selected PRD chats, proposed roles, and the project delivery mode. Its only mutating primary action is “确认开始”; “返回修改” sends the user back to the PRD/Mica chat and does not create a task.

- [ ] **Step 5: Run focused tests**

Run: `cd server && go test ./internal/service -run TestProjectExecutionSnapshot -count=1 && pnpm test -- packages/views/chat/components/project-execution-confirmation.test.tsx`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add server/internal/service/project_execution.go server/internal/service/project_execution_test.go server/internal/handler/chat.go packages/core/project-workflow packages/views/chat/components/project-execution-confirmation*
git commit -m "feat: confirm project execution snapshots"
```

### Task 5: Enforce role capabilities and implement bounded development/review orchestration

**Files:**
- Create: `server/internal/service/project_workflow_transition.go`
- Create: `server/internal/service/project_workflow_transition_test.go`
- Modify: `server/internal/handler/issue.go`
- Modify: `server/internal/handler/issue_review_coordination.go`
- Modify: `server/internal/service/task.go`
- Modify: `server/internal/service/builtin_agents/mika/INSTRUCTIONS.md`
- Modify: `server/internal/daemon/prompt.go`
- Test: `server/internal/handler/issue_review_coordination_test.go`
- Test: `server/internal/handler/issue_create_position_test.go`

**Interfaces:**

```go
func (s *ProjectWorkflowService) StartDevelopment(ctx context.Context, recordID, developerAgentID pgtype.UUID, actorID string) (db.AgentTaskQueue, error)
func (s *ProjectWorkflowService) SubmitDevelopmentEvidence(ctx context.Context, recordID, issueID pgtype.UUID, evidence DeliveryEvidence) error
func (s *ProjectWorkflowService) StartReview(ctx context.Context, recordID, reviewerAgentID pgtype.UUID, actorID string) (db.AgentTaskQueue, error)
func (s *ProjectWorkflowService) ApplyReviewDecision(ctx context.Context, recordID pgtype.UUID, decision ReviewDecision) (db.ProjectExecutionRecord, error)
```

- [ ] **Step 1: Write the transition matrix tests first**

Create table-driven tests for all legal transitions:

```text
awaiting_confirmation -> development (member confirmation only)
development -> review (developer evidence with commit + tests + summary)
review -> repair (reviewer needs_changes)
repair -> review (new developer evidence only; round <= 2)
review -> delivering (reviewer pass)
review -> needs_member_decision (reviewer decision/escalation)
repair -> needs_member_decision (third failed automatic round)
```

Also assert: reviewers cannot write repository tasks or create assignments; developers cannot set `delivering`/`completed`; Mica cannot mark review pass; a repeated `in_review` update creates no extra review task.

- [ ] **Step 2: Run the transition tests and verify failure**

Run: `cd server && go test ./internal/service -run TestProjectWorkflowTransitions -count=1`

Expected: FAIL because no transition engine exists.

- [ ] **Step 3: Implement server-side capability checks**

Use `project_agent_binding`, not `agent_workflow_role`, to resolve a role for the execution record’s project. Reject actions before enqueueing work. Inject role-specific, record-id-specific task prompts from the server: developer prompts allow repository work and require evidence; reviewer prompts receive snapshot and evidence but no repository-write/task-dispatch capability; Mica prompts can coordinate only.

- [ ] **Step 4: Replace global review handoff for workflow records**

Keep `enqueueMikaReviewCoordination` for ordinary legacy issues. For an issue linked to an execution record, call `SubmitDevelopmentEvidence` and use the record’s selected reviewer. Deduplicate by `(record_id, state, review_round)` and require `review_round` to advance only after a new delivery event. Never enqueue a task on a status observation alone.

- [ ] **Step 5: Implement automatic recovery without loops**

For a failed/blocked task, inspect its queue error and dependency state. Requeue only once for a transient runtime failure; otherwise mark the record `blocked` or `needs_member_decision`. Store the task ids and transition cause in `project_execution_issue`/record activity so retries are visible.

- [ ] **Step 6: Run backend tests**

Run: `cd server && go test ./internal/service ./internal/handler -run 'TestProjectWorkflowTransitions|TestIssueReviewCoordination' -count=1`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add server/internal/service server/internal/handler/issue.go server/internal/handler/issue_review_coordination.go server/internal/daemon/prompt.go
git commit -m "feat: orchestrate bounded project delivery reviews"
```

### Task 6: Render delivery records and project-specific delivery actions

**Files:**
- Create: `packages/views/projects/components/project-delivery-records.tsx`
- Create: `packages/views/projects/components/project-delivery-records.test.tsx`
- Modify: `packages/views/projects/components/project-detail.tsx`
- Modify: `packages/views/projects/components/index.ts`
- Modify: `packages/core/project-workflow/queries.ts`
- Modify: `server/internal/service/project_workflow_transition.go`
- Modify: `packages/views/locales/{en,zh-Hans,fr,ja,ko}/projects.json`
- Test: `server/internal/service/project_workflow_transition_test.go`

**Interfaces:**

```ts
type ProjectDeliveryResult = {
  mode: "notify_only" | "pull_request" | "manual_release" | "automated_release";
  status: "not_started" | "succeeded" | "failed" | "needs_member_decision";
  evidence: Array<{ label: string; value: string; url?: string }>;
};
```

- [ ] **Step 1: Write failing UI and service tests**

Test that records render source chat, frozen requirements, task links, evidence, review conclusion, and delivery result; that a project with workflow disabled does not render the tab; that `notify_only` creates no external action; and that only configured automated-release checks run.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `pnpm test -- packages/views/projects/components/project-delivery-records.test.tsx && cd server && go test ./internal/service -run TestProjectDeliveryMode -count=1`

Expected: FAIL because the record view and delivery dispatcher are absent.

- [ ] **Step 3: Implement delivery dispatcher**

After a pass, branch exclusively on `delivery_mode`:

```text
notify_only       -> record a member notification and mark completed
pull_request      -> create/update only the configured repository PR
manual_release    -> record release instructions and mark needs_member_decision
automated_release -> run only configured commands/checks, capture output, then complete or fail
```

Do not hard-code Docker, container names, or URLs. The Multica project may configure its Docker command, container names, and two health URLs; another project may configure only a deployment URL or remain `notify_only`.

- [ ] **Step 4: Implement the shared project tab**

Add a compact “交付记录” tab in `ProjectDetail`. Use shared views so web and desktop receive it through their existing `ProjectDetail` pages. Keep the list hidden when workflow is disabled and empty; no separate Spec navigation is added.

- [ ] **Step 5: Run focused UI tests, typecheck, and backend tests**

Run: `pnpm test -- packages/views/projects/components/project-delivery-records.test.tsx && pnpm typecheck && cd server && go test ./internal/service -run TestProjectDeliveryMode -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add packages/views/projects packages/core/project-workflow server/internal/service packages/views/locales
git commit -m "feat: show project delivery records"
```

### Task 7: End-to-end verification and documentation

**Files:**
- Create: `e2e/project-mica-workflow.spec.ts`
- Modify: `apps/docs/content/docs/developers/conventions.mdx` only if a new enduring project/workflow boundary needs documenting
- Modify: `apps/docs/content/docs/developers/conventions.zh.mdx` with equivalent Chinese wording only if the English convention changes

- [ ] **Step 1: Write the e2e scenario**

Use one project and reusable seeded agents. Cover: create project → first project chat → enable Mica → attach existing PRD/developer/reviewer identities → preview from current PRD chat → member confirmation → developer evidence → pass review → `notify_only` completed record. Add a second scenario with two needs-changes decisions and assert the third unresolved review ends at `needs_member_decision` with no fourth task.

- [ ] **Step 2: Run the e2e test and verify failure before implementation is complete**

Run: `pnpm exec playwright test e2e/project-mica-workflow.spec.ts`

Expected: FAIL until Tasks 1–6 are complete.

- [ ] **Step 3: Add only durable documentation**

Document project containment, execution-record immutability, role boundaries, and per-project delivery configuration. Do not add agent prompt text or cancelled Spec details to conventions.

- [ ] **Step 4: Run the complete relevant verification set**

Run:

```bash
make sqlc
make test
pnpm typecheck
pnpm lint
pnpm test
pnpm exec playwright test e2e/project-mica-workflow.spec.ts
git diff --check
```

Expected: all commands pass. If an existing unrelated failure remains, report it with the exact command and do not mark this workflow complete.

- [ ] **Step 5: Commit**

```bash
git add e2e apps/docs/content/docs/developers
git commit -m "test: cover project Mica delivery workflow"
```

## Self-review

- Product workflow is covered by Tasks 2–6: optional project setting, reusable agents with project-bound sessions, confirmation-first execution, PRD snapshots, evidence, review, bounded repair, and delivery.
- The user’s task-leak problem is covered by Task 2’s server enforcement and Task 3’s project-first chat UI.
- The current workflow does not create a second PRD/Spec system; existing Spec code/data are not removed or merged. A separate follow-up design will decide whether and how Spec supports non-workflow multi-agent collaboration.
- Server enforcement, not prompt-only behavior, is covered in Task 5.
- All created indexes are specified as isolated concurrent-index migrations; no foreign keys/cascades are introduced.
- No placeholders remain; each task names its files, contracts, test commands, and expected result.

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-10-21-project-mica-delivery-workflow.md`. Two execution options:

1. **Subagent-Driven (recommended)** — dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — execute tasks in this session using executing-plans, batch execution with checkpoints.
