# Palworld Playtime Guard

Palworld 防沉迷 sidecar。它通过 REST API 轮询在线玩家，按固定的每日或每周周期累计在线时长，在到达提醒阈值时发送全服公告，并将超限玩家踢出服务器。

## 特性

- 全服默认规则，以及按 Palworld `userId` 设置的覆盖和豁免。
- 可配置时区、每日或每周重置时间。
- SQLite 持久化用时、提醒和执法审计，重启不会丢失当前周期累计值。
- API 或数据库故障时停止计时和执法，避免误判。
- 配置规则和消息模板可热重载；连接、监听、存储和重试设置变更需要重启。
- Docker 内网只读 API，为独立 WebUI 提供稳定的 `/api/v1` 契约。
- 独立 React/Vite WebUI，可作为单独容器部署，并在容器内反代到 sidecar API。

## 初次运行

在父级 Palworld 栈目录执行：

```bash
cp playtime-guard/config.example.yaml playtime-guard/config.yaml
mkdir -p playtime-guard/data
docker compose up -d --build palworld-playtime-guard
docker compose ps palworld-playtime-guard
docker compose logs --tail=100 palworld-playtime-guard
```

示例配置中的 `policy.default.enabled` 为 `false`。首次启动只验证连接和 API，不累计受限用时、不公告、也不 kick。

Palworld REST API 密码从父栈 `.env.palworld` 的 `ADMIN_PASSWORD` 注入，不应写入 `config.yaml`。

策略以 SQLite 为准。`config.yaml` 中的 `policy` 只在数据库还没有策略文档时作为首次初始化 seed；之后 WebUI/API 写入的数据库策略是唯一权威源，修改 `config.yaml` 不会覆盖已有策略。

如果在线时长没有增长，先看容器 JSON 日志里的 `player usage unchanged` / `player usage updated`：

- `skip_reason=policy_disabled`：当前规则未启用，不会计时。
- `skip_reason=first_observation`：玩家本次连续在线的第一轮观察，不追溯计时；下一轮才开始累计。
- `skip_reason=gap_exceeded`：两次成功轮询间隔超过 `server.max_observation_gap`，本段时间被丢弃以避免误算。
- `added_ms > 0`：本轮已经写入累计时长。

## 规则配置

```yaml
policy:
  timezone: Asia/Shanghai
  default:
    enabled: true
    strategy: fixed_window
    period: daily
    reset_at: "04:00"
    limit: 2h
    warning_before: [30m, 10m, 5m, 1m]
  overrides:
    steam_123456:
      limit: 4h
    steam_789012:
      exempt: true
```

每周规则使用 `period: weekly`，并增加 `reset_weekday: Monday`。覆盖规则未填写的字段继承默认规则。玩家名称只用于显示，累计和覆盖始终以 REST API 返回的 `userId` 为准。

`strategy` 支持三种模式：

- `fixed_window`：固定每日或每周窗口，字段为 `period`、`reset_at`、`reset_weekday`、`limit`。
- `cooldown`：CD 制，字段为 `cooldown_every` 和 `cooldown_rest`。例如玩 `2h` 后必须休息 `30m`。
- `credit`：额度恢复制，字段为 `credit_recover_every`、`credit_recover_amount`、`credit_max`。例如每离线 `1h` 恢复 `30m`，最多存 `3h`。

CD 示例：

```yaml
policy:
  timezone: Asia/Shanghai
  default:
    enabled: true
    strategy: cooldown
    cooldown_every: 2h
    cooldown_rest: 30m
    warning_before: [30m, 10m, 5m, 1m]
```

credit 示例：

```yaml
policy:
  timezone: Asia/Shanghai
  default:
    enabled: true
    strategy: credit
    credit_recover_every: 1h
    credit_recover_amount: 30m
    credit_max: 3h
    warning_before: [30m, 10m, 5m, 1m]
```

## 只读 API

默认无需登录即可读取状态。服务仅在 Docker 网络中暴露 `8080`，没有宿主机端口映射：

- `GET /healthz`：进程和 SQLite 状态。
- `GET /readyz`：配置有效且至少成功轮询一次。
- `GET /api/v1/status`：轮询、在线人数和配置重载状态。
- `GET /api/v1/players`：在线玩家当前周期用时和剩余额度。
- `GET /api/v1/players/{userId}`：已知玩家当前状态。
- `GET /api/v1/policies`：脱敏后的默认规则和用户覆盖。

## 管理操作

写操作默认关闭。配置以下环境变量后，WebUI 会显示管理员登录入口；登录成功后通过 HttpOnly session cookie 解锁写操作：

```yaml
http:
  listen: 0.0.0.0:8080
  admin_username_env: PALREST_ADMIN_USERNAME
  admin_password_env: PALREST_ADMIN_PASSWORD
```

```env
PALREST_ADMIN_USERNAME=admin
PALREST_ADMIN_PASSWORD=change-me
```

已支持的管理接口：

- `POST /api/v1/admin/login`：账号密码登录。
- `POST /api/v1/admin/logout`：退出登录。
- `GET /api/v1/admin/session`：查看管理能力和当前登录状态。
- `PUT /api/v1/policies`：保存整份数据库策略。
- `POST /api/v1/players/{userId}/reset`：重置指定玩家的 usage、warning、enforcement 和策略状态，并清理内存连续状态。

Passkey 还未启用；`/api/v1/admin/session` 会返回 `passkey: false`。后续可在同一套管理员 session 上接 WebAuthn credential 存储。

可以从同一 Docker 网络中的容器查询，例如：

```bash
docker run --rm --network homelab-v2 curlimages/curl:latest \
  http://palworld-playtime-guard:8080/api/v1/status
```

## WebUI

WebUI 位于 `webui/`，是独立的 React + Vite 前端。它默认同源请求 `/api/v1`、`/healthz` 和 `/readyz`；本地开发时 Vite 会把这些请求代理到 Go sidecar。

未登录时 WebUI 只读。管理员登录后可以重置玩家频控，并通过策略 JSON 编辑器保存数据库策略。

本地开发：

```bash
cd webui
npm install
PALREST_API_TARGET=http://127.0.0.1:8080 npm run dev
```

容器部署：

```bash
docker build -t palrest-webui:test webui
docker run --rm -p 127.0.0.1:18081:8080 \
  -e PALREST_API_UPSTREAM=http://palworld-playtime-guard:8080 \
  palrest-webui:test
```

`PALREST_API_UPSTREAM` 是 WebUI 容器内 Caddy 访问 Go sidecar 的地址。默认值为 `http://palworld-playtime-guard:8080`，适合与 sidecar 位于同一 Docker 网络的部署。通常不需要设置 `PALREST_API_BASE_URL`；保持为空时浏览器只访问 WebUI 容器，由 Caddy 反代 API 请求。

## 开发验证

```bash
go test -race ./...
go vet ./...
docker build -t palworld-playtime-guard:test .
cd webui && npm run build
```
