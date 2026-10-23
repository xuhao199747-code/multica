# Custom Self-Host One-Click Update Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a deployment administrator click the existing update entry to integrate a stable official Multica release into the pushed custom branch, review and test it, then safely update this self-hosted Docker Compose installation with Mika reporting the outcome.

**Architecture:** Keep the web backend as an authenticated job/control plane and run Git, tests, backup, Docker and health checks in a separate host updater. Persist deployed provenance and job state in PostgreSQL plus a host journal so backend restarts do not erase results. Reuse project-scoped Mika/developer/reviewer runs for evidence, but prevent the project's ordinary delivery mode from deploying a self-update a second time.

**Tech Stack:** Go/Chi, PostgreSQL/sqlc, Git, Docker Compose, React, TanStack Query, zod, Vitest, Go tests, Playwright.

## Global Constraints

- Only the configured self-host deployment administrator may create jobs; a workspace `owner` or `admin` role alone grants no deployment authority. The backend and updater have separate credentials; never mount the Docker socket in the backend.
- Source is the pushed custom branch plus the latest non-draft, non-prerelease official GitHub Release, pinned to a commit SHA. Do not merge upstream `main`, include local dirty files, force-push, or silently change branches.
- A job-specific `self-update/<job-id>` staging branch shares the reviewed candidate between isolated agent workdirs. It is not a release or deployed version; only a reviewer-approved pinned SHA may fast-forward the configured custom branch.
- Current `:dev` images have no trustworthy source provenance. A separately confirmed, successful baseline deployment is required before the UI enables updates.
- Never delete PostgreSQL data, Docker volumes, chats, `/Users/mac/Documents/multica`, or the Tailscale container. Use an explicit production environment file; do not let the checkout's development `.env` choose the production database.
- Candidate tests run only under their own `.env.worktree` and isolated worktree database, never under the protected production env file; reject a candidate worktree that contains a copied `.env`.
- `pg_dump` must complete successfully before replacing backend/frontend. Only backend `/readyz`, the actual local frontend port, and the configured external URL together prove delivery. Do not promise an automatic database rollback after migrations.
- Each update has one active job, at most one automatic development repair task, the existing maximum of two review rework rounds, and at most one automatic update retry. Terminal Mika reports and repair coordination are idempotent by `(job_id, event_kind)`.
- Project workflow is enabled only on the chosen Multica project; update-specific execution records cannot call generic project delivery. No other project's deployment behavior changes.
- Follow repository package boundaries, `parseWithFallback` + zod response schemas, five shared locales, and the UI conventions. Read `packages/ui/docs/button.md` and `packages/ui/docs/dialog.md` before implementing the dialog. Default tests use fake executables, never the user's installed agent CLIs.

## File map and phase boundaries

| Phase | Files | Independently testable result |
| --- | --- | --- |
| A: release and control plane | `server/internal/selfupdate/release.go`, `server/internal/service/self_update_state.go`, `server/internal/handler/self_update.go`, `server/pkg/db/queries/self_update.sql`, migrations `622` onward | Stable-release status, server-side authority, durable single-job API; no deployment yet. |
| B: host execution | `server/cmd/selfhost-updater/`, `server/internal/selfupdate/{git,journal,backup,deploy,health}.go`, `docker-compose.selfhost.build.yml` | Explicit bootstrap and dry-run candidate checks with fake-command tests; production site unchanged until operator setup. |
| C: coordination and UI | `server/internal/handler/self_update_coordination.go`, project workflow guard, `packages/core/self-update/`, `packages/views/layout/update-*`, locale files | Real Mika/developer/reviewer runs and the existing sidebar one-click interaction. |

The phase boundary is a review checkpoint, not a separate product mode: phase A does not expose a working update button, phase B can be exercised by an operator from CLI, and phase C enables the UI only when bootstrap and the worker heartbeat are present.

### Task 1: Resolve the latest stable official release

**Files:**
- Create: `server/internal/selfupdate/release.go`
- Create: `server/internal/selfupdate/release_test.go`

**Interfaces:**

```go
type Release struct { Tag, CommitSHA, URL string }
type ReleaseSource interface { LatestStable(context.Context) (Release, error) }
```

- [ ] **Step 1: Write failing table tests.** An `httptest.Server` returns a stable release JSON plus `/commits/{tag}` SHA; assert the tag/SHA/URL. Return `prerelease:true`, `draft:true`, missing tag, invalid SHA, 429 and malformed JSON in separate cases; each must return an error, never a guessed release.
- [ ] **Step 2: Observe red.** Run `cd server && go test ./internal/selfupdate -run TestLatestStable -count=1`; expected failure: package or `LatestStable` is not defined.
- [ ] **Step 3: Implement `GitHubReleaseSource`.** GET `/repos/multica-ai/multica/releases/latest`, reject non-2xx and draft/prerelease, then GET `/repos/multica-ai/multica/commits/{url.PathEscape(tag)}` and require a 40-hex-character SHA. Give the HTTP client a timeout, an overridable base URL for tests, and no fallback to `main`. Official API contract: [releases](https://docs.github.com/en/rest/releases/releases), [commits](https://docs.github.com/en/rest/commits/commits).
- [ ] **Step 4: Observe green.** Run `cd server && go test ./internal/selfupdate -run TestLatestStable -count=1`; expected PASS.
- [ ] **Step 5: Commit.** `git add server/internal/selfupdate/release.go server/internal/selfupdate/release_test.go && git commit -m "feat: resolve stable self-host update releases"`.

### Task 2: Persist deployed provenance and a bounded job state machine

**Files:**
- Create: `server/migrations/622_self_update_tables.up.sql` and `.down.sql`
- Create: `server/migrations/623_self_update_installation_unique_index.up.sql` and `.down.sql`
- Create: `server/migrations/624_self_update_job_unique_index.up.sql` and `.down.sql`
- Create: `server/migrations/625_self_update_job_active_index.up.sql` and `.down.sql`
- Create: `server/migrations/626_self_update_job_idempotency_index.up.sql` and `.down.sql`
- Create: `server/pkg/db/queries/self_update.sql`
- Create: `server/internal/service/self_update_state.go` and `self_update_state_test.go`
- Modify: `server/pkg/db/generated/` (sqlc output only)

**Interfaces:**

```go
type UpdatePhase string
const (
  PhaseQueued UpdatePhase = "queued"
  PhasePreparing UpdatePhase = "preparing"
  PhaseAwaitingReview UpdatePhase = "awaiting_review"
  PhaseRepairing UpdatePhase = "repairing"
  PhasePushing UpdatePhase = "pushing"
  PhaseBackingUp UpdatePhase = "backing_up"
  PhaseDeploying UpdatePhase = "deploying"
  PhaseVerifying UpdatePhase = "verifying"
  PhaseSucceeded UpdatePhase = "succeeded"
  PhaseFailed UpdatePhase = "failed"
  PhaseNeedsDecision UpdatePhase = "needs_decision"
)
func CanAdvanceUpdate(from, to UpdatePhase, repairCount, retryCount int) bool
```

- [ ] **Step 1: Write failing state tests.** Assert `queued → preparing → awaiting_review → pushing → backing_up → deploying → verifying → succeeded`; allow `preparing/awaiting_review → repairing → preparing` only with `repair_count <= 1` and `retry_count <= 1`; `needs_decision` ends automatic work. Reject transitions out of terminal states and a second repair/retry.
- [ ] **Step 2: Observe red.** Run `cd server && go test ./internal/service -run TestCanAdvanceUpdate -count=1`; expected failure: function not defined.
- [ ] **Step 3: Add tables and indexes.** `self_update_installation` has singleton key, deployed release tag/custom SHA/backend+web digests, project ID, worker heartbeat and update time. `self_update_job` has UUID, requester, target tag+SHA, source SHA, staging branch, candidate SHA, approved SHA, pushed custom SHA, phase, step/result JSON, repair/retry counts, idempotency key and timestamps. No foreign keys. Give each of the four `CREATE UNIQUE INDEX CONCURRENTLY` statements its own migration file; the active index covers every nonterminal phase. SQL queries must include `GetSelfUpdateInstallation`, `UpsertSelfUpdateInstallation`, `CreateSelfUpdateJob`, `ClaimSelfUpdateJob`, `AdvanceSelfUpdateJob`, `GetSelfUpdateJob`, `FindSelfUpdateJobByIdempotencyKey`, and `HeartbeatSelfUpdateWorker`.
- [ ] **Step 4: Implement the pure transition guard and generate sqlc.** Run `make sqlc`, then the focused state test and a DB-backed test for one active job plus idempotent replay. Expected PASS; a concurrent second job gets a conflict, not a second row.
- [ ] **Step 5: Commit.** Add only the listed migrations, query, generated output and tests; `git commit -m "feat: persist self-host update jobs"`.

### Task 3: Add authenticated status, job and worker endpoints

**Files:**
- Create: `server/internal/handler/self_update.go` and `self_update_test.go`
- Modify: `server/internal/handler/handler.go`
- Modify: `server/cmd/server/router.go`
- Modify: `docker-compose.selfhost.yml` (pass the opt-in administrator IDs, worker-token secret and project ID to backend)

**Interfaces:**

```text
GET  /api/self-update/status              authenticated member; read-only capability and latest/deployed versions
POST /api/self-update/jobs                configured deployment administrator only; Idempotency-Key required
GET  /api/self-update/jobs/{id}           configured deployment administrator only
POST /api/self-update/worker/heartbeat    dedicated bearer token, outside browser-session group
POST /api/self-update/worker/claim        dedicated bearer token, outside browser-session group
POST /api/self-update/worker/jobs/{id}    dedicated bearer token; validated phase/evidence update
```

- [ ] **Step 1: Write failing handler tests.** Cover disabled-by-default, no baseline, wrong user ID including workspace owner, valid deployment administrator, missing/replayed idempotency key, two concurrent POSTs, missing/wrong worker token, and unknown release. Read endpoints never expose the token or host paths.
- [ ] **Step 2: Observe red.** Run `cd server && go test ./internal/handler -run TestSelfUpdate -count=1`; expected failure: routes/handlers absent.
- [ ] **Step 3: Implement the routes.** Parse the configured admin UUID allowlist once at startup; fail closed on invalid IDs or missing worker token. Compare bearer secrets in constant time. POST validates the requested stable release against `ReleaseSource`, requires a stamped baseline and recent worker heartbeat, and inserts under the DB active-job constraint. Worker phase updates call `CanAdvanceUpdate`; status compares latest Release with **deployed** tag, not branch HEAD.
- [ ] **Step 4: Run focused and malformed-response tests.** `cd server && go test ./internal/handler -run TestSelfUpdate -count=1`; expected PASS. Verify the public `/api/config` never exposes update credentials or grants update permission.
- [ ] **Step 5: Commit.** Add only these files; `git commit -m "feat: gate self-host update control plane"`.

### Task 4: Prepare candidates in an isolated host worktree

**Files:**
- Create: `server/cmd/selfhost-updater/main.go`
- Create: `server/internal/selfupdate/config.go`, `git.go`, `journal.go`, `worker.go`
- Create: colocated `_test.go` files for each unit

**Interfaces:**

```go
type CommandRunner interface {
  Run(context.Context, string, []string, string, []string) (CommandResult, error)
  RunToFile(context.Context, string, []string, string, []string, string) error
}
type CommandResult struct { Stdout, Stderr string; ExitCode int }
type Candidate struct { JobID, BaseSHA, ReleaseSHA, CandidateSHA, StagingBranch, WorktreePath string }
func PrepareCandidate(context.Context, CommandRunner, WorkerConfig, Job) (Candidate, error)
```

- [ ] **Step 1: Write failing fake-command tests.** Record every executable/argument vector. Assert clean worktree creation from the fetched remote custom SHA, official tag SHA equality, successful non-force merge, conflict preservation for repair, no access to the user's dirty checkout, and rejection of a moved tag or remote branch. Assert a copied `.env` or a test command using the production env is rejected. The job-specific staging ref is created at the custom base SHA before merge; if merge conflicts, the ref remains usable by the one repair task. A failed test/build keeps the merged candidate on that ref for repair but never reaches review/deployment. An existing conflicting staging ref stops the job; no path pushes the custom branch before review.
- [ ] **Step 2: Observe red.** Run `cd server && go test ./internal/selfupdate -run 'Test(PrepareCandidate|Journal)' -count=1`; expected failure: missing functions.
- [ ] **Step 3: Implement candidate and journal operations.** Use `exec.CommandContext` with argument arrays, never `sh -c`; fixed allowed remote/branch from protected host config. Create and non-force push `self-update/<job-id>` at the custom base SHA before attempting the merge. If merge succeeds, push its commit to that same staging ref, then in the isolated worktree run `make setup-worktree` followed by `make check-worktree` and build both candidate images without starting containers. A merge conflict leaves the staging ref at base; a test/build failure leaves it at the merged candidate, both ready for the single repair task. Record command, exit code and candidate SHA as review evidence. The generated `.env.worktree` must name a database distinct from the protected production database. Store one atomic JSON journal under a dedicated job directory, mode `0700`, with candidate SHA, phase, and redacted command outcomes. On restart reconcile it with the server job instead of starting another candidate. Keep the worktree while development/review runs.
- [ ] **Step 4: Implement host polling.** `selfhost-updater serve --config <absolute-path>` heartbeats, claims one job, runs `PrepareCandidate` plus preflight checks/build, and reports its phase with the bearer token. A `--dry-run` mode exercises fetch/merge/tests/build without pushing or starting Docker containers. Run focused tests and `go test ./cmd/selfhost-updater`; expected PASS with fake Git and HTTP server.
- [ ] **Step 5: Commit.** `git add server/cmd/selfhost-updater server/internal/selfupdate && git commit -m "feat: prepare isolated self-host update candidates"`.

### Task 5: Back up, bootstrap and deploy without changing the database identity

**Files:**
- Create: `server/internal/selfupdate/backup.go`, `deploy.go`, `health.go`
- Create: `server/internal/selfupdate/backup_test.go`, `deploy_test.go`, `health_test.go`
- Modify: `server/cmd/selfhost-updater/main.go`
- Modify: `docker-compose.selfhost.build.yml`

**Interfaces:**

```go
type DeployEvidence struct { BackendDigest, WebDigest, DatabaseBackup, LocalURL, ExternalURL string }
func BackupDatabase(context.Context, CommandRunner, WorkerConfig, string) (string, error)
func DeployCandidate(context.Context, CommandRunner, WorkerConfig, Candidate) (DeployEvidence, error)
func VerifyDeployment(context.Context, *http.Client, DeployEvidence) error
```

- [ ] **Step 1: Write failing fake-command and HTTP tests.** Reject mismatched `POSTGRES_DB`/running backend `DATABASE_URL`, absent protected production env file, a nonzero `pg_dump`, an empty backup, bad image build, remote port parse failure, `/readyz` returning 200 without `checks.db=ok` and `checks.migrations=ok`, a failing local frontend, or a failing external URL. Assert none of these cases records success. Assert no command uses `down -v`, `destroy`, `rm`, or recreates postgres/Tailscale.
- [ ] **Step 2: Observe red.** Run `cd server && go test ./internal/selfupdate -run 'Test(BackupDatabase|DeployCandidate|VerifyDeployment)' -count=1`; expected failure: missing functions.
- [ ] **Step 3: Implement explicit production deployment.** `WorkerConfig` requires an absolute, permission-restricted production env-file path outside the checkout and expected database name. Change the build override images to `multica-backend:${MULTICA_CUSTOM_IMAGE_TAG:-dev}` and `multica-web:${MULTICA_CUSTOM_IMAGE_TAG:-dev}`; worker supplies the candidate SHA as tag plus `VERSION`, `COMMIT`, `DATE`, and `NEXT_PUBLIC_APP_VERSION` build args. Rebuild and retest the approved candidate without starting containers, then run `pg_dump` through `RunToFile` into a newly created `0600` file and check exit/size before `docker compose --env-file <production-file> -f ... up -d --no-deps backend frontend`. Preserve the old image IDs. Read actual ports from `docker compose port`; check `/readyz` JSON and HTTP 200 for local/external frontend.
- [ ] **Step 4: Implement one-time bootstrap.** `selfhost-updater bootstrap --config <absolute-path> --source-sha <40-hex> --release-tag <tag> --apply` verifies that the remote custom branch equals `source-sha` and the selected official release commit is an ancestor; without `--apply` it only prints the exact planned source/Compose/database/URLs. With `--apply`, run the same backup/deploy/verification pipeline, then register deployed SHA, release tag and image digests. No implicit bootstrap from the old `:dev` image.
- [ ] **Step 5: Observe green and commit.** Run the focused tests and `cd server && go test ./cmd/selfhost-updater -count=1`; expected PASS without a live Docker call. Commit the listed files as `feat: add guarded self-host update deployment`.

### Task 6: Create an update-specific project execution and review gate

**Files:**
- Create: `server/migrations/627_project_execution_self_update_origin.up.sql` and `.down.sql`
- Create: `server/migrations/628_project_execution_self_update_job_index.up.sql` and `.down.sql`
- Modify: `server/pkg/db/queries/project_workflow.sql`
- Modify: `server/pkg/db/generated/` (sqlc output only)
- Modify: `server/internal/handler/project_workflow.go`
- Create: `server/internal/handler/self_update_coordination.go` and `self_update_coordination_test.go`

**Interfaces:**

```go
type SelfUpdateWorkflowRef struct { JobID, ExecutionID, MicaIssueID, ReviewIssueID string }
func (h *Handler) StartSelfUpdateReview(ctx context.Context, jobID string) (SelfUpdateWorkflowRef, error)
```

- [ ] **Step 1: Write failing DB-backed tests.** With project workflow enabled and Mika/developer/reviewer bound, start one review for a candidate and assert a frozen snapshot containing `goal`, `scope`, `acceptance_criteria`, `risks`, target tag/SHA, staging branch and test/build evidence. Assert its source chat belongs to the triggering admin and the chosen Multica project. Assert development/review assignments have real issues and queued runs with the same staging branch and pinned candidate SHA. Assert `CompleteProjectDelivery` rejects this origin even if the project's delivery mode is `automated_release`; ordinary project executions still complete normally.
- [ ] **Step 2: Observe red.** Run `cd server && go test ./internal/handler -run TestSelfUpdateReview -count=1`; expected failure: update-specific origin/service absent.
- [ ] **Step 3: Persist provenance.** Add `origin text NOT NULL DEFAULT 'project'` and nullable `self_update_job_id` to `project_execution_record` with a check allowing only `project` or `self_update`; add one concurrent unique index on nonnull `self_update_job_id` in its own migration. Generate sqlc. The update request confirmation creates one project-Mika source chat snapshot and one execution tied to the job; a repeated call returns the existing reference.
- [ ] **Step 4: Enforce the review gate.** Start a real Mika issue through `IssueService.Create` so Mika dispatches the project-bound developer for integration checking and then the reviewer. Pin both task resources to `self-update/<job-id>` and require submitted commit/test evidence to match its fetched HEAD; the developer may add repair commits there, never to the configured custom branch. The reviewer records the exact approved SHA and reads the frozen snapshot plus candidate diff/evidence. Only the review outcome `passed` for that SHA releases the host worker to the push/deploy gate. Add a hard guard in generic project delivery so `origin=self_update` cannot invoke Docker, URL checks or completion via the project's delivery configuration.
- [ ] **Step 5: Observe green and commit.** Run `make sqlc`, the focused tests, and `cd server && go test ./internal/handler -run 'Test(SelfUpdateReview|CompleteProjectDelivery)' -count=1`; expected PASS. Commit as `feat: gate self-updates on project review`.

### Task 7: Bound Mika repair and terminal reporting

**Files:**
- Modify: `server/internal/handler/self_update_coordination.go`
- Modify: `server/internal/handler/project_workflow.go`
- Modify: `server/internal/service/self_update_state.go`
- Create: `server/internal/handler/self_update_repair_test.go`
- Create: `server/internal/handler/self_update_report_test.go`
- Create: `server/migrations/629_self_update_event.up.sql` and `.down.sql`
- Create: `server/migrations/630_self_update_event_key_index.up.sql` and `.down.sql`
- Create: `server/migrations/631_self_update_issue_origin_index.up.sql` and `.down.sql`
- Modify: `server/pkg/db/queries/self_update.sql` and generated output
- Modify: `server/pkg/db/queries/issue.sql` and generated output
- Modify: `server/internal/service/issue.go` (only if needed for the existing origin-aware create path)

**Interfaces:**

```go
type UpdateEventKind string
const ( EventRepair UpdateEventKind = "repair"; EventTerminal UpdateEventKind = "terminal" )
func (h *Handler) QueueSelfUpdateMikaEvent(ctx context.Context, jobID string, kind UpdateEventKind) error
```

- [ ] **Step 1: Write failing tests.** Conflict, test/build failure and `needs_changes` each create at most one repair coordination event/issue; Docker, credential, backup and post-deploy health failures create no developer task. A project reviewer cannot edit or dispatch. After the one repair task, a second `needs_changes` stops this update without another repair; after review passes, worker may run exactly one retry. Another failure or duplicate webhook/heartbeat cannot create another task. Terminal success/failure creates at most one Mika reporting run with the job's actual evidence.
- [ ] **Step 2: Observe red.** Run `cd server && go test ./internal/handler -run 'TestSelfUpdate(Repair|Report)' -count=1`; expected failure: event coordinator absent.
- [ ] **Step 3: Implement event idempotency.** Add `self_update_event(id, job_id, kind, issue_id, created_at, delivered_at)` without foreign keys; unique `(job_id, kind)` gets its own concurrent-index migration. Extend the `issue.origin_type` check to admit `self_update_event` and add a separate concurrent unique partial index on `(origin_type, origin_id)` for that type. Stamp every Mika coordination issue with `origin_id=self_update_event.id`; after a crash, `GetIssueByOrigin` finds the already-created real issue and its queued run instead of creating another. Use the existing origin-aware `IssueService.Create` path; do not insert a fake Mika assistant message. Extend only the update-specific path of project workflow so `needs_changes` uses the one repair task and existing two-round ceiling.
- [ ] **Step 4: Push only the approved candidate.** Fetch the staging branch and require its HEAD to equal the reviewer-approved SHA, descend from the original custom base and contain the pinned official Release SHA. Retest/rebuild that exact SHA in the isolated worktree, then fetch the custom remote branch again and require its original base SHA. Push the approved SHA to the custom branch with ordinary non-force fast-forward semantics; if either ref moved, mark this job failed with no deployment. Record the pushed SHA separately from deployed provenance. Only after the push is confirmed may the worker back up and deploy. Fake Git tests must prove no custom-branch push before approval and no deployment after a rejected push.
- [ ] **Step 5: Cover backend outage.** The host journal keeps unsent phase/terminal evidence while HTTP is unavailable. On reconnection it replays the same event IDs; tests assert one Mika issue/run, not duplicates. If the site remains unavailable, logs remain on the host and the API does not claim delivery.
- [ ] **Step 6: Observe green and commit.** Run `make sqlc`, the focused tests and `make test`; expected PASS. Commit as `feat: coordinate bounded self-update repair and reports`.

### Task 8: Replace the sidebar indicator with a guarded update interaction

**Files:**
- Create: `packages/core/self-update/queries.ts` and `queries.test.ts`
- Modify: `packages/core/api/client.ts`, `schemas.ts`, `schemas.test.ts`
- Modify: `packages/views/layout/update-status.tsx`, `update-status.test.ts`
- Create: `packages/views/layout/update-dialog.tsx`, `update-dialog.test.tsx`
- Modify: `packages/views/layout/app-sidebar.tsx`
- Modify: `packages/views/layout/dashboard-layout.tsx`
- Modify: `apps/desktop/src/renderer/src/components/desktop-layout.tsx` (read-only update link; no website deployment action)
- Modify: `packages/views/locales/{en,zh-Hans,fr,ja,ko}/layout.json`

**Interfaces:**

```ts
type SelfUpdateStatus = {
  capability: "unsupported" | "needs_setup" | "ready" | "worker_offline";
  can_update: boolean;
  latest_release: { tag: string; sha: string; url: string } | null;
  deployed_release: string | null;
  deployed_custom_sha: string | null;
  active_job: { id: string; phase: string; step: string } | null;
};
```

- [ ] **Step 1: Read UI contracts and write failing tests.** Read `packages/ui/docs/button.md`, `packages/ui/docs/dialog.md`, and both conventions pages. Tests cover unsupported older backend, no setup, worker offline, current, available, updating, needs action, success, unknown server phase, malformed API response, non-admin read-only view, double-click idempotency, refresh persistence and accessible dialog labels. Desktop must not expose the website-deploy action.
- [ ] **Step 2: Observe red.** Run `pnpm test -- packages/views/layout/update-status.test.ts packages/views/layout/update-dialog.test.tsx packages/core/api/schemas.test.ts`; expected failure: new contracts or dialog absent.
- [ ] **Step 3: Add shared API contracts.** Add zod schemas with optional/defaulted fields and unknown phase fallback; every new client method uses `parseWithFallback`, not `as T`. Put workspace-independent query keys under `packages/core/self-update/queries.ts`; mutations invalidate status/job queries. Keep the existing release-history external link as a non-destructive fallback.
- [ ] **Step 4: Implement one-row UI.** Give shared `AppSidebar` an explicit `updateCapability: "website" | "info-only"` prop; Web `DashboardLayout` passes `website`, Desktop passes `info-only` and keeps only the official release-history link. In Web, clicking the available state opens a confirmation dialog showing target version, deployed custom SHA, branch, backup and brief-interruption warning. Confirm POSTs once and opens persistent progress; failure links to evidence and the triggering admin's project Mika chat. Sidebar label stays short. Use existing dialog/button primitives and provide all five locales.
- [ ] **Step 5: Observe green and commit.** Run focused tests, `pnpm typecheck`, `pnpm lint`; expected PASS. Commit as `feat: add guarded self-host update control`.

### Task 9: Verify the integrated path and document operator setup/recovery

**Files:**
- Create: `server/internal/selfupdate/integration_test.go` (fake command runner and HTTP services)
- Create: `e2e/self-host-update.spec.ts` (fake API, no production update)
- Modify: `apps/docs/content/docs/self-host-quickstart.mdx`
- Modify: `apps/docs/content/docs/self-host-quickstart.zh.mdx`
- Modify: `.env.example` (names/documentation only; no secrets)

- [ ] **Step 1: Write the end-to-end failure matrix.** Fake Git, Compose, PostgreSQL dump, readiness endpoints and project-agent records; assert success only with release SHA, exact reviewed staging SHA, code/test/review evidence, backup, `/readyz` and both site URLs. Assert predeploy failure does not call `up`, changed staging/custom refs block deployment, and the staging branch alone never changes deployed provenance. Postdeploy failure remains failed with backup/old-image evidence; backend restart resumes exactly one job/Mika report.
- [ ] **Step 2: Run red.** Run `cd server && go test ./internal/selfupdate -run TestSelfUpdateIntegration -count=1` and `pnpm exec playwright test e2e/self-host-update.spec.ts`; expected initial FAIL until wiring is complete.
- [ ] **Step 3: Document the one-time operator action.** State exact protected config fields, `bootstrap` dry-run and `--apply`, admin account IDs, project bindings, stable-release policy, worker process supervision, backup location, recovery procedure and the limitation that Mika cannot report while the site is down. Make clear that this does **not** change other projects' delivery modes or Desktop/mobile updates. Do not include real tokens, credentials, or a production env file in Git.
- [ ] **Step 4: Run verification.** Run focused Go tests, `make sqlc` if queries changed, `make check`, `pnpm typecheck`, `pnpm lint`, `pnpm test`, and `git diff --check`; report any unavailable environment honestly. Do not run real-agent smoke tests or a production Docker update without separate explicit authorization.
- [ ] **Step 5: Commit and hand off.** Commit tests/docs as `test: verify guarded self-host update flow`. Report the rollout order: deploy API with update disabled, install host worker, run explicit baseline bootstrap, configure the Multica project roles, then enable the sidebar action.
