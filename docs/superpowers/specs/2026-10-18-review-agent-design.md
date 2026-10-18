# Review agent design

DAWE-20 adds an opt-in review role to an ordinary user agent; it does not change the existing system-agent identity or grant additional permissions. A review agent has its own recipient, workspace or selected-project scope, instant notifications, and a daily summary time and IANA time zone. Configuration changes require authorization and validate that all referenced resources belong to the workspace and are accessible to the agent and recipient.

Committed source events for review, blocking, completion of review, and execution failure create durable, workspace-scoped work items with stable event identities. The dispatcher re-reads current issue, run, PR and check state, coalesces pending work without losing distinct issues, excludes the reviewer and its own delivery events, limits work, and retries recoverable failures. The existing scheduler and agent task queue provide dispatch and lease recovery; runtime connectivity is not proof of available quota. Failure and unknown states remain visible.

Scheduled summaries enumerate unresolved work and coordinate with instant delivery to avoid simultaneous duplicate dispatch. Reports reference the original issue and exact PR head. User decisions are recorded on that issue, tied to the PR head, and tracked until resolved or superseded; they do not grant permission to merge or deploy. After approval the user can explicitly decide deployment.

Existing agents and clients continue to work with default ordinary role and no review configuration. Web and desktop share the configuration UI. An incomplete or disabled review agent must not schedule work or imply monitoring is active. No migrations or deployments are applied to production without explicit authorization.

Verification includes legacy responses, permissions and workspace isolation, creation and edits, duplicate and concurrent events, rollback, self-trigger exclusion, restart and retry, schedules and time zones, missing evidence, version-specific decisions, and authorization boundaries. Delivery is a reviewable PR.
