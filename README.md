# Palworld Playtime Guard

Palworld 防沉迷 sidecar。它通过 REST API 轮询在线玩家，按固定的每日或每周周期累计在线时长，在到达提醒阈值时发送全服公告，并将超限玩家踢出服务器。

## 特性

- 全服默认规则，以及按 Palworld `userId` 设置的覆盖和豁免。
- 可配置时区、每日或每周重置时间。
- SQLite 持久化用时、提醒和执法审计，重启不会丢失当前周期累计值。
- API 或数据库故障时停止计时和执法，避免误判。
- 消息模板可热重载；Policy 在首次初始化后完全由 SQLite 管理，连接、监听、存储和重试设置变更需要重启。
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

## 首次策略默认值

`config.yaml` 中的 `policy` 是可选的首次初始化默认值，可以只填写需要覆盖代码默认值的字段。数据库创建策略文档后，这一段即使被修改或写成无效时区，也不会影响当前策略；玩家覆盖不能在 YAML 中配置。

```yaml
policy:
  timezone: Asia/Shanghai
  default:
    enabled: true
    limit: 2h
```

未配置 YAML Policy 时，代码默认使用 `Asia/Shanghai`、每日 `04:00` 重置、固定窗口 `2h`、提醒阈值 `30m/10m/5m/1m`，并保持策略禁用。首次初始化后请在 WebUI 的 Policy management 中选择完整选项。玩家名称只用于显示，累计和覆盖始终以 REST API 返回的 `userId` 为准。

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
- `GET /api/v1/analytics/summary?ranking=today|week`：当前在线快照、今日汇总和玩家排行；`ranking` 默认是 `today`。
- `GET /api/v1/analytics/activity?range=7d|30d&user_id=...&include_concurrency=true|false`：并发和可选的单玩家逐日活动；`range` 默认是 `7d`，`user_id` 默认不查询玩家，`include_concurrency` 默认是 `true`。

以上 Analytics 接口与其他只读接口使用相同的读取权限，不要求管理员 session。

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

未登录时 WebUI 只读。管理员按钮会打开标准用户名/密码登录框，可由常见密码管理器识别。登录后可以重置玩家状态，并通过可视化 Policy management 编辑全局规则、三种策略的全部选项，以及按玩家继承或自定义的覆盖；新增覆盖支持搜索已知玩家或手动输入 User ID。

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

## 玩家活动分析

WebUI 顶部的 Overview / Analytics 可在运行状态和玩家活动分析之间切换。Analytics 显示当前在线人数及其 `as_of` 观测时间、今日累计观测玩家时长、今日并发峰值和活跃玩家数，并提供今日/本周排行、最近 7/30 天服务器并发曲线，以及所选玩家按本地日期汇总的每日活动。排行中的在线标记来自当前在线快照；本周按当前策略时区的周一到周日计算。

活动采集覆盖 REST API 成功返回的所有玩家，不受全局规则、玩家覆盖、豁免或策略是否启用影响。采集从部署此版本后开始，不回填历史数据。首次成功观测只建立基线；两次成功观测间隔超过 `server.max_observation_gap` 时，该区间会被丢弃。轮询/API/存储失败以及过长间隔代表“未知”，不能解释为在线人数为零或玩家活动为零。

并发数据按 5 分钟桶返回。`average_count` 是已观测时段按时间加权的平均在线人数，`max_count` 是桶内已观测峰值，`coverage` 表示该桶被成功覆盖的时间比例；没有有效观测的桶会保留为缺口（平均值和峰值为 `null`、覆盖率为 `0`），不会补零。单玩家查询会在存在该玩家活动数据时返回范围内逐日序列，未观测到活动的日期为零；未知玩家返回 404。

`GET /api/v1/analytics/summary` 返回 `online_count`、可空的 `as_of`、`today_observed_ms`、`peak_count`、可空的 `peak_at`、`active_players`、`ranking_period` 和 `ranking`。`GET /api/v1/analytics/activity` 返回 `range`、`timezone`、左闭右开的本地日期边界 `start`/`end`、`concurrency`，以及可空的 `player`。查询参数只接受上面列出的值；无效值返回 400。

Analytics 原始会话、并发桶和逐日统计以 90 天为清理截止目标。清理由成功观测触发、最多每天一次，每次对每张表最多删除 500 行，避免无界清理阻塞；过期数据量较大时清理可能滞后，超过 90 天的记录会暂时保留。当前 Policy 的 `timezone` 同时决定新的逐日归属和 API 日历边界；运行时修改时区只影响后续观测，跨越修改时刻的单个区间会被丢弃，既有数据不会重新分桶。

图表刷新时，新绘图区会在约 550 ms 内淡入并轻微水平平移，不会在新旧路径形状之间插值；系统启用“减少动态效果”时会停用该动画。

SQLite schema migration 当前最高版本为 v8；启动时会自动按顺序升级已有数据库。

## 开发验证

```bash
go test -race ./...
go vet ./...
docker build -t palworld-playtime-guard:test .
cd webui && npm test && npm run build
```
