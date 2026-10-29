# Single-user settings cleanup

## Goal

Keep Settings aligned with the simplified navigation: a single user chats with
and manages agents, Skills, and runtimes. Do not surface settings that only
control the hidden task-management or multi-member collaboration UI.

## Scope

Remove the following Settings entries from the shared web and desktop settings
page:

- Workspace task configuration: issue statuses, wakeups, labels, properties,
  and quick actions.
- Workspace membership management.
- Preferences that require task pages or task creation: sticky comment bar,
  reply behavior for a running agent, task opening mode, and task creation
  field visibility.

The removal includes the side navigation entry, rendered tab content, and the
settings search index. Direct URLs to removed tabs fall back to the first
available Settings entry through the existing settings-page resolution logic.

## Retained settings

Keep account profile, appearance, language, timezone, notifications, keyboard
shortcuts, tokens, workspace general settings, code hosting, channels, MCP,
plugins, billing, and platform-specific device settings. These settings either
remain useful in a single-user installation or configure the retained agent
and runtime capabilities.

Keep the floating-chat preference because Chat remains a primary navigation
surface.

## Implementation approach

Make the feature boundary in the Settings composition layer:

1. Remove the obsolete workspace entries from `SettingsPage`, so they cannot
   appear in navigation or search.
2. Simplify `PreferencesTab` to retain only region, appearance, and floating
   chat preferences. Remove imports that exclusively support the hidden task
   settings.
3. Update the Settings page and preference component tests to assert that
   task-management and member controls are absent while retained controls
   remain available.

The underlying APIs, routes, and storage are intentionally left intact. This
is a UI surface cleanup consistent with the existing navigation change, not a
backend feature deletion.

## Error handling and compatibility

No API contract changes are required. The existing Settings page already
selects the first available entry whenever a requested tab is not in the
current entry list, so obsolete bookmarked tabs render a valid Settings page
without adding a compatibility mapping.

## Verification

Add focused component tests first, verify that they fail against the current
UI, then implement the minimum changes to make them pass. Run the focused
Settings tests, followed by relevant type checking and linting if time and the
local environment permit.
