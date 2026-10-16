# Cross-layer change guide

This is a **just-in-time engineering guide** for changes that cross a product
boundary: client and server, persisted draft and editor, HTTP and realtime, or
server scheduler and local daemon. It is deliberately not a second `AGENTS.md`.

Read the relevant card below before changing a matching flow. Do not read this
document for a local layout change, copy change, isolated refactor, or a test
that does not change a boundary. [`AGENTS.md`](../../AGENTS.md) remains
authoritative; this document explains how participating pieces fit together so
separate changes do not create a valid-looking but inconsistent flow.

## How to use this guide

For a cross-layer change, do the smallest applicable version of this sequence:

1. Find the card whose trigger matches the change. A change can require more
   than one card.
2. Identify the source of truth and hand-off boundary before editing a caller.
   Do not infer either from a convenient UI cache or TypeScript cast.
3. List the producers, consumers, and recovery paths actually affected.
4. Change the contract and its consumers together, then test at the layer that
   owns the behavior.
5. Update this guide only if a durable responsibility, state transition, or
   entry-point map changed.

When work is split across people or agents, the boundary owner states the input,
output, source of truth, compatibility expectation, and verification still
required. A hand-off is not approval to silently redesign another layer.

## Reading map

| Change touches | Read | Also inspect |
| --- | --- | --- |
| File picker, paste-as-file, attachment, draft, Markdown link, multipart upload, preview, or download | [Attachment and upload flow](#attachment-and-upload-flow) | [API response compatibility](#api-response-compatibility) |
| Endpoint, request/response field, Zod schema, auth/workspace header, status code, or API-client fallback | [API response compatibility](#api-response-compatibility) | [Realtime and cache flow](#realtime-and-cache-flow) if clients are refreshed by events |
| WebSocket event, reconnect, Query cache, unread summary, list membership, or optimistic update | [Realtime and cache flow](#realtime-and-cache-flow) | [API response compatibility](#api-response-compatibility) when payload or fetch shape changes |
| Task creation, runtime wake-up, daemon RPC, provider, progress, retry, or cancellation | [Task execution and runtime flow](#task-execution-and-runtime-flow) | [Realtime and cache flow](#realtime-and-cache-flow) when user-visible state changes |
| Database migration, SQL query, workspace filter, or relationship cleanup | `AGENTS.md` database and workspace rules | The card that owns the feature behavior |

These cards are maps, not a claim that every client uses the same
implementation. Mobile owns its API, cache, and realtime plumbing; read
[`apps/mobile/AGENTS.md`](../../apps/mobile/AGENTS.md) before modifying it.

## Shared boundary rules

| Boundary | Owner and rule |
| --- | --- |
| Durable business data | PostgreSQL is authoritative. A client cache, editor document, or daemon memory is a projection and needs a reconciliation path. |
| Server response to UI | The platform API client parses it at the schema boundary. A TypeScript assertion cannot make a newer or malformed response safe. |
| Server state to UI | TanStack Query owns server projections. Zustand is for client-owned UI state and durable drafts, not a duplicate server record. |
| Workspace context | Authorization and SQL scope enforce access; route/platform context and `X-Workspace-ID` select the active workspace but do not replace authorization. |
| Realtime delivery | A WebSocket notification reduces latency; it is not durable delivery and cannot be the sole reconciliation mechanism. |
| Asynchronous work | The creating React mount is not necessarily the owner. Long-lived work names its durable owner and its unmount, abort, reconnect, and retry behavior. |

## Workspace entry flow

### Read this card when

Changing login or OAuth destinations, a workspace route guard, desktop startup
overlays, the landing-page dashboard CTA, or an onboarding redirect.

### Contract

```text
authenticated account + first accessible workspace -> /<workspace>/issues
authenticated account + no accessible workspace    -> /workspaces/new
unauthenticated workspace URL                      -> /login
```

An existing accessible workspace is sufficient to enter its workbench.
`onboarded_at` records optional setup progress; it is not authorization for an
existing workspace and must not redirect a user away from that workbench. This
same decision applies to post-login routing, a direct workspace URL, an old
`/onboarding` bookmark, and desktop startup. Membership and workspace-scoped
data access remain enforced independently by the server and workspace query.

If an account has no workspace, retain the workspace-creation path. Do not
invent a fallback workspace slug or render a dashboard without resolved
workspace access.

### Primary code map

| Layer | Entry points |
| --- | --- |
| Shared destination choice | [`packages/core/paths/resolve.ts`](../../packages/core/paths/resolve.ts) |
| Web workspace guard | [`apps/web/app/[workspaceSlug]/layout.tsx`](../../apps/web/app/[workspaceSlug]/layout.tsx) |
| Web onboarding route | [`apps/web/app/(auth)/onboarding/page.tsx`](../../apps/web/app/(auth)/onboarding/page.tsx) |
| Desktop startup overlay | [`apps/desktop/src/renderer/src/App.tsx`](../../apps/desktop/src/renderer/src/App.tsx) |

## Attachment and upload flow

### Read this card when

Changing file selection, drag-and-drop, paste-as-file, attachment rows,
`attachment_ids`, draft persistence, attachment URLs, preview/download, upload
size/type rules, or the upload endpoint.

### System shape

```text
file / paste
  -> editor placeholder + persisted draft upload record
  -> module-level upload coordinator
  -> POST /api/upload-file (multipart)
  -> attachment row in storage + PostgreSQL
  -> completed attachment + Markdown reference in draft body
  -> create/update/send mutation with reference-filtered attachment IDs
  -> later render / preview / click-time attachment lookup
```

An uploaded byte and an attachment bound to a message are different states. The
upload endpoint creates an attachment record; the create, update, or send
operation binds eligible attachment IDs to an issue, comment, or chat message.
Successful upload alone does not prove that the attachment was submitted.

### Sources of truth and ownership

| Concern | Source of truth | Why it matters |
| --- | --- | --- |
| Attachment metadata and authorization | Server attachment record | URLs and access can change; client metadata is a rendering projection. |
| Completed upload submitted with a draft | References in the draft body, filtered at submit | A visible chip can outlive a reference the user removed. |
| Upload lifecycle after composer unmount | Draft upload placeholder plus module-level coordinator | A component-local promise can lose its result. |
| Editor placeholder replacement | Shared `clientUploadId` | The editor node, draft row, and settle handler need one identity after reopening. |
| Download or preview address | Click-time attachment lookup | Signed URLs are short-lived and must not be stored as durable content. |

### Required state transitions

1. A file pick creates a client upload ID and immediately records an
   `uploading` placeholder in the correct persisted draft when one exists.
2. The coordinator owns the request independently of the initiating component.
   Its settle callback re-reads the current draft and writes only if the
   placeholder still belongs to the current draft generation.
3. On success, the placeholder becomes a completed attachment and its Markdown
   reference reaches the live editor or persisted draft body. A mounted but
   not-yet-initialized editor is a timing state, not permission to drop data.
4. On failure, retain visible recovery state. For paste-as-file, restore the
   swallowed source text when a recoverable destination exists.
5. On logout, abort tracked requests before clearing drafts. An abort must not
   later resurrect a cleared draft through an asynchronous settle callback.
6. On submit, block while relevant uploads are in flight and bind only completed
   attachments still referenced by the submitted body.

An interrupted `uploading` record cannot resume after reload because its bytes
were never persisted. Do not present it as resumable without a durable upload
protocol.

### Primary code map

| Layer | Entry points |
| --- | --- |
| Shared upload coordinator | [`packages/core/drafts/upload-coordinator.ts`](../../packages/core/drafts/upload-coordinator.ts), [`packages/core/drafts/draft-upload.ts`](../../packages/core/drafts/draft-upload.ts) |
| Web/Desktop composer coordination | [`packages/views/editor/use-coordinated-uploads.ts`](../../packages/views/editor/use-coordinated-uploads.ts), [`packages/views/editor/use-editor-upload.ts`](../../packages/views/editor/use-editor-upload.ts), [`packages/views/editor/use-upload-gate.ts`](../../packages/views/editor/use-upload-gate.ts) |
| API client and schema | [`packages/core/api/client.ts`](../../packages/core/api/client.ts), [`packages/core/api/schemas.ts`](../../packages/core/api/schemas.ts), [`packages/core/hooks/use-file-upload.ts`](../../packages/core/hooks/use-file-upload.ts) |
| Server persistence and authorization | [`server/pkg/db/queries/attachment.sql`](../../server/pkg/db/queries/attachment.sql), [`server/internal/handler/attachment_capability.go`](../../server/internal/handler/attachment_capability.go), `server/internal/storage/` |
| Mobile-owned equivalent | [`apps/mobile/components/composer/message-composer.tsx`](../../apps/mobile/components/composer/message-composer.tsx), [`apps/mobile/components/editor/use-file-attach.ts`](../../apps/mobile/components/editor/use-file-attach.ts), [`apps/mobile/data/api.ts`](../../apps/mobile/data/api.ts) |

### Change checklist

- Does the new context (`issueId`, `commentId`, `chatSessionId`, or equivalent)
  reach the multipart request and server authorization path?
- Can the composer close, reopen, clear, submit, or log out between request
  start and completion without silently attaching, duplicating, or losing data?
- Does removing a Markdown/reference exclude the attachment from submit?
- Do preview, inline open, and forced download use an appropriate click-time
  URL rather than persisting a signed URL into content?
- Are coordinator, editor, API-client, and relevant mutation tests updated at
  their owning layers?

## API response compatibility

### Read this card when

Adding or changing an HTTP endpoint, request/response field, multipart field,
authentication behavior, workspace selection, enum, error envelope, or client
fallback.

### Contract shape

```text
client caller
  -> platform API client
  -> schema + parseWithFallback (for UI-consumed JSON)
  -> HTTP handler and service
  -> sqlc query / storage / transaction
  -> response schema and client projection
```

The API boundary is a compatibility boundary, particularly because installed
desktop clients can connect to newer backends. A server change is incomplete
until every supported consumer safely parses the portion it consumes.

### Stable rules

- Update the Web/Desktop response schema in
  [`packages/core/api/schemas.ts`](../../packages/core/api/schemas.ts) and use
  [`parseWithFallback`](../../packages/core/api/schema.ts) for UI-consumed JSON.
  Do not replace validation with `as T`.
- Give optional fields and unknown enums a safe rendering behavior. Preserve
  valid older responses unless deliberately making a breaking API change.
- Mobile has its own boundary in [`apps/mobile/data/api.ts`](../../apps/mobile/data/api.ts)
  and [`apps/mobile/data/schemas.ts`](../../apps/mobile/data/schemas.ts). It
  implements equivalent semantics, not Web/Desktop cache machinery.
- Forward cancellation signals for queries and uploads when a caller owns
  cancellation. Do not discard an established timeout or abort path.
- Request acceptance is not authoritative projection. A successful write can
  still require invalidation or a realtime update to show final state.
- A workspace header selects context; handlers and queries still require
  membership and `workspace_id` constraints.

### Change checklist

1. Identify supported callers: Web, Desktop, Mobile, CLI, daemon, and an
   external integration if it consumes this endpoint.
2. Specify request fields, response fields, nullability, enum fallback, error
   status, and workspace/auth context before editing callers.
3. Update handler, service/query behavior, and each consuming schema together.
4. Add a malformed-response regression test for a changed UI-facing shape and
   a serialization test for a field whose presence or name affects behavior.
5. If the write changes another projection, follow the realtime/cache card
   instead of inventing a component-local refresh.

### Primary code map

| Layer | Entry points |
| --- | --- |
| Web/Desktop boundary | [`packages/core/api/client.ts`](../../packages/core/api/client.ts), [`packages/core/api/schemas.ts`](../../packages/core/api/schemas.ts), [`packages/core/api/schema.ts`](../../packages/core/api/schema.ts) |
| HTTP behavior | `server/internal/handler/`, `server/internal/service/` |
| Data boundary | [`server/pkg/db/queries/`](../../server/pkg/db/queries/), generated `server/pkg/db/generated/` |
| Mobile boundary | [`apps/mobile/data/api.ts`](../../apps/mobile/data/api.ts), [`apps/mobile/data/schemas.ts`](../../apps/mobile/data/schemas.ts), [`apps/mobile/lib/parse-response.ts`](../../apps/mobile/lib/parse-response.ts) |

## Realtime and cache flow

### Read this card when

Adding or changing a WebSocket event, changing list membership, invalidating or
patching Query data, changing reconnect behavior, or adding an optimistic UI
transition.

### System shape

```text
HTTP write / daemon update / integration event
  -> database transaction
  -> server broadcast
  -> user-client WebSocket
  -> feature-owned cache patch or invalidation
  -> query refetch/reconciliation, especially after reconnect
```

Realtime is a latency optimization over the database and query path. An event
can be missed, delayed, duplicated, or insufficient to determine list
membership; it does not make a local cache authoritative.

### Ownership rules

| Concern | Owner |
| --- | --- |
| Event publication and protocol behavior | Server realtime or daemon path that committed the state change |
| Web/Desktop cache projection | The feature's Query hook/realtime synchronizer in `packages/core/realtime/` or the owning feature |
| Mobile cache projection | Feature-owned updater under `apps/mobile/data/realtime/` |
| Client-only UI state | Relevant Zustand or mobile-local store, never a duplicate server entity |
| Reconnect repair | Each feature owning a projection; not a global refetch sweep |

Patch only when the event contains enough information for a deterministic cache
value and membership. Invalidate or refetch when it cannot prove the complete
result, such as a filtered list that may gain or lose an item. Account-level
and workspace-scoped projections are separate; refresh both if both changed.

### Optimistic transitions

Use an optimistic patch only when the result is predictable, failure is rare,
rollback is trivial, and the user stays on the current screen. Capture the
rollback snapshot before patching, accept server/realtime authority when it
arrives, and invalidate uncertain projections on settle. Do not generalize a
documented narrow exception into create, delete, leave, or confirmation flows.

### Protocol change checklist

1. Define event producer, payload, consumers, and reconnect repair before
   adding a subscription.
2. Update both `WSEventType` and `WSEventPayloadMap` in
   [`packages/core/types/events.ts`](../../packages/core/types/events.ts) for a
   new typed event. Narrow unknown payloads rather than adding unchecked casts.
3. Update every client with a product consumer. A client without a product use
   should not subscribe merely for symmetry.
4. Choose deterministic patch, invalidation, or reconnect refresh for each
   cache. Check list membership, unread summaries, and cross-cache projections.
5. Test a local update, an update from another client/surface, and a reconnect
   case where applicable.

### Primary code map

| Layer | Entry points |
| --- | --- |
| User-client server transport | [`server/internal/realtime/`](../../server/internal/realtime/) |
| Daemon transport | [`server/internal/daemonws/`](../../server/internal/daemonws/) |
| Shared Web/Desktop sync | [`packages/core/realtime/`](../../packages/core/realtime/), [`packages/core/types/events.ts`](../../packages/core/types/events.ts) |
| Mobile sync | [`apps/mobile/data/realtime/`](../../apps/mobile/data/realtime/), [`apps/mobile/lib/use-ws-subscriptions.ts`](../../apps/mobile/lib/use-ws-subscriptions.ts) |
| Query ownership | `packages/core/**/queries/` and `apps/mobile/data/queries/` |

## Task execution and runtime flow

### Read this card when

Changing an issue, mention, or automation trigger; task queueing; runtime
availability; daemon claim/RPC; provider adapter; streamed task update; retry;
or cancellation.

### System shape

```text
product action or automation
  -> TaskService creates queued task
  -> runtime wake-up / daemon notification
  -> daemon claims task and receives task-bound credentials
  -> daemon prepares local work area and starts provider adapter
  -> daemon uploads progress, messages, usage, and final state
  -> server persists state and broadcasts user-visible changes
```

The daemon owns local directories, sessions, and provider invocation. The
server owns queueing, task identity, authorization, durable status, and the
user-visible business result. A provider adapter normalizes provider-specific
startup, streaming, cancellation, and usage; it is not an alternate source of
truth for task state.

### Required failure semantics

- A daemon disconnect must not leave a queued task permanently stranded. Keep
  the established polling/recovery path when changing notifications.
- Cancellation updates server task state, propagates to daemon/provider, and
  reconciles final task/issue projection. A local process signal alone is not
  user-visible cancellation success.
- Temporary credentials remain task- and agent-bound. Do not broaden their
  scope to simplify a daemon or provider call.
- Progress messages are asynchronous; persist and broadcast through the owned
  path rather than making a provider-specific UI assumption the final state.
- Retry semantics belong to the task/service model. A provider retry must not
  create an untracked parallel task or duplicate completion.

### Change checklist

1. State which layer owns the transition and which layer observes it.
2. Trace success, provider failure, daemon disconnect, cancellation, and retry;
   name the durable final status for each.
3. Preserve workspace, task, agent, and runtime identity at each boundary.
   Resolve human-readable route parameters before writes as `AGENTS.md` requires.
4. Update protocol/API schemas, daemon/client consumers, and realtime/cache
   projections together when a visible task field or event changes.
5. Run focused server and client tests. Real-agent smoke tests remain opt-in.

### Primary code map

| Layer | Entry points |
| --- | --- |
| Task lifecycle and HTTP behavior | `server/internal/service/`, `server/internal/handler/` |
| Daemon RPC and lifecycle | [`server/internal/daemonws/`](../../server/internal/daemonws/), `server/cmd/` |
| Provider adapters | [`server/pkg/agent/`](../../server/pkg/agent/) |
| Database task model | [`server/pkg/db/queries/`](../../server/pkg/db/queries/) |
| Shared client task projection | `packages/core/**/queries/`, [`packages/core/realtime/`](../../packages/core/realtime/) |
| Product architecture | [`apps/docs/content/docs/developers/architecture.mdx`](../../apps/docs/content/docs/developers/architecture.mdx) |

## Multi-agent coordination without duplicate rules

Use this only when concurrent work touches the same boundary. It avoids both
uncoordinated edits and mandatory ceremony for ordinary work.

| Situation | Required coordination |
| --- | --- |
| Two changes use the same API field or event | Agree on schema, nullability/unknown-enum handling, producer, and consumers before either depends on the other. |
| One change alters a durable state transition | Name source of truth, allowed predecessor states, final state, recovery path, and owning regression test in the hand-off. |
| One change only rearranges a local component | No cross-layer hand-off or guide update is needed. |
| A change exposes an enduring cross-layer contract | Add or update a concise card here, linked to its primary entry points. Do not add an incident narrative. |
| A change conflicts with this guide or `AGENTS.md` | `AGENTS.md` wins. Resolve the discrepancy in the same change. |

Good hand-off example:

> `attachment_ids` remains derived from references in the persisted body at
> submit time. This change adds `chat_session_id` to multipart upload context;
> caller, API schema, handler authorization, and upload tests move together.
> Preview URLs remain click-time only.

It says what another agent must preserve without prescribing its local
implementation.

## Keeping the guide useful

- Record durable responsibilities, state transitions, compatibility guarantees,
  and recovery semantics.
- Link to owning implementation and tests instead of duplicating volatile
  function bodies, line numbers, dependency versions, or UI markup.
- Keep examples representative rather than exhaustive. Add a card only when a
  flow has a distinct cross-layer contract worth preserving.
- Prefer a regression test for a concrete failure over a new universal rule.
  Promote the lesson here only when multiple callers or future agents need it.
- Review this guide when a referenced workflow changes, but do not make
  unrelated contributors read it merely because it exists.
