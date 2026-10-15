# Local Sync Trash Deletion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move metadata-verified local chat and project directories to the macOS Trash when browser sync removes them, never permanently deleting user files.

**Architecture:** `localsync.Exporter` receives an injected `moveToTrash` function for deterministic tests. The production default uses Finder through Darwin-specific AppleScript; unsupported platforms return an error rather than permanently deleting content. All exporter removal paths call one root-constrained helper.

**Tech Stack:** Go 1.26, Cobra, macOS `osascript`, Go tests.

## Global Constraints

- Only a path below the configured sync root can be sent to Trash.
- Chats require matching `metadata.json`; projects require matching `.multica-project.json`.
- A deleted project moves its complete local directory, including user and agent files.
- A Trash failure retains the original directory and returns an error for retry.
- Synchronized content must not have an `os.RemoveAll` fallback.

---

### Task 1: Add a platform-safe Trash adapter

**Files:**
- Create: `server/internal/daemon/localsync/trash_darwin.go`
- Create: `server/internal/daemon/localsync/trash_other.go`

**Interfaces:** Produces `func defaultMoveToTrash(path string) error`, consumed by `NewExporter`.

- [ ] **Step 1: Add the Darwin implementation**

```go
//go:build darwin

package localsync

import (
    "fmt"
    "os/exec"
)

func defaultMoveToTrash(path string) error {
    script := `on run argv
    tell application "Finder" to delete POSIX file (item 1 of argv)
end run`
    if output, err := exec.Command("/usr/bin/osascript", "-e", script, path).CombinedOutput(); err != nil {
        return fmt.Errorf("move %q to Trash: %w: %s", path, err, output)
    }
    return nil
}
```

- [ ] **Step 2: Add the fail-closed non-Darwin implementation**

```go
//go:build !darwin

package localsync

import "fmt"

func defaultMoveToTrash(path string) error {
    return fmt.Errorf("cannot move %q to the system trash on this platform", path)
}
```

- [ ] **Step 3: Format and compile**

Run: `gofmt -w server/internal/daemon/localsync/trash_darwin.go server/internal/daemon/localsync/trash_other.go && (cd server && go test ./internal/daemon/localsync -run '^$' -count=1)`

Expected: PASS.

- [ ] **Step 4: Commit**

Run: `git add server/internal/daemon/localsync/trash_darwin.go server/internal/daemon/localsync/trash_other.go && git commit -m "feat: add local sync trash adapter"`

### Task 2: Route exporter cleanup through the adapter

**Files:**
- Modify: `server/internal/daemon/localsync/exporter.go:20-21,255-405`
- Modify: `server/internal/daemon/localsync/exporter_test.go:43-211`

**Interfaces:** Consumes `defaultMoveToTrash`. Produces `Delete`, `Prune`, `PruneProjects`, and empty-parent cleanup that use `Exporter.moveToTrash` for every synchronized-content deletion.

- [ ] **Step 1: Write failing test coverage**

Add a test helper that records Trash targets without moving any file:

```go
func newTrashRecordingExporter(root string) (*Exporter, *[]string) {
    targets := []string{}
    exporter := NewExporter(root)
    exporter.moveToTrash = func(path string) error {
        targets = append(targets, path)
        return nil
    }
    return exporter, &targets
}
```

Add these test cases:

```go
func TestDeleteMovesVerifiedChatDirectoryToTrash(t *testing.T)
func TestPruneMovesMissingChatDirectoryToTrash(t *testing.T)
func TestPruneProjectsMovesCompleteDeletedProjectToTrash(t *testing.T)
func TestTrashFailureLeavesChatDirectoryForRetry(t *testing.T)
```

The first two create a chat with `Sync`, capture its metadata-owned path, invoke `Delete` or `Prune`, and assert it is the recorded target. The project test adds `agent-output.txt`, calls `PruneProjects(map[string]bool{})`, and asserts the target is the whole project directory. The failure test injects `errors.New("offline")`, expects that error, and asserts `os.Stat(chat)` succeeds.

- [ ] **Step 2: Verify RED**

Run: `(cd server && go test ./internal/daemon/localsync -run 'Test(DeleteMovesVerifiedChatDirectoryToTrash|PruneMovesMissingChatDirectoryToTrash|PruneProjectsMovesCompleteDeletedProjectToTrash|TrashFailureLeavesChatDirectoryForRetry)' -count=1)`

Expected: FAIL because `Exporter` has no injectable Trash operation.

- [ ] **Step 3: Implement the minimal root-constrained operation**

Use this exporter shape:

```go
type Exporter struct {
    root        string
    moveToTrash func(string) error
}

func NewExporter(root string) *Exporter {
    return &Exporter{root: root, moveToTrash: defaultMoveToTrash}
}

func (e *Exporter) trash(path string) error {
    root, err := filepath.Abs(e.root)
    if err != nil { return err }
    target, err := filepath.Abs(path)
    if err != nil { return err }
    rel, err := filepath.Rel(root, target)
    if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
        return fmt.Errorf("refusing to move path outside sync root to Trash: %s", target)
    }
    return e.moveToTrash(target)
}
```

Replace `os.RemoveAll(dir)` in `Delete`, `Prune`, and `PruneProjects` with `e.trash(dir)`. Make `removeOwnedEmptyProject` an `Exporter` method and replace its `os.RemoveAll(dir)` call with `e.trash(dir)`. In `PruneProjects`, remove the `len(children) == 1` condition: matching project metadata plus no active chats authorizes moving the complete project directory. Keep `removeEmptyParents` limited to `os.Remove` for genuinely empty parent directories.

- [ ] **Step 4: Verify GREEN**

Run: `(cd server && go test ./internal/daemon/localsync -run 'Test(DeleteMovesVerifiedChatDirectoryToTrash|PruneMovesMissingChatDirectoryToTrash|PruneProjectsMovesCompleteDeletedProjectToTrash|TrashFailureLeavesChatDirectoryForRetry)' -count=1)`

Expected: PASS.

- [ ] **Step 5: Run the package suite**

Run: `(cd server && go test ./internal/daemon/localsync -count=1)`

Expected: PASS.

- [ ] **Step 6: Commit**

Run: `git add server/internal/daemon/localsync/exporter.go server/internal/daemon/localsync/exporter_test.go && git commit -m "feat: move synced deletions to trash"`

### Task 3: Surface the behavior in CLI help

**Files:**
- Modify: `server/cmd/multica/cmd_sync.go:19-22`
- Modify: `server/cmd/multica/cmd_sync_test.go`
- Modify: `docs/superpowers/specs/2026-10-14-local-sync-trash-design.md`

**Interfaces:** Consumes production `localsync.NewExporter`; produces discoverable operator help.

- [ ] **Step 1: Write a failing help test**

Create `TestSyncHelpExplainsTrashDeletion`: execute `syncCmd.SetArgs([]string{"--help"})`, direct `syncCmd.SetOut` to a `bytes.Buffer`, and assert the output contains `Deleted browser chats and projects are moved to the system Trash.`

- [ ] **Step 2: Verify RED**

Run: `(cd server && go test ./cmd/multica -run TestSyncHelpExplainsTrashDeletion -count=1)`

Expected: FAIL because the string is absent.

- [ ] **Step 3: Implement concise copy and document platform behavior**

Set `syncCmd.Long` to:

```go
"Mirror browser chat transcripts to a local folder. Deleted browser chats and projects are moved to the system Trash."
```

Add to the design document that macOS uses Finder through `/usr/bin/osascript`; unsupported platforms fail closed and retain local content.

- [ ] **Step 4: Verify integration**

Run: `(cd server && go test ./cmd/multica ./internal/daemon/localsync -count=1)`

Expected: PASS.

- [ ] **Step 5: Commit**

Run: `git add server/cmd/multica/cmd_sync.go server/cmd/multica/cmd_sync_test.go docs/superpowers/specs/2026-10-14-local-sync-trash-design.md && git commit -m "docs: clarify local sync trash behavior"`

## Plan self-review

- Spec coverage: Tasks 1 and 2 implement macOS Trash movement, root and metadata safeguards, complete project-directory behavior, and retryable errors. Task 3 makes the consequence visible to operators.
- Placeholder scan: no deferred work or unspecified test behavior remains.
- Type consistency: every cleanup path calls `Exporter.trash`; both platform files define the symbol consumed by `NewExporter`.

