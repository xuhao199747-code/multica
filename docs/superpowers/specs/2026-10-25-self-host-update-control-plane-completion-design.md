# 自托管更新控制面收口

## 目标

让自托管管理员能在 Multica Web 侧栏看见**真实**的更新状态，并在明确确认后发起一次受控的官方稳定版更新。页面刷新、Docker 暂不可用和主机登录重启都不能让更新状态或后台执行器静默消失。

这是对 `2026-10-24-custom-self-host-one-click-update-design.md` 已有后端/worker 设计的收口，不改变更新的安全边界，也不把 Desktop 客户端更新与自托管 Docker 更新混为一谈。

## 已确认的断点

- 现有侧栏 `UpdateStatus` 直接读取 GitHub 提交关系，不调用自托管更新 API；“检查更新”只会重新请求 GitHub，不能创建更新任务。
- 自托管 worker 由 LaunchAgent 启动，但曾指向 `/tmp/selfhost-updater`。该路径不是持久安装位置，文件消失后 LaunchAgent 以 `EX_CONFIG` 退出。
- worker 当前在首次控制面不可用时退出。Docker Desktop 或后端短暂未启动时，LaunchAgent 必须能恢复，而不是永久显示为离线。
- 自托管更新后端已经提供状态、创建任务、查询任务、worker 心跳与领取接口；浏览器端尚未使用这些接口。

## 用户交互

侧栏保留一个更新入口，但其语义改为“自托管服务更新”，而不是 Electron/Desktop 更新。

1. 页面加载与手动点击都会读取 `/api/self-update/status`。
2. 状态明确显示为以下之一：
   - 正在检查；
   - `vX.Y.Z` 可更新；
   - 当前已经是已部署版本；
   - 更新进行中，并显示当前阶段；
   - 需要配置；
   - 更新 worker 离线；
   - 无法检查。
3. “检查更新”只刷新状态，绝不开始部署。
4. 当状态为可更新且当前用户拥有后端管理员权限时，显示“更新到 vX.Y.Z”。
5. 点击更新按钮打开确认对话框，说明这是 Docker 服务更新、会创建数据库备份、服务可能短暂中断。确认才会 POST 创建任务。
6. 创建后轮询该任务；任务阶段在侧栏持续展示。终态成功、失败或需要人工决定均保留可读状态，不再自动消失。

云端或旧后端没有自托管更新接口时，侧栏保留现有 GitHub 提交检查，避免影响非自托管安装。

## API 与前端边界

- 在 `packages/core` 定义并以 zod 解析 `SelfUpdateStatus`、`SelfUpdateJob` 和创建任务响应。
- `ApiClient` 负责调用三个 API：读取状态、创建任务、读取任务。视图层只管理刷新节奏、确认弹窗和 UI 展示。
- 对 404 使用旧 GitHub 检查；对其他 API 错误显示“无法检查”，不伪造版本或权限。
- 创建任务使用每次点击生成的 idempotency key；同一待处理点击复用该 key，避免网络重试创建重复任务。

## 后台 worker

- worker 二进制安装到 `~/.multica/self-update/bin/selfhost-updater`，LaunchAgent 只能引用该固定路径。
- worker 配置和状态继续位于 `~/.multica/self-update/`，均为用户私有权限；生产环境文件在该目录中保存，不能从 Git 工作树读取。
- 对临时控制面不可用（Docker Desktop、后端尚未启动、短暂重启）采用有界间隔重试，并维持重启后的心跳。配置错误、私有目录权限错误和不可信输入仍然立即失败，便于管理员处理。
- 当主机 VPN 的系统 DNS 不能解析 Funnel 域名时，worker 只能对已配置的固定公网解析地址重写该一个健康检查连接；TLS 仍使用原 Funnel 主机名，其他请求继续使用系统网络。未配置地址或地址不可信时外部检查必须失败。
- worker 不接收浏览器提供的 shell 命令、仓库、分支、Docker 参数或凭据。

## 真实更新验收

本次只在以下前置条件同时满足时发起真实更新：

1. Docker Desktop 已运行；
2. backend、frontend、postgres、Tailscale Funnel 均已运行；
3. worker 状态为 `ready`；
4. `/api/self-update/status` 显示一个稳定版可更新；
5. worker 的生产环境文件具有正确生产数据库与外部访问 URL。

真实更新以创建 `self_update_job` 为开始证据，而不是 UI 提示。验收记录必须包含：

- 更新任务的完整阶段和终态；
- worker 日志与本机状态日志；
- PostgreSQL 备份文件；
- 被部署的 backend/frontend 镜像 digest 与提交 SHA；
- 后端 `/readyz`、实际 Compose 端口页面和外部 Funnel URL 的 HTTP 200。

任一项失败都保留失败证据，不把任务创建、构建成功或单个 `/health` 200 报作更新成功。

## 非目标

- 不更新 macOS Desktop 应用、Mobile 应用或本机 agent CLI。
- 不自动开始更新；管理员确认是唯一的部署授权。
- 不删除 Docker 卷、数据库、Tailscale 容器或本地同步文件。
- 不把当前主工作树未提交改动带入候选更新。

## 验证

- 前端：状态解析、404 回退、可更新按钮、确认后创建任务、活动任务轮询和失败状态都有组件/逻辑测试。
- worker：临时控制面错误后重试；配置错误仍立即退出；LaunchAgent 使用持久二进制路径。
- 后端：现有 self-update handler 与 worker 测试继续通过。
- 运行时：启动 Docker 后，用管理员身份查询 status；从 UI 发起一次真实任务；按“真实更新验收”中的所有证据收尾。
