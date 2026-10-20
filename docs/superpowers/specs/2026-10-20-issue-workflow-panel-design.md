# Task Workflow Panel Design

## Goal

Make the agent workflow visible in each task detail instead of hiding a one-line status hint in the desktop sidebar.

## Scope

The task detail page gets a compact, always-visible workflow panel above the task body. It uses the existing issue status and task-run query; it does not add workflow tables, status values, or action buttons.

The panel shows:

- the current workflow stage and responsible role;
- the latest relevant agent run, including agent name and run status;
- a short next-step statement that distinguishes queued, running, review, repair, blocked, and done work.

On narrow screens it remains in the task content column, so it is available on mobile and is not dependent on the desktop right rail.

## Data Flow

`IssueDetail` already has the issue and passes its identifier to `ExecutionLogSection`. A new `IssueWorkflowPanel` receives the issue status and uses `issueTasksOptions(issueId)`, the same source as the execution log. It derives display-only workflow information from the latest agent task. Existing realtime invalidation of issue task queries refreshes the panel when a task is queued, starts, completes, or fails.

## Boundaries

The panel reports real server data. It does not claim that a reviewer, executor, Mika, or deployment has run when no matching task exists. It is not a workflow control surface; scheduling and review-repair remain backend responsibilities.

## Verification

Component tests cover the status-to-stage mapping, active and completed task presentation, and narrow-screen-safe placement through the `IssueDetail` render. Existing execution-log tests continue to own run-history behavior.
