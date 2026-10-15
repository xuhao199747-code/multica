# Upstream-Safe Customization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Put the current Multica customizations in a clean personal GitHub fork so future official updates can be merged without overwriting local functionality.

**Architecture:** Preserve the existing working directory unchanged as a timestamped local recovery copy. Clone the user-owned fork into a clean sibling checkout, make the official repository its `upstream`, transfer only the current source diff, commit it on a dedicated `codex/browser-chat-local-sync` branch, and verify the frontend and local-sync tests before using that checkout for later releases.

**Tech Stack:** Git, GitHub CLI, Docker BuildKit, pnpm/Vitest, Go tests.

## Global Constraints

- Never modify PostgreSQL data, Docker volumes, or `/Users/mac/Documents/multica`.
- Never force-push, reset hard, or delete the legacy checkout.
- Keep the official repository read-only through an `upstream` remote.
- Keep browser project/chat workflow and local filesystem synchronization in the feature branch.

---

### Task 1: Create a user-owned fork and recovery point

**Files:**
- Create: `/Users/mac/Documents/vibcoding/multica-upgrade-backup-2026-10-07/` (recoverable directory rename)
- Create: `https://github.com/xuhao199747-code/multica` (GitHub fork)

**Interfaces:**
- Consumes: official `https://github.com/multica-ai/multica.git` at `bb14e8763ced1cfd7a640375563e0daa6fc31153`.
- Produces: personal GitHub repository with `origin` and an unchanged local recovery copy.

- [ ] **Step 1: Verify the official default revision and current Docker health**

Run: `git ls-remote https://github.com/multica-ai/multica.git HEAD && curl -fsSI http://localhost:3000 | head -1`

Expected: the official `bb14e876...` revision and `HTTP/1.1 200 OK`.

- [ ] **Step 2: Create the GitHub fork without cloning into the dirty directory**

Run: `gh repo fork multica-ai/multica --clone=false`

Expected: `xuhao199747-code/multica` is created and accessible.

- [ ] **Step 3: Preserve the current checkout by renaming it, not deleting it**

Run: `mv /Users/mac/Documents/vibcoding/multica /Users/mac/Documents/vibcoding/multica-upgrade-backup-2026-10-07`

Expected: the original sources remain recoverable at the backup path; running containers stay up because they use built images.

### Task 2: Construct the clean, customized checkout

**Files:**
- Create: `/Users/mac/Documents/vibcoding/multica/` (clean clone)
- Modify: Git history only, with a custom-feature commit on `codex/browser-chat-local-sync`.

**Interfaces:**
- Consumes: backup source tree and the user-owned fork.
- Produces: `origin` → personal fork, `upstream` → official Multica, and a branch that contains the local customizations as a reviewable diff.

- [ ] **Step 1: Clone the personal fork into the original workspace path**

Run: `git clone https://github.com/xuhao199747-code/multica.git /Users/mac/Documents/vibcoding/multica`

Expected: `git status --short` is empty and `origin` points to the personal fork.

- [ ] **Step 2: Add and verify the official read-only remote**

Run: `git remote add upstream https://github.com/multica-ai/multica.git && git fetch upstream && git remote -v`

Expected: `origin` points to the fork and `upstream` points to the official repository.

- [ ] **Step 3: Create the dedicated customization branch**

Run: `git switch -c codex/browser-chat-local-sync`

Expected: branch name is `codex/browser-chat-local-sync`.

- [ ] **Step 4: Generate and apply only the source differences from the recovery copy**

Run: `git diff --no-index --binary -- /Users/mac/Documents/vibcoding/multica /Users/mac/Documents/vibcoding/multica-upgrade-backup-2026-10-07 > /tmp/multica-customizations.patch || test $? -eq 1; git apply --check /tmp/multica-customizations.patch; git apply /tmp/multica-customizations.patch`

Expected: project/chat hierarchy, project rename, and server local-sync source changes appear as tracked modifications; unrelated upstream files stay unchanged.

- [ ] **Step 5: Review and commit the transferred feature**

Run: `git status --short && git diff --stat && git add server packages docs/superpowers && git commit -m "feat: add project chat hierarchy and local sync"`

Expected: exactly one feature commit; no credentials, Docker volumes, generated cache, or environment file is committed.

- [ ] **Step 6: Push the feature branch to the personal fork**

Run: `git push -u origin codex/browser-chat-local-sync`

Expected: branch is available on the user-owned GitHub fork.

### Task 3: Verify the migratable customization and document updates

**Files:**
- Modify: `/Users/mac/Documents/vibcoding/multica/README.zh.md` (only if an existing local-sync section is available; otherwise do not add unrelated documentation)

**Interfaces:**
- Consumes: clean feature branch from Task 2.
- Produces: verified source branch and an explicit later-update command sequence.

- [ ] **Step 1: Run the local-sync tests**

Run: `PATH=/var/folders/53/bfhdhqs56hj7fzl043wb9ydc0000gn/T/tmp.yXz2I1ESfL/go/bin:$PATH go test ./cmd/multica ./internal/daemon/localsync -count=1`

Expected: both packages pass.

- [ ] **Step 2: Run the project chat-list component test in the Docker builder**

Run: `docker build --target builder -f Dockerfile.web -t multica-web-builder:project-chat-tree . && docker run --rm --entrypoint sh multica-web-builder:project-chat-tree -lc 'pnpm --filter @multica/views test --run chat/components/chat-thread-list.test.tsx'`

Expected: the chat-thread-list test file passes.

- [ ] **Step 3: Record the future update procedure in the final handoff**

Run: `git fetch upstream && git log --oneline --left-right --cherry-pick codex/browser-chat-local-sync...upstream/main`

Expected: commands show which official commits are pending; future update is `git merge upstream/main`, resolve only overlapping changes, test, then rebuild/restart the frontend image.
