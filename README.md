# Palworld Playtime Guard

Palworld 防沉迷 sidecar。它通过 REST API 轮询在线玩家，按固定的每日或每周周期累计在线时长，在到达提醒阈值时发送全服公告，并将超限玩家踢出服务器。

## 特性

- 全服默认规则，以及按 Palworld `userId` 设置的覆盖和豁免。
- 可配置时区、每日或每周重置时间。
- SQLite 持久化用时、提醒和执法审计，重启不会丢失当前周期累计值。
- API 或数据库故障时停止计时和执法，避免误判。
- 消息模板可热重载；Policy 在首次初始化后完全由 SQLite 管理。连接、监听、存储、重试和 `observation` 设置变更需要重启。
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

## Phase 1 数据采集与故障语义

每次关键轮询读取 Palworld REST `/players` 的 `name`、`accountName`、`playerId`、`userId`、`ip`、`ping`、`location_x`、`location_y`、`level` 和 `building_count`。独立的可选采样器在每轮读取 `/metrics` 的 `serverfps`、`currentplayernum`、`serverframetime`、`maxplayernum`、`uptime`、`basecampnum`、`days`，并按 `observation.server_document_interval` 读取 `/info` 的 `version`、`servername`、`description`、`worldguid` 和完整 `/settings` JSON。

玩家列表、Analytics、业务时间线和防沉迷执法构成关键路径；关键读取或写入失败会中断本轮连续性，不计未知区间，也不执法。服务器 metrics/info/settings 是边界超时的可选路径：三类请求彼此独立，任意一类读取或持久化失败只记录该数据流缺口，不会把缺口写成零，不会阻塞玩家关键路径，也不会阻止另外两类提交。

Phase 1 使用同一个 correlation ID 把一次玩家观察写成统一业务时间线：玩家加入、离开和已知属性变化是事件；坐标按最小移动距离或最大采样间隔稀疏采样并形成连续 trajectory segment；IP、ping、level 和 building count 是只供管理员读取的稀疏 private sample。启动后的第一轮、轮询失败、持久化失败、超过 `server.max_observation_gap` 的区间以及进程停止期间都表示“未知”，不能推断为离线、零并发或零活动。

当前只实现 REST 观察路径。Save Worker、存档/地图解析与历史地图回放属于后续阶段；本版本不会读取 Palworld save 文件。VictoriaMetrics 是可选的未来长期导出方案，本版本没有向 VictoriaMetrics 发出任何指标或事件。

## 公开只读 API 与隐私

默认无需登录即可读取运行和防沉迷状态。服务建议只在 Docker 网络中暴露 `8080`：

- `GET /healthz`：进程、SQLite 与最近成功时间。
- `GET /readyz`：是否至少完成一次成功轮询。
- `GET /api/v1/status`：`started_at`、`last_attempt`、`last_success`、`last_error`、`online_count`、`config_version`、`config_reload_error`。
- `GET /api/v1/players` 与 `GET /api/v1/players/{userId}`：玩家身份显示字段、在线状态、解析后的规则、周期边界、用时/余额、提醒和执法状态。
- `GET /api/v1/policies`：默认规则和玩家覆盖，不包含环境变量值或 REST 凭据。
- `GET /api/v1/analytics/summary?ranking=today|week`：在线快照、今日观测时长、峰值、活跃人数和排行。
- `GET /api/v1/analytics/activity?range=7d|30d&user_id=...&include_concurrency=true|false`：并发覆盖和可选玩家逐日活动。

公开接口绝不返回玩家 IP、坐标、ping、private samples、原始 server settings、管理员密码、Palworld REST 密码、Authorization 或 session cookie。Analytics 与其他公开只读接口一样不要求管理员 session。建议不要把 sidecar API 直接暴露到公网。

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
- `GET /api/v1/admin/players/{userId}/timeline?start=...&end=...&limit=...`：左闭右开的玩家事件、轨迹和私有样本；`start`/`end` 必须各出现一次、为 RFC3339、范围不超过 31 天，`limit` 为 1–2000（默认 500）。
- `GET /api/v1/admin/server/metrics?start=...&end=...&limit=...`：同样范围语义的完整服务器 metrics 样本，默认 limit 500。
- `GET /api/v1/admin/server/documents?kind=info|settings&limit=...&cursor=...`：按发生记录分页读取内容寻址的服务器文档；limit 为 1–2000（默认 100），响应的 opaque `next_cursor` 原样 URL 编码后传给下一页。

敏感时间线、metrics 和 document 查询在成功或 not-found 时，把数据读取与管理员 actor、动作、对象、范围、结果、时间的访问审计放在同一个 SQLite 事务中；审计写入失败时不返回敏感数据。查询/事务错误的 fallback 审计使用另一个最多 2 秒、与请求取消分离的有界事务。管理员事件 payload 采用允许列表，未知 schema/event 不透传 payload；settings 文档在响应前递归遮盖 credential/password/token/secret/authorization/cookie 等键。错误响应和日志只返回稳定摘要，不回显底层凭据。

Passkey 还未启用；`/api/v1/admin/session` 会返回 `passkey: false`。后续可在同一套管理员 session 上接 WebAuthn credential 存储。

可以从同一 Docker 网络中的容器查询。以下示例需要 `curl` 和 `jq`，让 curl 负责 RFC3339 `+08:00` 和 cursor 的 URL 编码；cookie 值只保存在随机临时 jar，密码通过 stdin 传给 curl，不出现在进程参数中：

```bash
docker run --rm --network homelab-v2 curlimages/curl:latest \
  http://palworld-playtime-guard:8080/api/v1/status

COOKIE_JAR=$(mktemp)
trap 'rm -f "$COOKIE_JAR"' EXIT
jq -n \
  '{username: env.PALREST_ADMIN_USERNAME, password: env.PALREST_ADMIN_PASSWORD}' | \
  curl -sS -c "$COOKIE_JAR" \
    -H 'Content-Type: application/json' \
    --data-binary @- \
    http://palworld-playtime-guard:8080/api/v1/admin/login

curl -sS -b "$COOKIE_JAR" --get \
  --data-urlencode 'start=2026-07-13T08:00:00+08:00' \
  --data-urlencode 'end=2026-07-13T09:00:00+08:00' \
  --data-urlencode 'limit=500' \
  http://palworld-playtime-guard:8080/api/v1/admin/server/metrics

curl -sS -b "$COOKIE_JAR" --get \
  --data-urlencode 'kind=settings' \
  --data-urlencode 'limit=100' \
  http://palworld-playtime-guard:8080/api/v1/admin/server/documents

# Only for a subsequent page, using a non-empty next_cursor from the response:
curl -sS -b "$COOKIE_JAR" --get \
  --data-urlencode 'kind=settings' \
  --data-urlencode 'limit=100' \
  --data-urlencode "cursor=$NEXT_CURSOR" \
  http://palworld-playtime-guard:8080/api/v1/admin/server/documents
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

## Analytics、保留与 schema

WebUI 顶部的 Overview / Analytics 可在运行状态和玩家活动分析之间切换。Analytics 显示当前在线人数及其 `as_of` 观测时间、今日累计观测玩家时长、今日并发峰值和活跃玩家数，并提供今日/本周排行、最近 7/30 天服务器并发曲线，以及所选玩家按本地日期汇总的每日活动。排行中的在线标记来自当前在线快照；本周按当前策略时区的周一到周日计算。

活动采集覆盖 REST API 成功返回的所有玩家，不受全局规则、玩家覆盖、豁免或策略是否启用影响。采集从部署此版本后开始，不回填历史数据。首次成功观测只建立基线；两次成功观测间隔超过 `server.max_observation_gap` 时，该区间会被丢弃。轮询/API/存储失败以及过长间隔代表“未知”，不能解释为在线人数为零或玩家活动为零。

并发数据按 5 分钟桶返回。`average_count` 是已观测时段按时间加权的平均在线人数，`max_count` 是桶内已观测峰值，`coverage` 表示该桶被成功覆盖的时间比例；没有有效观测的桶会保留为缺口（平均值和峰值为 `null`、覆盖率为 `0`），不会补零。单玩家查询会在存在该玩家活动数据时返回范围内逐日序列，未观测到活动的日期为零；未知玩家返回 404。

`GET /api/v1/analytics/summary` 返回 `online_count`、可空的 `as_of`、`today_observed_ms`、`peak_count`、可空的 `peak_at`、`active_players`、`ranking_period` 和 `ranking`。`GET /api/v1/analytics/activity` 返回 `range`、`timezone`、左闭右开的本地日期边界 `start`/`end`、`concurrency`，以及可空的 `player`。查询参数只接受上面列出的值；无效值返回 400。

`observation.raw_retention` 默认 `90d`。统一观察清理由成功玩家观察触发，最多每天一次、每类最多删除 500 行，因此积压时可能暂时超过截止时间。它清理未被长期服务器事实引用的原始 activity event、trajectory、private sample 和 server metric；`server_observation_state` 当前 metrics/info/settings 基线、内容寻址的 server documents/occurrences 和被它们引用的事件不会因该 raw cleanup 丢失。原有 Analytics 会话、并发桶和逐日统计仍使用独立的 90 天增量清理。当前 Policy 的 `timezone` 决定新的逐日归属和查询边界；修改只影响后续观察，不重分桶历史。

观察配置可省略，默认值如下。所有字段都是启动配置；修改后热重载会在 `/api/v1/status.config_reload_error` 明确要求重启，不会修改 SQLite 中的 Policy 文档或玩家策略状态。

```yaml
observation:
  server_document_interval: 5m
  trajectory_min_distance: 100
  trajectory_max_interval: 5m
  raw_retention: 90d
```

图表刷新时，新绘图区会在约 550 ms 内淡入并轻微水平平移，不会在新旧路径形状之间插值；系统启用“减少动态效果”时会停用该动画。

SQLite schema migration 当前最高版本为 v11；启动时从已有版本按顺序、事务化升级。v9 加入统一事件、轨迹、server metrics/documents、当前 server state 和敏感访问审计，v10 加入 private samples，v11 为玩家会话时间线查询补充索引。升级是 additive；不会把旧 Analytics 或 Policy 数据重写成伪造的统一观察历史。

## 排障与数据健康

- `/readyz` 未就绪或 `/api/v1/status.last_error` 有值：先检查 `/players` 连通性、REST 密码、响应 JSON、SQLite 写入和磁盘空间；这是关键路径故障。
- 日志出现 `optional server observation failed`：查看 `stream=metrics|info|settings` 与 `operation=read|record`。这类故障只形成对应流的未知缺口；玩家计时仍可健康。
- 时间线或图表有空洞：对照 `last_attempt`/`last_success`、`server.max_observation_gap` 和服务重启时间。不要用相邻样本插值判断玩家在未知区间的位置或在线状态。
- 文档长时间不更新：确认 `observation.server_document_interval`；内容未变化时只更新 authoritative state 的 `observed_at`，不插入 raw observation；相同 canonical JSON 也只保存一份内容。
- raw 数据比截止时间更旧：清理是每日、每类 500 行的有界任务，积压会逐步收敛；当前 server state 不应随 raw cleanup 消失。
- `config_reload_error` 提示 restart：重启 sidecar 应用 observation/server/HTTP/storage/retry 改动；Policy 继续以 SQLite 为准。

## 开发验证

```bash
go test -race ./...
go vet ./...
docker build -t palworld-playtime-guard:test .
cd webui && npm test && npm run build
git diff --check
```
