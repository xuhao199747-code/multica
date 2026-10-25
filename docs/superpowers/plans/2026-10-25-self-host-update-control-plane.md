# Self-Host Update Control Plane Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the Web sidebar create and track a guarded self-host Docker update, while keeping the host worker durable across temporary Docker and backend outages.

**Architecture:** `packages/core` owns the typed self-update HTTP contract and zod parsing. `packages/views/layout/update-status.tsx` renders the self-host state when the server supports it and retains the current GitHub-only behavior for installations returning 404. The host worker treats only control-plane transport and 5xx errors as retryable; malformed configuration and unsafe local state remain terminal errors.

**Tech Stack:** TypeScript, React, Vitest, Testing Library, zod, Go, Go test, macOS LaunchAgent, Docker Compose.

## Global Constraints

- Preserve the existing self-update safety model: browser requests never carry host commands, Docker arguments, repository paths, branch names, or credentials.
- A check never deploys. The destructive Docker update requires an explicit confirmation action.
- API responses consumed by UI must pass through zod and `parseWithFallback`.
- Keep Web/Desktop shared UI inside `packages/views`; do not import platform router APIs.
- Support English, French, Japanese, Korean, and Simplified Chinese layout copy.
- Do not delete Docker volumes, PostgreSQL data, the Tailscale container, or local-sync files.
- Only claim a successful update after the database task, worker journal/logs, backup, deployed image identities, `/readyz`, local frontend, and Funnel URL have all been checked.

---

### Task 1: Add the typed self-update client contract

**Files:**
- Create: `packages/core/types/self-update.ts`
- Modify: `packages/core/types/index.ts`
- Modify: `packages/core/api/schemas.ts`
- Modify: `packages/core/api/client.ts`
- Modify: `packages/core/api/client.test.ts`

**Interfaces:**
- Produces `SelfUpdateStatus`, `SelfUpdateJob`, `SelfUpdateCapability`, and `SelfUpdateRelease` from `@multica/core/types`.
- Produces `api.getSelfUpdateStatus(): Promise<SelfUpdateStatus | null>`, `api.createSelfUpdateJob(targetTag: string, idempotencyKey: string): Promise<SelfUpdateJob | null>`, and `api.getSelfUpdateJob(id: string): Promise<SelfUpdateJob | null>`.
- `null` means a syntactically invalid success body; `ApiError` continues to represent non-2xx responses.

- [ ] **Step 1: Write the failing API client tests**

Add tests in `packages/core/api/client.test.ts` that create an `ApiClient`, mock a 200 JSON body, and assert:

```ts
expect(await client.getSelfUpdateStatus()).toEqual({
  capability: "ready",
  can_update: true,
  latest_release: {
    tag: "v0.7.0",
    sha: "dbdbb299164347586e2650bb67684a490726a798",
    url: "https://github.com/multica-ai/multica/releases/tag/v0.7.0",
  },
  deployed_release: "v0.6.0",
  deployed_custom_sha: "1d43ac43d8a0fb9028a52830e729891d430c70eb",
  active_job: null,
  update_available: true,
  check_failed: false,
});
```

Add a creation test that asserts the request is `POST /api/self-update/jobs`, the body is `{"target_tag":"v0.7.0"}`, and the `Idempotency-Key` header equals the caller-provided key. Add a malformed-body test that expects `null`, not an unsafe cast.

- [ ] **Step 2: Run the client test to verify it fails**

Run:

```bash
pnpm --filter @multica/core test api/client.test.ts
```

Expected: FAIL because the `ApiClient` methods and self-update schema do not yet exist.

- [ ] **Step 3: Define the wire types and zod schemas**

Create `packages/core/types/self-update.ts` with:

```ts
export type SelfUpdateCapability =
  | "unsupported"
  | "needs_setup"
  | "worker_offline"
  | "ready";

export interface SelfUpdateRelease {
  tag: string;
  sha: string;
  url: string;
}

export interface SelfUpdateJob {
  id: string;
  phase: string;
  step: string;
  target_tag: string;
  target_sha: string;
  source_sha: string;
  repair_count: number;
  retry_count: number;
  created_at: string;
  updated_at: string;
  finished_at: string | null;
}

export interface SelfUpdateStatus {
  capability: SelfUpdateCapability;
  can_update: boolean;
  latest_release: SelfUpdateRelease | null;
  deployed_release: string;
  deployed_custom_sha: string;
  active_job: SelfUpdateJob | null;
  update_available: boolean;
  check_failed: boolean;
}
```

Export those types from `packages/core/types/index.ts`. Add zod schemas that accept only the four known capabilities, default unknown optional fields to safe values, and return `null` for malformed required payloads.

- [ ] **Step 4: Add the three `ApiClient` methods**

Import the types and schemas into `packages/core/api/client.ts`. Follow the existing `createWorkspaceSubscriptionPortal` pattern: use `fetchRaw`, decode once, and run `parseWithFallback`.

```ts
async getSelfUpdateStatus(): Promise<SelfUpdateStatus | null> {
  const raw = await this.fetch("/api/self-update/status");
  return parseWithFallback(raw, SelfUpdateStatusSchema, null, {
    endpoint: "GET /api/self-update/status",
  });
}

async createSelfUpdateJob(targetTag: string, idempotencyKey: string): Promise<SelfUpdateJob | null> {
  const response = await this.fetchRaw("/api/self-update/jobs", {
    method: "POST",
    body: JSON.stringify({ target_tag: targetTag }),
    extraHeaders: {
      "Content-Type": "application/json",
      "Idempotency-Key": idempotencyKey,
    },
  });
  return parseWithFallback(await response.json(), SelfUpdateJobSchema, null, {
    endpoint: "POST /api/self-update/jobs",
  });
}
```

Implement `getSelfUpdateJob` as the matching parsed GET.

- [ ] **Step 5: Run the client test to verify it passes**

Run:

```bash
pnpm --filter @multica/core test api/client.test.ts
```

Expected: PASS with the typed status, idempotent job creation, and malformed-body fallback assertions green.

- [ ] **Step 6: Commit the contract**

```bash
git add packages/core/types/self-update.ts packages/core/types/index.ts packages/core/api/schemas.ts packages/core/api/client.ts packages/core/api/client.test.ts
git commit -m "feat: add typed self-host update client"
```

### Task 2: Replace the sidebar's false update check with a real update control

**Files:**
- Modify: `packages/views/layout/update-status.tsx`
- Modify: `packages/views/layout/update-status.test.ts`
- Modify: `packages/views/locales/en/layout.json`
- Modify: `packages/views/locales/fr/layout.json`
- Modify: `packages/views/locales/ja/layout.json`
- Modify: `packages/views/locales/ko/layout.json`
- Modify: `packages/views/locales/zh-Hans/layout.json`

**Interfaces:**
- Consumes the Task 1 methods and `SelfUpdateStatus`.
- Displays a distinct read-only state for `needs_setup`, `worker_offline`, and `check_failed`.
- Calls `api.createSelfUpdateJob(status.latest_release.tag, idempotencyKey)` only after AlertDialog confirmation.
- Polls `api.getSelfUpdateJob(job.id)` every five seconds while the job phase is not `succeeded`, `failed`, or `needs_decision`.

- [ ] **Step 1: Write failing layout tests**

Expand `packages/views/layout/update-status.test.ts` to mock `@multica/core/api` and assert:

```ts
expect(await screen.findByText("Update to v0.7.0")).toBeInTheDocument();
expect(screen.getByRole("button", { name: "Update to v0.7.0" })).toBeInTheDocument();
```

After clicking that button, assert the confirmation content includes `Database backup` and no `createSelfUpdateJob` call has happened. Click the confirmation action, then assert:

```ts
expect(api.createSelfUpdateJob).toHaveBeenCalledWith(
  "v0.7.0",
  expect.stringMatching(/^self-update-/),
);
```

Add one test each for:

```ts
worker_offline -> "Update worker offline"
needs_setup -> "Update needs setup"
active_job.phase === "deploying" -> "Deploying update"
ApiError with status 404 -> current GitHub fallback
```

- [ ] **Step 2: Run the layout test to verify it fails**

Run:

```bash
pnpm --filter @multica/views test layout/update-status.test.ts
```

Expected: FAIL because the component still only compares GitHub commits and has no self-update action.

- [ ] **Step 3: Implement status mapping and the confirmed action**

Keep `classifyUpstreamUpdate` and `loadCommit` for the explicit 404 compatibility path. Add pure helpers:

```ts
export function isTerminalSelfUpdatePhase(phase: string): boolean {
  return phase === "succeeded" || phase === "failed" || phase === "needs_decision";
}

export function selfUpdateLabel(status: SelfUpdateStatus, t: LayoutTranslator): string {
  if (status.active_job) return t(($) => $.sidebar.self_update_phase, { phase: status.active_job.phase });
  if (status.capability === "needs_setup") return t(($) => $.sidebar.self_update_needs_setup);
  if (status.capability === "worker_offline") return t(($) => $.sidebar.self_update_worker_offline);
  if (status.check_failed) return t(($) => $.sidebar.update_unknown);
  if (status.update_available && status.latest_release) return t(($) => $.sidebar.self_update_available, { tag: status.latest_release.tag });
  return t(($) => $.sidebar.update_current);
}
```

Call `api.getSelfUpdateStatus()` first. Catch `ApiError` status 404 and invoke the old GitHub comparison; show `update_unknown` for other errors. Use `AlertDialog` from `@multica/ui/components/ui/alert-dialog` for the confirmation. While creating, disable both actions and add `aria-busy`.

Use `crypto.randomUUID()` to create a stable per-click idempotency key:

```ts
const idempotencyKey = `self-update-${crypto.randomUUID()}`;
```

Keep the returned job in state, close the dialog only after the creation response is valid, then poll it every five seconds until `isTerminalSelfUpdatePhase(job.phase)` returns true. Refresh status after a terminal phase.

- [ ] **Step 4: Add translated copy**

Add these layout keys in all five locale files, with native-language text:

```json
"self_update_available": "Update to {{tag}}",
"self_update_needs_setup": "Update needs setup",
"self_update_worker_offline": "Update worker offline",
"self_update_phase": "Update: {{phase}}",
"self_update_confirm_title": "Update this self-hosted Multica?",
"self_update_confirm_description": "This creates a database backup and may briefly restart the service.",
"self_update_confirm_action": "Start update",
"self_update_cancel": "Cancel"
```

Keep the existing Desktop update-setting copy unchanged because it refers to the Electron application, not this self-hosted service.

- [ ] **Step 5: Run the layout test to verify it passes**

Run:

```bash
pnpm --filter @multica/views test layout/update-status.test.ts
```

Expected: PASS, including the explicit-confirmation and 404-fallback cases.

- [ ] **Step 6: Commit the UI**

```bash
git add packages/views/layout/update-status.tsx packages/views/layout/update-status.test.ts packages/views/locales/*/layout.json
git commit -m "feat: control self-host updates from sidebar"
```

### Task 3: Make the host worker survive temporary control-plane outages

**Files:**
- Modify: `server/internal/selfupdate/worker.go`
- Modify: `server/internal/selfupdate/worker_test.go`
- Modify: `server/cmd/selfhost-updater/main.go`
- Modify: `server/cmd/selfhost-updater/main_test.go`

**Interfaces:**
- `Worker.Serve` retries only transient `worker control plane unavailable` and `worker control plane HTTP 5xx` failures after `PollInterval`.
- Invalid configuration, invalid control-plane response, 4xx authorization errors, unsafe journals, and host command failures still return immediately.
- `selfhost-updater serve` logs retryable control-plane errors to stderr without leaking the worker token.

- [ ] **Step 1: Write a failing worker retry test**

In `server/internal/selfupdate/worker_test.go`, create an `httptest.Server` whose first `/api/self-update/worker/heartbeat` request returns 503 and whose second returns 200. Set `PollInterval` to `time.Millisecond`, run `Serve` with a cancellable context, cancel after the second heartbeat, then assert:

```go
if heartbeats < 2 {
    t.Fatalf("worker did not retry transient control-plane failure: %d", heartbeats)
}
if err != context.Canceled {
    t.Fatalf("Serve error = %v, want context cancellation", err)
}
```

Add a companion invalid-config test asserting `Serve` still returns its validation error without a retry loop.

- [ ] **Step 2: Run the worker test to verify it fails**

Run:

```bash
cd server && go test ./internal/selfupdate -run 'TestWorkerServe(RetriesTransientControlPlaneFailure|RejectsInvalidConfiguration)' -count=1
```

Expected: FAIL because `Serve` currently returns immediately after the first `PollOnce` error.

- [ ] **Step 3: Add narrow retry classification**

In `server/internal/selfupdate/worker.go`, add:

```go
func retryableControlPlaneError(err error) bool {
    if err == nil {
        return false
    }
    message := err.Error()
    return message == "worker control plane unavailable" ||
        strings.HasPrefix(message, "worker control plane HTTP 5")
}
```

Replace the `if err = w.PollOnce(ctx); err != nil { return err }` branch in `Serve` with a branch that waits for `PollInterval` and continues only for `retryableControlPlaneError(err)`. Return every other error immediately. Reuse the existing context-aware timer select so shutdown is prompt.

In `server/cmd/selfhost-updater/main.go`, wrap `Serve` errors only with the fixed message `worker control plane unavailable` when the error is retryable; preserve the original error for all terminal failures. Do not print config contents or token values.

- [ ] **Step 4: Run the worker test to verify it passes**

Run:

```bash
cd server && go test ./internal/selfupdate -run 'TestWorkerServe(RetriesTransientControlPlaneFailure|RejectsInvalidConfiguration)' -count=1
```

Expected: PASS with one 503 retry and one immediate invalid-config rejection.

- [ ] **Step 5: Run the existing self-update backend suite**

Run:

```bash
cd server && go test ./internal/selfupdate/... ./internal/handler/ -run 'SelfUpdate|SelfHost' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit worker resilience**

```bash
git add server/internal/selfupdate/worker.go server/internal/selfupdate/worker_test.go server/cmd/selfhost-updater/main.go server/cmd/selfhost-updater/main_test.go
git commit -m "fix: retry self-host update worker control plane"
```

### Task 4: Preserve Funnel health validation without host Tailscale DNS

**Files:**
- Modify: `server/internal/selfupdate/config.go`
- Modify: `server/internal/selfupdate/config_test.go`
- Modify: `server/internal/selfupdate/health.go`
- Modify: `server/internal/selfupdate/health_test.go`
- Modify: `server/internal/selfupdate/worker.go`
- Modify: `server/internal/selfupdate/worker_test.go`

**Interfaces:**
- Adds optional `external_resolve_ip` to `WorkerConfig`.
- When set, only the configured Funnel host in `DeployEvidence.ExternalURL` is dialed at that IP; TLS still verifies the original host name.
- A missing resolver override keeps the existing DNS behavior. An invalid IP, an IP used for a non-external URL, or an unexpected external host fails closed.

- [ ] **Step 1: Write failing external-health tests**

In `server/internal/selfupdate/health_test.go`, create a TLS test server and call the new external-health client with:

```go
e := DeployEvidence{
    ReadyURL: server.URL + "/readyz",
    LocalURL: server.URL + "/",
    ExternalURL: "https://funnel.example.test/",
}
```

Configure `external_resolve_ip` as the TLS test server IP and assert `VerifyDeployment` returns nil while the server observes `r.Host == "funnel.example.test"`. Add an invalid-IP config test and a test that `external_resolve_ip` cannot rewrite the local ready or local frontend URLs.

- [ ] **Step 2: Run the new health tests to verify they fail**

Run:

```bash
cd server && go test ./internal/selfupdate -run 'Test(VerifyDeployment|WorkerConfig).*External' -count=1
```

Expected: FAIL because the worker has no configured external resolver and health checks use the process-wide system DNS client.

- [ ] **Step 3: Add the narrow resolver override**

Add this field to `WorkerConfig`:

```go
ExternalResolveIP string `json:"external_resolve_ip"`
```

Validate it with `net/netip.ParseAddr`; accept an empty value, reject an invalid address. Add a helper that clones the HTTP transport and replaces `DialContext` only when `addr` matches the configured external URL host and port:

```go
func externalHealthClient(base *http.Client, externalURL, resolveIP string) (*http.Client, error)
```

Set `tls.Config.ServerName` to the original external host so the certificate is checked against the Funnel domain. Call `VerifyDeployment` using that client only for the external URL; readyz and local frontend must keep their ordinary loopback transports.

- [ ] **Step 4: Run the external-health tests to verify they pass**

Run:

```bash
cd server && go test ./internal/selfupdate -run 'Test(VerifyDeployment|WorkerConfig).*External' -count=1
```

Expected: PASS; the requested hostname remains visible to TLS/HTTP while only its socket target is overridden.

- [ ] **Step 5: Commit resolver support**

```bash
git add server/internal/selfupdate/config.go server/internal/selfupdate/config_test.go server/internal/selfupdate/health.go server/internal/selfupdate/health_test.go server/internal/selfupdate/worker.go server/internal/selfupdate/worker_test.go
git commit -m "fix: verify Funnel health without host DNS"
```

### Task 5: Install the worker from a durable host location

**Files:**
- Create: `scripts/install-selfhost-updater.sh`
- Create: `scripts/install-selfhost-updater.test.sh`
- Modify: `Makefile`
- Modify: `OPS_RUNBOOK.zh.md`

**Interfaces:**
- `make selfhost-updater-install` builds `server/cmd/selfhost-updater`, installs it at `~/.multica/self-update/bin/selfhost-updater`, writes a mode-600 worker config, and registers `com.multica.self-update`.
- The LaunchAgent uses only the durable binary path and writes logs inside `~/.multica/self-update/state/`.
- The installer receives the protected production env file explicitly through `SELFHOST_PRODUCTION_ENV_FILE`; it never uses a source checkout `.env` implicitly.

- [ ] **Step 1: Write failing shell tests**

Create `scripts/install-selfhost-updater.test.sh` using a temporary `HOME` and fake `launchctl`. Assert that a dry run:

```bash
SELFHOST_UPDATER_DRY_RUN=1 \
SELFHOST_PRODUCTION_ENV_FILE="$tmp/production.env" \
./scripts/install-selfhost-updater.sh
```

creates a plist containing:

```xml
<string>$HOME/.multica/self-update/bin/selfhost-updater</string>
```

and rejects a missing, group-readable, or checkout-contained production environment file.

- [ ] **Step 2: Run the installer test to verify it fails**

Run:

```bash
bash scripts/install-selfhost-updater.test.sh
```

Expected: FAIL because the installer and target do not exist.

- [ ] **Step 3: Implement the installer and Make target**

Implement `scripts/install-selfhost-updater.sh` to:

1. require an absolute `SELFHOST_PRODUCTION_ENV_FILE`;
2. check it is mode 600 and outside a directory containing `.git`;
3. create `~/.multica/self-update/{bin,repo.git,state,worktrees}` mode 700;
4. build the current checkout command with `go build -o "$bin" ./server/cmd/selfhost-updater`;
5. write `worker.json` mode 600 with fixed `server_url`, `production_database`, `custom_remote`, and `custom_branch` values sourced from explicit environment variables;
6. write and load `~/Library/LaunchAgents/com.multica.self-update.plist` that invokes only the durable binary;
7. avoid echoing the token.

Add:

```make
selfhost-updater-install:
	bash scripts/install-selfhost-updater.sh
```

Document the exact required environment variables and the `launchctl print gui/$(id -u)/com.multica.self-update` verification in `OPS_RUNBOOK.zh.md`.

- [ ] **Step 4: Run the installer test to verify it passes**

Run:

```bash
bash scripts/install-selfhost-updater.test.sh
```

Expected: PASS.

- [ ] **Step 5: Commit durable worker installation**

```bash
git add scripts/install-selfhost-updater.sh scripts/install-selfhost-updater.test.sh Makefile OPS_RUNBOOK.zh.md
git commit -m "feat: install self-host update worker durably"
```

### Task 6: Build, deploy, and execute one verified production update

**Files:**
- Modify at runtime only: `/Users/mac/.multica/self-update/worker.json`
- Modify at runtime only: `/Users/mac/Library/LaunchAgents/com.multica.self-update.plist`
- Read at runtime only: `/Users/mac/.multica/self-update/state/`
- Read at runtime only: `/Users/mac/Documents/vibcoding/multica/.env`

**Interfaces:**
- Requires Task 1–4 release artifacts and Docker Desktop running.
- Uses the sidebar action from Task 2 to create the production `self_update_job`.

- [ ] **Step 1: Verify the deployment prerequisites**

Run:

```bash
docker ps --format '{{.Names}} | {{.Status}}' --filter name=multica
curl -fsS http://localhost:18097/readyz
curl -fsS http://localhost:13017/
launchctl print gui/$(id -u)/com.multica.self-update
```

Expected: postgres, backend, frontend, and Funnel are running; both local HTTP checks return 200; the LaunchAgent references `~/.multica/self-update/bin/selfhost-updater`.

- [ ] **Step 2: Install the worker against the explicit protected production env**

Run `make selfhost-updater-install` with `SELFHOST_PRODUCTION_ENV_FILE` set to the private operator environment file. Confirm:

```bash
stat -f '%Lp %N' ~/.multica/self-update/worker.json ~/.multica/self-update/production.env
launchctl list | grep com.multica.self-update
```

Expected: both files are 600; the agent is running; `/api/self-update/status` reports `capability: ready`.

Set `external_resolve_ip` in `worker.json` to the verified public address for the configured Funnel host. Do not use an address from the host VPN DNS resolver; verify it with an independent public DNS lookup immediately before installation.

- [ ] **Step 3: Build and deploy backend and frontend**

Build the exact release branch and recreate only backend and frontend with the production compose combination. Then verify:

```bash
curl -fsS http://localhost:18097/readyz
curl -fsS http://localhost:13017/
docker exec multica-backend-1 /app/multica version
```

Expected: readyz and frontend return 200, and the backend reports the release commit passed as Docker `COMMIT`.

- [ ] **Step 4: Create the update from the sidebar**

Open the running Web app, click the self-host update status item, choose “Update to v0.7.0”, and confirm “Start update.” Record the returned job id from:

```bash
docker compose exec -T postgres sh -c \
  'psql -U "$POSTGRES_USER" -d multica -c "select id, phase, step, result from self_update_job order by created_at desc limit 1"'
```

Expected: exactly one newly created job, initially `queued` then claimed by the worker.

- [ ] **Step 5: Observe the full execution and preserve evidence**

Poll the job and worker evidence until it enters a terminal phase:

```bash
docker compose exec -T postgres sh -c \
  'psql -U "$POSTGRES_USER" -d multica -c "select id, phase, step, result, updated_at from self_update_job order by created_at desc limit 1"'
tail -100 ~/.multica/self-update/state/worker.log
find ~/.multica/self-update/state/backups -type f -name '*.dump' -maxdepth 3
```

Expected success evidence: a real backup file, a terminal `succeeded` job, and worker journal evidence without credentials. If the job becomes `failed` or `needs_decision`, stop and report its stored result rather than issuing another update request.

- [ ] **Step 6: Verify deployed service health and image identity**

Run:

```bash
curl -fsS http://localhost:18097/readyz
curl -fsS http://localhost:13017/
curl -fsS https://multica-server.tail709cfb.ts.net/
docker inspect multica-backend-1 --format '{{.Image}}'
docker inspect multica-frontend-1 --format '{{.Image}}'
```

Expected: all three HTTP checks return 200 and both image IDs match the job’s recorded backend/frontend digests.

- [ ] **Step 7: Commit the release implementation**

```bash
git status --short
git log --oneline -4
```

Commit only the code, tests, script, documentation, and locale changes produced in Tasks 1–4. Do not commit runtime secrets, host worker configuration, LaunchAgent files, database data, backup files, or unrelated main-checkout changes.
