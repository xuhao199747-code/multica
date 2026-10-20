# Force issue surfaces to list view

## Goal

Every task-oriented issue surface renders the list view only. Users cannot
switch to board, table, swimlane, or Gantt views.

## Scope

- Apply to the shared issue view state used by workspace, project, My Issues,
  member, and agent task surfaces in web and desktop clients.
- Start every surface in list view.
- Normalize persisted legacy view selections to list view.
- Remove the issue-header view-mode selector so no alternate view is exposed.
- Keep the project overview page unchanged: its compact/table and card display
  options are not task-board view modes.

## Design

The shared issue view store becomes the invariant boundary: its initial
`viewMode` is `list`, and `setViewMode` always stores `list` regardless of an
incoming mode. This makes stale persisted state and calls from any existing
entry point resolve consistently.

Issue surface controllers are configured with list as their only allowed mode.
The header hides its view selector on those surfaces. Existing non-list
renderers and their configuration state remain in place but are unreachable;
this avoids unrelated removal of drag-and-drop and saved-view behavior.

## Verification

- Add store tests for the list default and normalization of alternate mode
  requests.
- Add a surface/controller regression test proving a persisted alternate mode
  becomes list.
- Run the affected Vitest suites, then the relevant type check and lint checks.
