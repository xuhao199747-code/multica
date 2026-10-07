# 项目本地工作区 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让每个浏览器项目自动绑定当前 Mac daemon 上的同步项目目录，使该项目内全部聊天可按需在该目录读写文件。

**Architecture:** 本机 `multica sync` 已经是项目目录的权威创建者。每次同步解析当前 daemon ID、确保项目目录存在，再用已有 Project Resource API 对齐一条 `local_directory` 资源。服务端现有领取链会将该资源下发给匹配 daemon。

**Tech Stack:** Go、Cobra CLI、Multica Project Resource HTTP API、现有 local daemon、Go testing。

## Global Constraints

- 根目录由 `multica sync --root` 传入；绝不读写该根目录之外的路径。
- 自动资源类型为 `local_directory`，`daemon_id` 等于当前 profile 的 daemon ID，`execution_mode` 固定 `in_place`。
- 每项目仅维护当前 daemon 的一条自动资源；不得修改其他 daemon 的资源。
- 不递归读取或把整个目录写入聊天上下文。

---

### Task 1: 暴露同步器最终项目目录

**Files:**
- Modify: `server/internal/daemon/localsync/exporter.go`
- Modify: `server/internal/daemon/localsync/exporter_test.go`

**Interfaces:**
- Produces: `func (e *Exporter) EnsureProjectPath(projectID, title string) (string, error)`

- [ ] **Step 1: Write failing test**

```go
func TestEnsureProjectPathReturnsRenamedOwnedDirectory(t *testing.T) {
    e := NewExporter(t.TempDir())
    _, err := e.EnsureProjectPath("p", "旧项目")
    if err != nil { t.Fatal(err) }
    path, err := e.EnsureProjectPath("p", "新项目")
    if err != nil { t.Fatal(err) }
    if filepath.Base(path) != "新项目" { t.Fatalf("path = %q", path) }
}
```

- [ ] **Step 2: Verify RED**

Run: `go test ./server/internal/daemon/localsync -run TestEnsureProjectPathReturnsRenamedOwnedDirectory -count=1`

Expected: compile error, method undefined.

- [ ] **Step 3: Implement**

```go
func (e *Exporter) EnsureProjectPath(projectID, title string) (string, error) {
    return e.ensureProject(projectID, title)
}
func (e *Exporter) EnsureProject(projectID, title string) error {
    _, err := e.EnsureProjectPath(projectID, title)
    return err
}
```

- [ ] **Step 4: Verify GREEN**

Run: `go test ./server/internal/daemon/localsync -count=1`

- [ ] **Step 5: Commit**

```bash
git add server/internal/daemon/localsync/exporter.go server/internal/daemon/localsync/exporter_test.go
git commit -m "feat: expose synced project directory path"
```

### Task 2: 同步项目目录资源

**Files:**
- Modify: `server/cmd/multica/cmd_sync.go`
- Modify: `server/cmd/multica/cmd_sync_test.go`

**Interfaces:**
- Consumes: `EnsureProjectPath`, `daemon.EnsureDaemonID(resolveProfile(cmd))`.
- Produces: `reconcileLocalProjectDirectory(ctx, client, projectID, title, path, daemonID) error`.
- Uses: `GET /api/projects/{id}/resources`, `POST /api/projects/{id}/resources`, `PUT /api/projects/{id}/resources/{resourceId}`.

- [ ] **Step 1: Write failing tests**

```go
func TestReconcileLocalProjectDirectoryCreatesResource(t *testing.T) {
    api := newProjectResourceTestServer(t, nil)
    err := reconcileLocalProjectDirectory(context.Background(), api.Client,
        "p1", "项目", "/tmp/multica/项目", "daemon-1")
    if err != nil { t.Fatal(err) }
    got := api.onlyLocalDirectory(t)
    if got.ResourceRef["local_path"] != "/tmp/multica/项目" ||
       got.ResourceRef["daemon_id"] != "daemon-1" { t.Fatalf("%#v", got.ResourceRef) }
}
func TestReconcileLocalProjectDirectoryPreservesOtherDaemon(t *testing.T) {
    api := newProjectResourceTestServer(t, []testResource{
        localResource("foreign", "/other", "daemon-other"),
        localResource("mine", "/old", "daemon-1"),
    })
    if err := reconcileLocalProjectDirectory(context.Background(), api.Client,
        "p1", "项目", "/new", "daemon-1"); err != nil { t.Fatal(err) }
    if api.resource("mine").ResourceRef["local_path"] != "/new" { t.Fatal("mine not updated") }
    if api.resource("foreign").ResourceRef["local_path"] != "/other" { t.Fatal("foreign changed") }
}
```

- [ ] **Step 2: Verify RED**

Run: `go test ./server/cmd/multica -run TestReconcileLocalProjectDirectory -count=1`

Expected: compile error, reconciliation function undefined.

- [ ] **Step 3: Implement exact-daemon reconcile**

```go
// List current resources. Find only resource_type=local_directory where
// resource_ref.daemon_id == daemonID. POST one when absent. PUT that row when
// local_path, label, or execution_mode differs; preserve unknown ref fields.
func reconcileLocalProjectDirectory(ctx context.Context, c *cli.APIClient,
    projectID, title, path, daemonID string) error
```

- [ ] **Step 4: Invoke from sync loop**

```go
daemonID, err := daemon.EnsureDaemonID(resolveProfile(cmd))
if err != nil { return fmt.Errorf("resolve local daemon id: %w", err) }
path, err := e.EnsureProjectPath(id, title)
if err != nil { return err }
if err := reconcileLocalProjectDirectory(ctx, c, id, title, path, daemonID); err != nil {
    return fmt.Errorf("sync project local directory %s: %w", id, err)
}
```

- [ ] **Step 5: Verify**

Run: `go test ./server/cmd/multica -run 'TestReconcileLocalProjectDirectory|TestSync' -count=1 && go build ./server/cmd/multica`

- [ ] **Step 6: Commit**

```bash
git add server/cmd/multica/cmd_sync.go server/cmd/multica/cmd_sync_test.go
git commit -m "feat: bind synced projects to local directories"
```

### Task 3: 覆盖聊天领取载荷

**Files:**
- Modify: `server/internal/handler/claim_project_context_test.go`

**Interfaces:**
- Consumes: existing `resolveClaimProjectContext`.
- Produces: a regression test that a project chat task contains the project’s matching `local_directory` resource.

- [ ] **Step 1: Write failing database-backed test**

```go
func TestClaimChatTaskIncludesProjectLocalDirectory(t *testing.T) {
    // Create project + local_directory resource:
    // {"local_path":"/Users/example/Documents/multica/项目",
    //  "daemon_id":"daemon-test","execution_mode":"in_place"}.
    // Create a project-bound chat task, claim it, and assert the exact resource
    // appears in project_resources.
}
```

- [ ] **Step 2: Verify RED**

Run: `go test ./server/internal/handler -run TestClaimChatTaskIncludesProjectLocalDirectory -count=1`

Expected: assertion fails until fixture creation and exact claim assertion are added.

- [ ] **Step 3: Complete fixture and assertion**

Insert the resource using the existing project-resource test helpers; claim through the existing chat task endpoint; assert `local_path`, `daemon_id`, and `execution_mode`. Also assert an unassigned chat has no project resources.

- [ ] **Step 4: Verify**

Run: `go test ./server/internal/handler -run 'TestClaimChatTaskIncludesProjectLocalDirectory|TestClaim.*Project' -count=1`

Expected: PASS, or documented SKIP when no test database exists.

- [ ] **Step 5: Commit**

```bash
git add server/internal/handler/claim_project_context_test.go
git commit -m "test: cover project directory in chat claims"
```

### Task 4: Ship and validate locally

**Files:**
- Modify locally only: `server/bin/multica` (generated binary, do not commit)

- [ ] **Step 1: Build**

Run: `go build -o server/bin/multica ./server/cmd/multica`

- [ ] **Step 2: Reconcile live projects**

Run: `server/bin/multica sync once --root /Users/mac/Documents/multica`

- [ ] **Step 3: Validate**

Run: `server/bin/multica daemon status --output json`

Then list a project’s resources and verify one `local_directory` is beneath `/Users/mac/Documents/multica`, uses the displayed daemon ID, and has `execution_mode: "in_place"`.

- [ ] **Step 4: Commit source changes**

```bash
git add server/internal/daemon/localsync/exporter.go server/internal/daemon/localsync/exporter_test.go server/cmd/multica/cmd_sync.go server/cmd/multica/cmd_sync_test.go server/internal/handler/claim_project_context_test.go
git commit -m "feat: give project chats a shared local workspace"
```

