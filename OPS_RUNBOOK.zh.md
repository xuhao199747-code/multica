# Multica 执行规范（运维操作手册）

> 本文档是本仓库唯一的操作事实来源。命令都经过 2026-10-15 实际验证。
> 修改流程、地址、端口后必须同步更新本文档。

## 1. 环境地图（东西都在哪）

| 环境 | 位置 | 地址 | 跑的代码 | 数据 |
| --- | --- | --- | --- | --- |
| 生产栈（Docker） | 本机 Docker，compose 托管 | `http://localhost:3000`（前端）/ `:8080`（后端，仅 127.0.0.1） | 镜像 `multica-{web,backend}:dev`（从源码构建） | 卷 `multica_pgdata`、`multica_backend_uploads` |
| 对外入口 | 宿主机 Tailscale serve → 前端容器 | `https://multica-server.tail709cfb.ts.net` | 同生产栈 | 同生产栈 |
| 本地开发后端 | 本机进程（`go run`） | `http://localhost:18080` | 当前工作区源码 | 库 `multica_dev_postgres`（127.0.0.1:**5433**，卷 `multica_dev_pgdata`，与生产隔离） |
| 本地开发前端 | 本机进程（`pnpm dev:web`，热更新） | `http://localhost:3100` | 当前工作区源码，连 18080 | 无 |
| Git 远端 | GitHub | `origin` = `xuhao199747-code/multica`（你的 fork，可推送）；`upstream` = `multica-ai/multica`（官方，只读） | — | — |

关键事实：

- 生产数据库容器**没有发布宿主端口**（不能 localhost:5432 连接）；生产库只能通过备份/容器内访问。
- `multica-tailscale` 容器是坏的（无 AUTHKEY），**不要修不要删**——对外入口由宿主机 Tailscale 提供。
- `docker compose` 必须带 `-f docker-compose.selfhost.yml`（必要时加 `-f docker-compose.selfhost.build.yml`）；`.env` 必须存在（密钥已与线上一致，绝不要重新随机生成，否则全员掉登录）。
- `/health` 返回的 `commit` 字段 = 生产后端的真实版本身份证明（构建时注入）。
- 本机网络注意：github.com 时通时断（推送需重试）；brew 不可用（Go 手工装在 `/opt/homebrew/opt/go`）；api.github.com 稳定。

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

# ③ 启动本地开发（生产栈已占 3000/8080，dev 用 3100/18080，互不影响）
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
curl -s http://localhost:8080/health          # commit 必须等于本次构建的 SHA
curl -s -o /dev/null -w '%{http_code}' http://localhost:3000/          # 200
curl -s -o /dev/null -w '%{http_code}' https://multica-server.tail709cfb.ts.net/   # 200
docker compose -f docker-compose.selfhost.yml ps                        # 3 个服务全 Up
docker logs --tail 50 multica-backend-1     # 无 migrate 失败 / panic
# 最后：浏览器打开 Tailscale 地址，登录 → 开个聊天 → 发消息 → 刷新确认
```

发布记录（每次发布必须留痕，记到 `docs/releases/` 或直接告诉我存档）：

| 字段 | 示例 |
| --- | --- |
| 版本 / 镜像锚点 | `20261015-eeb9459` |
| commit SHA | `eeb94599d2d0…` |
| 日期 / 发布人 | 2026-10-15 / 你 |
| 测试 | typecheck ✓ test ✓ |
| 迁移 | 无 / 清单+dev 库验证结果 |
| 回滚锚点 | 上一个 `multica-*:YYYYMMDD-xxxxxxx` 或 `rollback-v061` |

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
curl -s http://localhost:8080/health    # 确认 commit 回到锚点
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

## 11. 必须我确认后才执行的动作

`git push`（首次连通新远端时）、一切 `docker build/up/stop/rm/tag`、创建/修改 `.env`、安装系统级软件、对生产库应用迁移、`docker compose down`（任何形式）、回滚、暴露新端口到 Tailscale。
