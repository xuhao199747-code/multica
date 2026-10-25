# Mica Collaboration Loop P0 Design

**Status:** Draft for review
**Date:** 2026-10-25
**Scope:** Connect project Mica conversations to the existing Project Workflow without replacing the existing project, issue, inbox, or agent-run systems.

## Summary

Project Mica is intended to coordinate a member-confirmed request through PRD, development, review, repair, and completion. The existing system already has project-scoped agent bindings, execution records, issue creation, agent task queueing, delivery receipts, review receipts, inbox notifications, and realtime updates. The missing connection is a reliable entry from a project Mica conversation into that existing workflow.

P0 adds a small, explicit bridge: when a member confirms a structured request in a project Mica conversation, the system atomically creates the existing execution and its first development issue. It reuses the existing state machine after that point. It does not redesign all project pages, replace the issue system, or add a second task or notification system.

## Problem

Today, opening a project Mica conversation only opens a chat. Starting the existing workflow requires separate confirmation and dispatch commands. That makes a normal conversation easy to leave without an execution record or a real development task.

The real `multica` project has workflow settings enabled but no execution records. An isolated validation project has completed an execution, so the later API path has evidence of working in isolation. That does not prove that a real project Mica conversation starts the loop.

The current UI also makes the workflow look heavier than it is because configuration, raw evidence, and current progress are shown together. Users need one clear answer: what needs their attention now, what is running, and where to continue the conversation.

## Goals

- Let a member confirm a Mica-produced PRD and start the existing workflow in one action.
- Create an execution, development issue, execution-to-issue link, and agent run as one durable operation.
- Keep existing projects, issues, agent queues, inbox items, activity records, role bindings, and delivery evidence as their current product objects.
- Leave Project, Tasks, My Tasks, and Inbox on their existing data boundaries while making the new development task visible through their current behavior.
- Prove the loop against the real `multica` project with a narrow, safe acceptance request before changing later stages.

## Non-Goals

- Replacing the existing Issue or agent-run systems.
- Reworking every workflow state, role, or screen.
- Changing automated release, Docker, GitHub merge, or deployment behavior.
- Making Autopilot bypass member confirmation.
- Creating a new member-decision issue type or a second personal task database.
- Backfilling or rewriting historical executions and issues.

## Product Principles

1. **One fact, one owner.** Each business fact has one durable source of truth.
2. **Mica owns conversation, not duplicate state.** Mica explains, collects confirmation, and asks for decisions; it does not maintain an independent workflow ledger.
3. **Execution owns collaboration stage.** A Project Execution is the only source for `development`, `review`, `repair`, `needs_member_decision`, `delivering`, and `completed`.
4. **Issues own concrete work.** Development, review, and repair work remain normal project tasks with normal assignees and runs.
5. **Inbox owns notification delivery.** Reading or archiving a notification cannot complete or clear a pending workflow decision.
6. **Project pages summarize; they do not orchestrate.** The project Mica chat is the interaction entry point; the project page gives status, links, and configuration.

## Existing Capability Reused

| Existing capability | P0 responsibility | P0 change |
| --- | --- | --- |
| Project workflow settings and role bindings | Enable the workflow and choose the project-bound Mica, PRD, developer, and reviewer | None |
| Project Mica chat | Gather request context and present confirmation | Add a structured confirmation action |
| Project execution record | Persist the confirmed snapshot and collaboration stage | Reuse as the workflow source of truth |
| Issue service | Create the development task in the existing project task system | Reuse through the new atomic bridge |
| Agent task queue | Run the project-bound development agent | Reuse |
| Delivery and review receipts | Advance later execution stages | No behavior change in P0 |
| Project task views | Show development, review, and repair tasks | Add execution context only where useful |
| My Tasks | Show the existing Issue relations: assigned, created, and involved | No P0 change |
| Inbox | Deliver existing notifications | No P0 change |
| Activity log and realtime events | Record and refresh durable changes already emitted by issue and task creation | Reuse; add execution-specific events only if the current project query cannot refresh from existing events |

## Source of Truth

| Fact | Source of truth | Displayed in |
| --- | --- | --- |
| Confirmed scope and acceptance criteria | Frozen execution snapshot | Mica chat, project details, workflow tasks |
| Current collaboration stage | `project_execution_record.state` | Project summary and Mica context |
| Concrete work and assignee | Linked Issue plus agent run | Project tasks, global tasks, agent views |
| Delivery and review evidence | Execution receipt fields, with commit or PR references | Execution details and reviewer context |
| New/unread notification | Inbox item | Inbox and badge |
| Audit history | Activity log | Project and task history |

## Target User Flow

### 1. Start from project Mica

```text
Member opens project Mica
  -> Mica clarifies the request
  -> Mica prepares goal, scope, acceptance criteria, and risks
  -> Member selects "Confirm and start"
  -> System creates the execution and development task
  -> Development agent run is queued
  -> Mica confirms that development has started
```

The confirmation action is available only for a chat belonging to the current project and a Mica or PRD agent bound to that project.

### 2. Reuse the existing workflow after dispatch

```text
Development receipt
  -> existing execution changes to review
  -> existing review coordination path runs

Review result
  -> passed: existing delivery or completion path runs
  -> needs_changes: existing repair path runs
  -> needs_member_decision: Mica asks the confirming member in the project chat
```

P0 does not assume that later stages must be rewritten. The first real-project acceptance run decides whether a later handoff is broken before any broader change is proposed.

## Architecture

### Atomic confirmation-to-dispatch bridge

P0 introduces one application-level operation, named here as `StartProjectWorkflowExecutionFromChat`.

It must:

1. Validate the project, enabled workflow, source chat, and project-bound Mica or PRD agent.
2. Validate a structured snapshot containing `goal`, `scope`, `acceptance_criteria`, and `risks`.
3. Create one `project_execution_record` with the frozen snapshot.
4. Create one development Issue in the same project and assign it to the bound developer.
5. Create the `project_execution_issue` link with role `development`.
6. Persist the existing agent run for that Issue.
7. Commit all durable records in one transaction.
8. Publish activity and realtime events only after commit.

The operation must reuse lower-level workflow, issue, and task services. It must not call HTTP handlers from another handler or duplicate Issue creation and queueing rules.

`IssueService.Create` currently owns its own transaction, so P0 cannot obtain this atomicity by calling that public method from a new handler. The implementation must first expose a transaction-aware internal creation-and-enqueue path, then call it from the workflow transaction. If that extraction is not safe, P0 must stop rather than ship a compensating-cleanup path that can leave partial workflow records.

If any durable step fails, the transaction rolls back. The system must not leave an execution without its first development Issue or an orphaned development Issue without its execution link.

### State boundary

```text
Execution.state        = collaboration lifecycle
Issue.status           = concrete task progress
Agent task queue state = agent-run lifecycle
Inbox read state       = notification attention only
```

These are separate state machines. P0 must not infer one from another or write them as mirrored copies.

When later validation finds a completed execution with still-active linked workflow issues, that is a targeted convergence defect to fix in its own follow-up. P0 records and tests for it but does not preemptively redesign all terminal behavior.

## Interaction Design

### Project page

The existing Mica workflow section remains the P0 project surface.

- Its existing primary action continues to open the project Mica chat.
- Its existing execution list continues to show created execution records and their evidence.
- Role selectors, delivery settings, and raw evidence are not redesigned in P0.
- The existing "automated release" setting is not expanded or reinterpreted in P0.

P0 may add only the minimum cache invalidation or realtime event needed for an execution started from Mica to appear without a manual refresh. It does not add a new project dashboard, a new task board, or a second workflow summary.

### Project Mica chat

Mica presents a compact confirmation card after it has enough information:

```text
Ready to start collaboration

Goal: <goal>
Scope: <scope>
Acceptance criteria: <criteria>
Risks: <risks>

[Confirm and start] [Ask Mica to revise]
```

After confirmation, the chat shows a system progress summary and links to the created development task. The chat remains the place to ask questions and resolve member decisions; it is not a replacement task board.

### Task pages

Workflow-created development, review, and repair work stays in the existing task pages. P0 relies on their existing project, assignee, status, and run displays. A later task-page improvement may add:

- Its project.
- Its workflow role: development, review, or repair.
- A link to the execution snapshot and Mica conversation.
- Required evidence or upstream review findings.

Existing `Coordinate review` and `Coordinate delivery` tasks remain unchanged in P0. P0 creates no additional coordination-task type and does not hide or rewrite existing coordination records before a real-project acceptance run proves that later handoffs require a targeted change.

### My Tasks and Inbox

P0 does not change either surface.

- My Tasks remains an Issue-only view with its existing assigned, created, and involved scopes.
- Agent-owned development, review, and repair Issues remain visible through the existing project task list and agent views, not a member's My Tasks by default.
- Inbox continues to represent existing notifications. P0 does not create a new pending-confirmation or execution-decision notification type.

If a later real-project run proves that a member decision is too easy to miss in Mica chat, that work must be designed separately with a durable decision record. It must not be derived from Inbox read state or added as a duplicate Issue.

## Functional Requirements

### FR-1: Confirm and start

- A project Mica chat can request structured confirmation from a member.
- A confirmed snapshot starts an execution and its first development Issue atomically.
- Repeated clicks or retries cannot create duplicate active executions for the same confirmation action.
- A failed start leaves no partial execution, issue, link, or queued run.

### FR-2: Existing workflow preservation

- Existing project role bindings remain the source for the developer and reviewer.
- Existing delivery and review receipt requirements remain in force.
- Existing non-workflow tasks and workspace-level Mica review coordination keep their current behavior.
- Workflow-linked Issues cannot be moved to another project.

### FR-3: Project and task visibility

- A started execution appears on the project page without a manual refresh.
- The created development Issue appears in the existing project task list and agent run views.
- The task links back to its execution and source Mica chat.

### FR-4: Acceptance evidence

- The real `multica` project can complete a safe, narrow workflow acceptance execution.
- The acceptance record includes the source project Mica chat, execution ID, development Issue, agent run, delivery receipt, review receipt, final execution state, and visible project/task updates.
- A status change or chat comment alone cannot be treated as delivery or review evidence.

## Success Measures

P0 is successful when:

- A member can start a real project execution from Mica without manually running separate confirm and dispatch commands.
- One member confirmation yields exactly one active execution, one development Issue, one execution-to-issue link, and one queued agent run.
- The project page, task list, and Mica chat all identify the same active execution.
- Existing My Tasks and Inbox behavior remains unchanged by the new start operation.
- The real-project acceptance run produces end-to-end evidence.

🔶 Assumption: There is no current product metric for workflow-start completion rate. P0 should add structured logs or audit assertions first; a product metric can be selected after real-project usage produces a baseline.

## Risks and Guardrails

| Risk | Guardrail |
| --- | --- |
| Duplicate starts from chat retries | Use a confirmation-specific idempotency key and one transaction |
| Mica starts work without member agreement | Only the member confirmation action may start an execution |
| New bridge bypasses existing validation | Reuse workflow role, project, Issue, and task queue services |
| P0 expands into My Tasks or Inbox redesign | Keep both surfaces unchanged and prove the entry bridge first |
| Workflow creates noisy notifications | P0 creates no workflow notification type |
| Historical state inconsistencies are hidden | Preserve history and report discrepancies; do not auto-close old blocked work |
| Scope expands into delivery automation | Keep Docker, GitHub merge, and release automation out of P0 |

## Validation Plan

1. Create a narrow, safe acceptance request in the real `multica` project through its project Mica chat.
2. Confirm it using the new Mica confirmation action.
3. Verify one execution, one development Issue, one link, and one queued agent run in durable records.
4. Verify the project page and task list update through realtime events.
5. Complete the existing delivery and review path with real evidence.
6. Compare execution state, Issue status, and agent-run state at each transition.
7. Verify existing My Tasks and Inbox behavior is unchanged by the new start operation.

## Follow-Up Boundary

Only after the P0 acceptance run, assess evidence for these separate follow-ups:

- Repairing a proven later-stage handoff failure.
- Converging linked Issue status when an execution reaches a terminal state.
- Replacing user-visible coordination tasks with chat-first handoffs.
- Durable member decisions, My Tasks integration, Inbox notifications, and configurable decision ownership.
- GitHub, CI, Docker, and deployment integration.

None of these follow-ups are authorized by P0 alone.

## Open Questions

- 🔵 Open Question: Does the current Mica chat tool surface support a structured member-confirmation action, or does P0 need a small project-chat action API and UI card?
- 🔵 Open Question: Which existing durable event and activity-log shape best represents execution creation without adding a parallel audit mechanism?
- 🔵 Open Question: Does the current project execution list receive an existing realtime invalidation when a Mica confirmation creates a record, or is one narrow execution-created event needed?
