# Multica 执行规范（运维操作手册）

> 本文档是本仓库唯一的操作事实来源。命令都经过 2026-10-15 实际验证，2026-10-16 复核（地址、端口、对外入口、邮件通道已按当前实际状态更新）。
> 修改流程、地址、端口后必须同步更新本文档。

## 1. 环境地图（东西都在哪）

| 环境 | 位置 | 地址 | 跑的代码 | 数据 |
| --- | --- | --- | --- | --- |
| 生产栈（Docker） | 本机 Docker，compose 托管 | `http://localhost:13017`（前端，仅 127.0.0.1）/ `http://localhost:18097`（后端，仅 127.0.0.1） | 镜像 `multica-{web,backend}:dev`（从源码构建） | 卷 `multica_pgdata`、`multica_backend_uploads` |
| 对外入口 | `multica-tailscale` 容器（Tailscale Funnel，见 §1a）→ 前端容器 | `https://multica-server.tail709cfb.ts.net`（公网可达） | 同生产栈 | 同生产栈 |
| 本地开发后端 | 本机进程（`go run`） | `http://localhost:18080` | 当前工作区源码 | 库 `multica_dev_postgres`（127.0.0.1:**5433**，卷 `multica_dev_pgdata`，与生产隔离） |
| 本地开发前端 | 本机进程（`pnpm dev:web`，热更新） | `http://localhost:3100` | 当前工作区源码，连 18080 | 无 |
| Git 远端 | GitHub | `origin` = `xuhao199747-code/multica`（你的 fork，可推送）；`upstream` = `multica-ai/multica`（官方，只读） | — | — |

关键事实（2026-10-16 逐条核实）：

- 生产数据库容器**发布了宿主端口** `127.0.0.1:5432`（2026-10-15 版"没有发布"已过时）。
- 对外入口由 `multica-tailscale` 容器的 Funnel 提供，**该容器不要动**（登录态与 serve 配置都在卷 `multica-tailscale-state` 里）；宿主机 Tailscale.app 未运行、也不需要。细节见 §1a。
- 邮件通道：生产 `.env` 走 **Resend**（`RESEND_API_KEY`、`RESEND_FROM_EMAIL` 已配置；`SMTP_HOST` 为空 = SMTP 未启用）。改邮件 = 改 `.env` + 重启后端容器。机制见 `SELF_HOSTING_ADVANCED.md` 的 Email 一节。本地 dev（18080）不发邮件，验证码打印在后端日志（`[DEV] Verification code ...`，问我"验证码"即可）。
- 当前运行中的生产栈是 2026-10-16 用 `/tmp/multica-workbench-route-G7mLOS/` 下的 compose 副本启动的（宿主端口 13017/18097/5432 与仓库 compose 默认的 3000/8080 不同，属未收尾变更，见 §10）。postgres/frontend/backend/tailscale 都使用 `unless-stopped` 重启策略；Docker Desktop 启动后会恢复整套生产服务。
- `docker compose` 必须带 `-f docker-compose.selfhost.yml`（必要时加 `-f docker-compose.selfhost.build.yml`）；`.env` 必须存在（密钥已与线上一致，绝不要重新随机生成，否则全员掉登录）。
- `/health` 返回的 `commit` 字段 = 生产后端的真实版本身份证明（构建时注入）。当前值 `254852f`（即 20261016-1e2268d 那次发布的构建 HEAD，代码版本 = 1e2268d）。
- 本机网络注意：github.com 时通时断（推送需重试）；brew 不可用（Go 手工装在 `/opt/homebrew/opt/go`）；api.github.com 稳定。

## 1a. 对外入口（线上网址）是怎么搭的

链路：公网浏览器 → Tailscale Funnel 边缘节点（公共 anycast IP `103.84.155.x`，所以 `*.ts.net` 域名公网 DNS 可直接解析）→ 本机 `multica-tailscale` 容器（tailnet 机器名 `multica-server`，100.96.49.87）→ `host.docker.internal:13017`（前端容器宿主端口）→ 容器内后端。

- 容器内跑 `tailscaled`（userspace networking，socket 在 `/tmp/tailscaled.sock`，状态卷 `multica-tailscale-state`）。仓库的 compose 文件**没有**这个服务，它是单独 `docker run` 起来的。
- 查看/验证配置：`docker exec multica-tailscale tailscale --socket=/tmp/tailscaled.sock serve status`。注意必须带 `--socket=/tmp/tailscaled.sock`——在宿主机直接跑 `tailscale serve status` 会因宿主机未登录而挂起。
- serve/funnel 配置持久化在状态卷里：重建容器只要挂回 `multica-tailscale-state`，就以 `multica-server` 身份自动恢复，不用重配。只有要改 proxy 目标（比如前端宿主端口又变了）时才需要重新执行：
  `docker exec multica-tailscale tailscale --socket=/tmp/tailscaled.sock serve --bg --https=443 http://host.docker.internal:<前端宿主端口>`
- Funnel 需要 tailnet 策略允许（当前已开）。状态卷若丢失，要重新把容器登录进 tailnet（浏览器授权或 AUTHKEY），再执行上面的 serve 并 `funnel on`。
- 改了前端宿主端口必须同步做三件事：重指 serve 目标、更新本文档 §1 和 §5。

## 2. 固定口令（说这些词，我就执行对应流程）

| 口令 | 含义 |
| --- | --- |
| **本地启动** | 只启动/恢复本机开发环境（3100/18080），不碰生产 |
| **本地验证** | 跑 typecheck/test（lint 有已知欠账不阻塞），后端改动加跑 go test |
| **推送代码** | 提交并推送到你的 fork（origin），不碰 Docker |
| **发布上线** | 从当前源码构建镜像并切换生产容器（我会先复述清单再动手） |
| **回滚线上** | 生产切回上一个锚点镜像（我会先确认再动手） |
| **关闭本地服务** | 停 3100/18080 的 dev 进程，生产不受影响 |

## 3. 日常开发 SOP（本地启动 → 改代码 → 验证 → 提交）

```bash
cd /Users/mac/Documents/vibcoding/multica
git status --short                    # ① 看改动；当前分支应跟踪 origin/local-baseline-20261015
git checkout -b feat/xxx              # ② 新功能开新分支（命名：feat/ fix/ chore/）

# ③ 启动本地开发（生产栈已占宿主端口 13017/18097，dev 用 3100/18080，互不影响）
cd server && PORT=18080 \
  DATABASE_URL='postgres://multica:multica@localhost:5433/multica_dev?sslmode=disable' \
  JWT_SECRET=local-dev-only-secret-change-me \
  FRONTEND_ORIGIN=http://localhost:3100 CORS_ALLOWED_ORIGINS=http://localhost:3100 \
  go run ./cmd/server                 # 后端（新终端，或让我代跑）
FRONTEND_PORT=3100 NEXT_PUBLIC_API_URL=http://localhost:18080 pnpm dev:web   # 前端（热更新）

# ④ 打开 http://localhost:3100 注册新账号（dev 库是空的；
#    邮箱验证码不会发出，打印在后端日志里——问我"验证码"即可）
```

改代码的生效方式：

| 改动 | 生效方式 |
| --- | --- |
| 前端（apps/web、packages/*） | 热更新，浏览器自动刷新 |
| 后端（server/**） | 重启 `go run ./cmd/server`（对我说"重启本地后端"） |
| 数据库迁移（server/migrations） | 对 **dev 库** 跑 `DATABASE_URL='...@localhost:5433/multica_dev?...' go run ./cmd/migrate up` 验证；注意 AGENTS.md：并发索引必须单文件迁移 |
| 环境变量（后端运行时） | 改启动命令的 env 后重启 |
| 依赖 | `pnpm install` / `go mod download` |

验证与提交（**本地验证**）：

```bash
pnpm typecheck && pnpm test           # 前端/共享包
cd server && go test ./...            # 后端（改动涉及时）
git add -A && git commit -m "feat: 一句话说明"   # 原子提交
```

## 4. 推送代码 SOP（代码往哪传）

```bash
git push                              # 新功能分支（feat/xxx）：直接 push
# 当前主工作分支（本地名 codex/... ≠ 远端名 local-baseline-20261015）用：
git push origin HEAD:local-baseline-20261015
# 网络抖动失败就重试；成功后 git ls-remote origin | grep <分支名> 确认
```

规则：

- 一律推 `origin`（fork）；`upstream`（官方 multica-ai）只读，永不推送。
- fork 上的 `codex/browser-chat-local-sync` 旧分支（10-07 全量快照）**不要动、不要 force push**。
- 分支含义：`local-baseline-20261015` = 基线+全树导入快照（2026-10-15）；日常功能用 `feat/xxx` 新分支，合并完再删。

## 5. 发布上线 SOP（最重要，必须显式说"发布上线"）

前置检查（缺一不可，我会逐项确认）：

- [ ] `git status` 干净，当前 SHA 已推送（记下 `git rev-parse HEAD`）
- [ ] `pnpm typecheck && pnpm test` 通过；后端改动 `go test ./...` 通过
- [ ] 迁移已在 dev 库验证
- [ ] 数据库结构变更大：先备份 `docker exec multica-postgres-1 pg_dump -U multica -d multica | gzip > ~/multica-backup-$(date +%F).sql.gz`

执行（在仓库根目录）：

```bash
# ① 构建（约 10 分钟；COMMIT 注入 /health 身份证明）
COMMIT=$(git rev-parse HEAD) docker compose \
  -f docker-compose.selfhost.yml -f docker-compose.selfhost.build.yml build backend frontend

# ② 打不可变锚点 tag（回滚靠它，命名：日期-短SHA）
VER=$(date +%Y%m%d)-$(git rev-parse --short HEAD)
docker tag multica-backend:dev multica-backend:$VER
docker tag multica-web:dev multica-web:$VER

# ③ 切换（只重建 backend/frontend；postgres 与数据卷不动；中断约 30-60 秒）
docker compose -f docker-compose.selfhost.yml -f docker-compose.selfhost.build.yml up -d
```

快捷方式：`make selfhost-build` = ① + ③ 一条龙（锚点 tag 仍需自己按 ② 补打）。2026-10-16 的发布即用此方式。

发布后验证（全过才算成功）：

```bash
curl -s http://localhost:18097/health          # commit 必须等于本次构建的 SHA
curl -s -o /dev/null -w '%{http_code}' http://localhost:13017/          # 200
curl -s -o /dev/null -w '%{http_code}' https://multica-server.tail709cfb.ts.net/   # 200
docker compose -f docker-compose.selfhost.yml ps                        # 生产服务全 Up
docker logs --tail 50 multica-backend-1     # 无 migrate 失败 / panic
# 最后：浏览器打开 Tailscale 地址，登录 → 开个聊天 → 发消息 → 刷新确认
```

> ⚠️ 2026-10-16 现状：运行中的栈来自 `/tmp/multica-workbench-route-G7mLOS/` 的 compose 副本，宿主端口 13017/18097（见 §1）；直接对仓库 compose 跑 `up -d` 会按 3000/8080 重建、弄断 Funnel 指向（§1a）。发布前先确认 compose 来源与端口映射，或先把两边对齐（§10 欠账）。

发布记录（每次发布必须留痕，记到 `docs/releases/` 或直接告诉我存档）：

| 字段 | 示例 |
| --- | --- |
| 版本 / 镜像锚点 | `20261015-eeb9459` |
| commit SHA | `eeb94599d2d0…` |
| 日期 / 发布人 | 2026-10-15 / 你 |
| 测试 | typecheck ✓ test ✓ |
| 迁移 | 无 / 清单+dev 库验证结果 |
| 回滚锚点 | 上一个 `multica-*:YYYYMMDD-xxxxxxx` 或 `rollback-v061` |

### 5a. 安装持久运行的自托管更新 worker（macOS）

在要安装的源码版本根目录运行 `make selfhost-updater-install`。需要本机已有 Python 3、Go、Git、Docker Compose 和 pnpm；安装时的 `PATH` 会写入 LaunchAgent，需能找到后续构建和验证所需的工具。此目标跳过仓库 `.env` / `.env.worktree` 的读取，不能与其他 Make 目标合并执行。

先在源码仓库之外准备生产环境文件（例如 `~/.multica/self-update/production.env`），权限必须为 `600`，路径及其父目录不能是符号链接。使用经过核对的现有生产配置，不重新生成密钥。安装器只记录该文件的路径，不执行其内容。

必填环境变量：

| 变量 | 含义 / 示例 |
| --- | --- |
| `SELFHOST_PRODUCTION_ENV_FILE` | 受保护生产环境文件的绝对路径；不能位于任何带 `.git` 文件或目录的源码仓库内 |
| `SELFHOST_SERVER_URL` | 更新控制接口的固定后端地址，例如 `http://localhost:18097` |
| `SELFHOST_PRODUCTION_DATABASE` | 经过核对的实际生产数据库名，例如 `multica` |
| `SELFHOST_CUSTOM_REMOTE` | 自有 fork 的 HTTPS 仓库地址，不能内嵌账号或令牌 |
| `SELFHOST_CUSTOM_BRANCH` | 自有 fork 的固定发布分支，例如 `codex/self-host-update-release` |
| `SELFHOST_WORKER_TOKEN` | 与后端 `MULTICA_SELF_UPDATE_WORKER_TOKEN` 一致的私密令牌，通过安全方式预先导出；不要在终端打印或写入仓库 |

可选变量：`SELFHOST_EXTERNAL_RESOLVE_IP` 是公网健康检查的目标 IP，仅在 Funnel 域名受本机 VPN / DNS 影响时使用；仍按原域名校验证书。`SELFHOST_UPDATER_DRY_RUN=1` 写入配置和 plist、初始化本地裸仓库，但跳过编译和服务加载；默认 `0` 执行安装。

以下命令假定已安全导出 `SELFHOST_WORKER_TOKEN`：

```bash
SELFHOST_PRODUCTION_ENV_FILE="$HOME/.multica/self-update/production.env" \
SELFHOST_SERVER_URL=http://localhost:18097 \
SELFHOST_PRODUCTION_DATABASE=multica \
SELFHOST_CUSTOM_REMOTE=https://github.com/xuhao199747-code/multica.git \
SELFHOST_CUSTOM_BRANCH=codex/self-host-update-release \
make selfhost-updater-install

launchctl print gui/$(id -u)/com.multica.self-update
```

安装器从当前源码的 `server/` Go 模块编译 `cmd/selfhost-updater`，安装到 `~/.multica/self-update/bin/selfhost-updater`，并注册 `~/Library/LaunchAgents/com.multica.self-update.plist`。登录后由 launchd 启动并保持运行；重装会重新加载服务，保留现有仓库、工作区和执行记录。

`~/.multica/self-update/{bin,repo.git,state,worktrees}` 权限为 `700`，`worker.json` 权限为 `600`。日志位于 `state/worker.stdout.log` 和 `state/worker.stderr.log`。LaunchAgent 固定使用持久二进制和配置文件路径，不依赖 `/tmp`。安装成功只表示服务已注册，仍需确认更新状态接口显示 worker 在线，并在实际更新后验证本地后端 `/readyz` 和公网 Funnel 根地址 `/` 均返回 `200`。

## 6. 本地工作区链路（浏览器聊天 ↔ 本机文件夹）

> 记录的是 2026-10-16 时点的当前形态。这条链还在演进，细节以代码为准；有功能改动时同步更新本节。

浏览器里创建聊天项目时，项目本身只写数据库；本机文件夹由下面这条链补齐：

1. **sync（launchd 托管）**：`~/Library/LaunchAgents/com.multica.local-sync.plist` → 每 15 秒轮询 API，在同步根 `~/Documents/multica/<项目名>` 建目录（内含 `.multica-project.json` 元数据），并给项目绑定 `local_directory` 资源（`in_place`、指向本机 daemon）。聊天记录（`conversation.md` + `metadata.json`）也镜像到同步根下的项目目录里。
2. **daemon（本机进程）**：领取项目内的聊天任务时，若任务携带了匹配本机 daemon 的 `local_directory` 资源，就以该目录为 agent 的工作目录；否则按普通任务跑。

各组件的启动 / 重启方式：

| 组件 | 二进制 | 启动 | 重启 / 升级 |
| --- | --- | --- | --- |
| sync | `server/bin/multica`（仓库 `go build` 产物） | launchd 开机自启 | 改代码后：`cd server && go build -o bin/multica ./cmd/multica`，再 `launchctl kickstart -k gui/$(id -u)/com.multica.local-sync` |
| daemon | `~/.local/bin/multica`（手工替换） | `multica daemon start --foreground`（终端常驻） | 替换二进制后重启进程生效；`multica daemon status` 查状态 |

替换 `~/.local/bin/multica` 注意：直接 `cp` 覆盖会被 Gatekeeper 拦截（`Killed: 9`，`com.apple.provenance` 与文件内容不匹配），要 `rm` 旧文件后 `cp` 新文件（新 inode），并实际跑一条命令验证（如 `multica version`）再指望 daemon 重启拉起。
| 生产栈 | Docker 镜像 | 见 §5 | 见 §5 |

绑定电脑里**已有**的文件夹（当前支持两种方式，都不需要桌面版）：

```bash
# 方式一：CLI（项目已存在时绑定）
multica project resource add <project-id> --type local_directory \
  --local-path /绝对/路径/到/文件夹 --daemon-id <本机daemon id> --execution-mode in_place
# 本机 daemon id 用 `multica daemon status` 查看

# 方式二：聊天页"新建项目"对话框里直接填文件夹绝对路径（网页端可用；
#         桌面版额外有文件夹选择按钮，填路径两者都支持）
```

当前规则：资源路径在**同步根之外**时，sync 完全不动它——不建目录、不改名、不清扫、也不把资源路径"纠正"回同步根（2026-10-16 之前的旧版会改回，已修）；聊天记录仍照常镜像到同步根。同步根之内的项目维持原样（建目录、随项目改名、删除进废纸篓）。

边界：网页端不显示"选文件夹"入口（浏览器拿不到本地路径）；daemon 离线时相关聊天任务不带本地目录运行。

## 7. 回滚线上 SOP（说"回滚线上"）

只对**上一个已验证的锚点**回滚，禁止凭 `latest` 猜：

```bash
docker tag multica-backend:<锚点tag> multica-backend:dev
docker tag multica-web:<锚点tag> multica-web:dev
docker compose -f docker-compose.selfhost.yml -f docker-compose.selfhost.build.yml \
  up -d --force-recreate backend frontend
curl -s http://localhost:18097/health    # 确认 commit 回到锚点
```

已知锚点（2026-10-16 时点）：`rollback-v061`（旧 v0.6.1）、`20261015-eeb9459`、`20261016-fca801f`、`20261016-37f6f92`、`20261016-9949541`、`20261016-1e2268d`（当前线上）。

⚠️ **代码回滚 ≠ 数据库回滚**：新版本跑过的迁移不会自动撤销。回滚后若后端报错（表结构不匹配），用最近的 pg_dump 备份恢复数据库，再重启后端。

## 8. 数据保护红线（任何情况下不做）

- `docker compose down -v`、`make destroy`、删任何 volume（`multica_pgdata`、`multica_backend_uploads`、`multica_dev_pgdata`、`multica-tailscale-state`）
- 删/重建 `multica-postgres-1` 容器以外的任何生产容器前不确认
- `git push --force`（除我自己新建的分支外）、`git reset --hard`、`git clean -fdx`
- 向 `upstream`（multica-ai/multica）推送任何东西
- 重新生成 `.env` 里的 `JWT_SECRET` / `POSTGRES_PASSWORD` / `MULTICA_VCS_SECRET_KEY`（会导致全员掉登录、集成失效）
- 动 `docker-*` 开头的那批 Dify 容器（与本项目无关）

## 9. 当前资产清单

- 备份：`~/multica-backup-20261015/`（源码 + .git 打包 + 生产库 pg_dump）；发布大版本前重新做 pg_dump
- 生产镜像锚点：`multica-backend:rollback-v061`、`multica-web:rollback-v061`、`multica-{backend,web}:20261015-eeb9459`、`multica-{backend,web}:20261016-fca801f`、`multica-{backend,web}:20261016-37f6f92`、`multica-{backend,web}:20261016-9949541`、`multica-{backend,web}:20261016-1e2268d`（当前线上）
- 生产卷：`multica_pgdata`（数据库）、`multica_backend_uploads`（上传文件）
- dev 栈：`multica-dev-postgres`（5433，可随时 `docker stop multica-dev-postgres` 停掉，不影响任何线上）

## 10. 已知欠账（不紧急）

1. `packages/views` 仍有少量中文字面量未走 i18n（lint 报错，不影响构建运行）——chat-page 的一批已在 2026-10-16 顺手修掉，改到其他文件时同理顺手修。
2. Electron 桌面版未安装（本机已验证可构建，`apps/desktop/dist/` 有 2026-10-16 的 dmg；本地工作区链路目前不需要它）——要用桌面客户端时再装。
3. 镜像只存本机，未推 GHCR——走 GitHub Actions 自动发布时再配（需要 GHCR 权限）。
4. Docker Desktop（containerd 镜像存储）在 2026-10-16 发生过一次引擎重启、`docker images` 瞬时不显示自定义 tag——锚点 tag 若"丢失"，先 `docker ps` 确认容器在跑，再按镜像 ID 重打即可（数据未丢）。
5. 生产栈端口（13017/18097/5432）与仓库 compose 默认（3000/8080/不发布）不一致，栈由 `/tmp/multica-workbench-route-G7mLOS/` 的副本托管——下次发布前先把 compose 来源与端口对齐，否则 `up -d` 会重建出 3000/8080 的容器、Funnel 入口断掉（§5 警告、§1a）。postgres 重启策略是 `no`，机器重启后要手动 `docker start multica-postgres-1`。

## 11. 必须我确认后才执行的动作

`git push`（首次连通新远端时）、一切 `docker build/up/stop/rm/tag`、创建/修改 `.env`、安装系统级软件、对生产库应用迁移、`docker compose down`（任何形式）、回滚、暴露新端口到 Tailscale。
