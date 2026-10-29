# Multica 当前运行手册

> 最后核对：2026-10-28。本文只记录这台机器当前实际运行的自托管栈。
> 不创建 Multica 任务，不启用自动更新 worker，不删除数据库、上传文件或 Docker 卷。

## 入口与运行镜像

| 用途 | 地址 / 名称 | 当前状态 |
| --- | --- | --- |
| 本机网页 | `http://127.0.0.1:13017` | 前端入口 |
| 本机后端健康检查 | `http://127.0.0.1:18097/health` | 后端入口 |
| 对外网页 | `https://multica-server.tail709cfb.ts.net` | Tailscale Funnel |
| 前端镜像 | `multica-web:dev` | 从当前仓库构建 |
| 后端镜像 | `multica-backend:dev` | 从当前仓库构建 |
| 数据库 | Compose 卷 `multica_pgdata` | 数据库名 `multica` |
| 上传文件 | Compose 卷 `multica_backend_uploads` | 由后端使用 |

当前只保留上述两张 Multica 应用镜像。PostgreSQL 和 Tailscale 是运行依赖，不是 Multica 应用镜像。

## 日常启动

在仓库根目录运行：

```bash
cd /Users/mac/Documents/vibcoding/multica
docker compose \
  -f docker-compose.selfhost.yml \
  -f docker-compose.selfhost.build.yml \
  up -d --build
```

必须同时使用两份 Compose 文件。第二份文件会让服务使用当前仓库构建出的
`multica-web:dev` 和 `multica-backend:dev`；只使用第一份文件会按 `.env` 中的
`current` 标签寻找镜像，不是当前这套本地构建镜像。

## 检查是否正常

```bash
docker ps --format 'table {{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}'
curl -fsS http://127.0.0.1:18097/health
curl -sS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:13017/
curl -sS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:13017/api/me
curl -sS -o /dev/null -w '%{http_code}\n' https://multica-server.tail709cfb.ts.net/
```

正常结果：

- `multica-frontend-1`、`multica-backend-1` 为 `Up`，`multica-postgres-1` 为 `healthy`。
- `/health` 返回 JSON 且 `status` 为 `ok`。
- 网页根路径返回 `200`。
- 未登录时 `/api/me` 返回 `401`，这是预期结果。
- 对外地址返回 `200`。

如果网页显示加载图标不消失，先在浏览器实际打开本机或对外地址确认，再查看日志：

```bash
docker logs --tail 120 multica-frontend-1
docker logs --tail 120 multica-backend-1
```

## 停止与恢复

停止服务但保留聊天、项目、数据库和上传文件：

```bash
cd /Users/mac/Documents/vibcoding/multica
docker compose \
  -f docker-compose.selfhost.yml \
  -f docker-compose.selfhost.build.yml \
  down
```

之后用“日常启动”命令恢复即可。

禁止使用带 `-v` 的 `down`、`make destroy`、删除 `multica_pgdata` 或
`multica_backend_uploads`。这些操作会造成不可逆的数据丢失。

## 更新当前源码并重新部署

先确认代码和测试，再重新构建：

```bash
cd /Users/mac/Documents/vibcoding/multica
git status --short
pnpm typecheck
pnpm test
docker compose \
  -f docker-compose.selfhost.yml \
  -f docker-compose.selfhost.build.yml \
  up -d --build
```

发布是否成功以“检查是否正常”和浏览器中的真实页面为准，不能只看构建成功或 HTTP 根路径。

## Tailscale 对外入口

当前对外访问路径为：

```text
公网浏览器
  -> https://multica-server.tail709cfb.ts.net
  -> multica-tailscale
  -> 192.168.65.254:13017
  -> multica-frontend-1
  -> multica-backend-1
```

查看当前转发配置：

```bash
docker exec multica-tailscale tailscale serve status --json
```

不要删除 `multica-tailscale` 容器或它的状态卷；其中保存了 Tailscale 登录状态和 Funnel 转发配置。

## 配置与边界

- `.env` 中的生产数据库是 `multica`。不要把开发环境数据库配置替换进来。
- `.env` 里的 `MULTICA_IMAGE_TAG=current` 仅供不使用构建覆盖文件时解析镜像；当前实际运行的是构建覆盖文件指定的 `:dev` 镜像。
- 旧的自托管自动更新 worker 已移除，不要安装或恢复它。
- 不操作名称以 `docker-` 开头的其他容器；它们属于另一套服务。
- 修改 `.env` 后，需要重新执行带两份 Compose 文件的 `up -d`；单独 `docker compose restart` 不会重新读取配置。

## 备份数据库

在升级、迁移或大范围修改前，可创建一个可恢复备份：

```bash
cd /Users/mac/Documents/vibcoding/multica
BACKUP_DIR=/Users/mac/multica-backups
mkdir -p "$BACKUP_DIR"
docker compose \
  -f docker-compose.selfhost.yml \
  -f docker-compose.selfhost.build.yml \
  exec -T postgres pg_dump -U multica multica \
  | gzip > "$BACKUP_DIR/multica-backup-$(date +%F-%H%M%S).sql.gz"
```
