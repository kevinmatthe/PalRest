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

## 地图工作台

WebUI 默认打开世界地图。浮动玩家列表支持搜索、仅在线筛选、定位与持续跟随；手动操作地图会停止跟随。手机默认收起列表，选择玩家后自动收起以让出地图空间。

实时与历史共用一个地图实例。底部时间轴支持播放、暂停、拖动以及 1×/2×/4×/8× 速度（1× 每秒回看 1 分钟）；方向键每次调整 1 秒，Shift + 方向键调整 1 分钟。可选择最近 1/6/24 小时、7 天或指定日期。打开完整证据后再返回，保留视角、选择、时间和图层；离开地图或隐藏浏览器标签时暂停播放。

实时请求串行轮询，标记平滑过渡到新的观测位置。界面显示实际观测年龄；请求失败或超过 60 秒没有新观测时标记陈旧。历史刷新由“重新加载历史”显式触发，不受后台实时更新影响。

回放插值只用于画面，不产生新的事实记录。不同轨迹段、服务器重启、超过 5 分钟的缺口及至少 50,000 游戏单位的疑似跳跃均不连接，可跳到下一次观测。当前窗口每类最多加载 500 条记录，超出时显示提示；更早证据与行为统计仍从完整时间轴入口查看。

选择玩家后，可展开「旅程进度」查看拥有帕鲁、累计捕获记录、图鉴解锁与传送点解锁。变化卡显示两次存档观测之间的原值、新值和净变化，点击后跳到回放区间终点；没有地点证据的变化不生成地图坐标。历史卡片只展示游标之前已知的数据，实时进度每分钟刷新，旧记录缺少字段时显示「未采集」。

切换到「游玩小结」可查看当前观察窗口的有效时长、覆盖率、未知时长、已观测路径、等级变化及四项成长曲线。数字可展开保存的快照或位置证据，并跳转到变化区间终点。图表实点表示观测，虚线只连接可比较记录，不表示变化的准确发生时间；缺失、回档与世界切换会断开比较。小结代表已加载的观察窗口，不是完整游玩会话。

「停留热度」图层按有效观测时长加权：有效位置对的平均速度低于 50 游戏单位/秒时，将时长均分到两个端点所在的 10,000 单位网格。主要停留区域可定位到近似网格中心，不代表精确地点或挂机判断。断线、跳跃和未观测尾段不计入热度；回放时只累计游标之前已经结束的观测区间。

只有位置和相关进度记录未截断、无比较边界，并同时满足有效观测至少 5 分钟、覆盖率至少 60%、移动至少 2 分钟、新增传送点至少 1 个时，才显示「可能有探索活动」，可展开规则和原始证据。拥有帕鲁数增加不会被直接解释为捕获。

实时小结在打开小结或热度图层后读取位置历史，每分钟串行刷新，离开地图或隐藏标签页会暂停请求。历史小结复用已加载的数据；播放中按实际时间每秒更新统计，暂停或拖动立即更新，地图动画保持连续。位置窗口最多加载 500 条，进度各类最多加载 200 条；截断时明确提示，只汇总可验证的区间，不推断完整行程。

## 玩家进度 Checkpoint

启用 `save.enabled`，将 `save.path` 指向只读挂载的原始世界 `Level.sav`，并保留同级 `LevelMeta.sav`、`Players/` 与世界 GUID 目录。默认每 15 分钟导入，手动导入与定时导入共用单个解析队列。Worker 在独立进程中先复制稳定文件再解析，失败不会参与玩家计时或执法。

存档内部时间戳用于核对各文件是否属于同一次保存；来源时间使用原始 Level 文件的 UTC 修改时间。只有身份、世界、口径、时间顺序和文件一致性均可确认时才比较。首次只建基线；重复导入去重；乱序快照留存但不回退当前基线；重现过往存档或乱序观察会中断下一次比较；未知值不补零；累计计数下降或解锁集合减少时建立回档边界。分类目录摘要变化也会中断比较。快照、基线和变化在同一 SQLite 事务内提交。

拥有帕鲁按有效实例、归属及容器关系去重，不含人类 NPC；无个人归属的公会基地帕鲁单独提示，不分摊给成员，容器关系异常时显示未完整确认。累计捕获记录是游戏保存的计数，包含 Human 项，不能等同于帕鲁拥有数。图鉴和传送点只统计明确为 true 的解锁标志。拥有数增加不能单独证明捕获、孵化或交易。字段证据、世界身份、分类目录更新方法与独立运行命令见 [Save Worker 文档](tools/save_worker/README.md)。

`GET /api/v1/players/{userID}/progress?start=...&end=...&limit=200` 返回时间窗前基线、窗内 checkpoint 和区间变化。时间使用 RFC3339，窗口最多 31 天，每类最多 500 条；截断时返回最新记录及总数。接口仅通过唯一且精确的 REST player ID 关联存档，不按名字匹配，也不返回存档路径或解析错误原文。

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

每次关键轮询读取 Palworld REST `/players` 的 `name`、`accountName`、`playerId`、`userId`、`iP`、`ping`、`location_x`、`location_y` 和 `level`。独立的可选采样器在每轮读取 `/metrics` 的 `serverfps`、`currentplayernum`、`serverframetime`、`maxplayernum`、`uptime`、`basecampnum`、`days`，并按 `observation.server_document_interval` 读取 `/info` 的 `version`、`servername`、`description`、`worldguid` 和完整 `/settings` JSON。

玩家列表、Analytics、业务时间线和防沉迷执法构成关键路径；关键读取或写入失败会中断本轮连续性，不计未知区间，也不执法。服务器 metrics/info/settings 是边界超时的可选路径：三类请求彼此独立，任意一类读取或持久化失败只记录该数据流缺口，不会把缺口写成零，不会阻塞玩家关键路径，也不会阻止另外两类提交。

Phase 1 使用同一个 correlation ID 把一次玩家观察写成统一业务时间线：玩家加入、离开和已知属性变化是事件；坐标按最小移动距离或最大采样间隔稀疏采样，已知 level 改变或已知 ping 相对最后一个已采样已知值累计达到 `observation.trajectory_ping_change_threshold`（默认 10ms）时也立即保留轨迹点。小于阈值的浮点抖动不触发采样；NaN、无限值和负 ping 均视为 unknown、持久化为 0，unknown 与 known 之间的切换本身不构成变化。IP、ping 和 level 是只供管理员读取的稀疏 private sample。启动后的第一轮、轮询失败、持久化失败、超过 `server.max_observation_gap` 的区间以及进程停止期间都表示“未知”，不能推断为离线、零并发或零活动。

`/metrics` 的 uptime 明确下降会生成 `server_restarted`，并在同一 SQLite 事务中推进持久化 server runtime epoch。轨迹 API 同时返回 `runtime_epoch`，并以 `runtime:<epoch>:<base64url(raw_segment_id)>` 无歧义编码 `segment_id`；即使玩家边界样本先于异步 metrics 写入，restart 事务也会修正该时刻及之后已写样本的 epoch，因此前端不会跨服务器重启连线。epoch 在应用重启后恢复，重复提交和多写者 CAS 不会重复推进。

REST 观察与存档进度是独立来源。地图回放使用位置观测；存档变化使用 checkpoint 区间，两者不会伪装成同一时刻的采样。

## Prometheus / VictoriaMetrics 抓取

`GET /metrics` 输出 Prometheus text exposition（无需登录），供 VictoriaMetrics / vmagent scrape。  
看板 JSON：`deploy/grafana/palrest-dashboard.json`（Import 即可）。

### 指标族（扩大后）

**进程 / 轮询**
| 指标 | 含义 |
|------|------|
| `palrest_up` | handler 存活 |
| `palrest_online_players` / `palrest_known_players` | Guard 在线 / 已知玩家 |
| `palrest_poll_success_timestamp_seconds` / `_age_seconds` | 最近成功轮询 |
| `palrest_poll_error` / `palrest_config_reload_error` | 错误标志 |
| `palrest_process_uptime_seconds` / `palrest_config_version` | 进程与配置 |

**服务器本体（REST `/metrics` + runtime）**
| 指标 | 含义 |
|------|------|
| `palrest_server_fps` / `frame_time_milliseconds` | 性能 |
| `palrest_server_current_players` / `max_players` / `fill_ratio` | 容量 |
| `palrest_server_uptime_seconds` / `game_days` / `basecamps` | 世界状态 |
| `palrest_server_runtime_epoch` / `restarts_total` / `last_restart_timestamp_seconds` | 重启代数 |
| `palrest_server_metrics_age_seconds` | metrics 采样新鲜度 |

**服务器身份 / 设置**
| 指标 | 含义 |
|------|------|
| `palrest_server_info{version,server_name,world_guid}` | REST `/info`（标签身份） |
| `palrest_server_setting{key=...}` | REST `/settings` 白名单标量（ExpRate、流速、伤害倍率、开关等） |

**玩家实时（REST 轮询，label: `user_id`,`name`）**
| 指标 | 含义 |
|------|------|
| `palrest_player_online` | 是否在线 |
| `palrest_player_ping_milliseconds` | **仅在线**延迟 |
| `palrest_player_level` | **仅在线**等级 |
| `palrest_player_location_x` / `_y` | **仅在线** 世界坐标（游戏单位，非经纬度；供 Grafana XY Chart） |
| `palrest_player_distance_from_origin` | √(x²+y²) |
| `palrest_player_used_seconds` / `remaining_seconds` / `limit_seconds` | 防沉迷 |
| `palrest_player_policy_enabled` / `_exempt` | 策略开关 |
| `palrest_player_warning_active` | 活跃警告数 |
| `palrest_player_enforcement{status=...}` | 执法状态 |
| `palrest_player_last_online_timestamp_seconds` | 上次在线 |

**存档 Game-Data（最近一次 Save Import）**
| 指标 | 含义 |
|------|------|
| `palrest_save_import_timestamp_seconds` / `_age_seconds` / `level_bytes` | 导入新鲜度与文件大小 |
| `palrest_save_player_count` / `guild_count` / `basecamp_count` / `basecamp_area_sum` | 汇总 |
| `palrest_save_player_level` / `_exp` / `_hp` / `_shield_hp` / `_full_stomach` | 角色（label: name, save_uid, 可选 user_id） |
| `palrest_save_guild_*` | 公会等级/成员/据点/面积（label: guild, guild_id） |

不写 IP；不把精确坐标当序列。建议内网 scrape。

```yaml
- job_name: palrest
  scrape_interval: 15s
  static_configs:
    - targets: ["palworld-playtime-guard:8080"]
  metrics_path: /metrics
```

## 公开只读 API 与隐私

默认无需登录即可读取运行和防沉迷状态。服务建议只在 Docker 网络中暴露 `8080`：

- `GET /healthz`：进程、SQLite 与最近成功时间。
- `GET /readyz`：是否至少完成一次成功轮询。
- `GET /metrics`：Prometheus 指标（含在线玩家 ping / 用时，供监控栈；见上）。
- `GET /api/v1/status`：`started_at`、`last_attempt`、`last_success`、`last_error`、`online_count`、`config_version`、`config_reload_error`。
- `GET /api/v1/players` 与 `GET /api/v1/players/{userId}`：玩家身份显示字段、在线状态、解析后的规则、周期边界、用时/余额、提醒和执法状态。
- `GET /api/v1/overlay/snapshot?game_id=palworld&user_id=...`：供桌面悬浮条读取的版本化玩家快照，包含公开身份、延迟、计时和地图定位；支持 `ETag` 条件请求。
- `GET /api/v1/policies`：默认规则和玩家覆盖，不包含环境变量值或 REST 凭据。
- `GET /api/v1/analytics/summary?ranking=today|week`：在线快照、今日观测时长、峰值、活跃人数和排行。
- `GET /api/v1/analytics/activity?range=7d|30d&user_id=...&include_concurrency=true|false`：并发覆盖和可选玩家逐日活动。

JSON API 不返回玩家 IP、private samples、原始 server settings、管理员密码、Palworld REST 密码、Authorization 或 session cookie。`/metrics` 含在线 ping 与用时标签，仅供内网 scrape。Analytics 与其他公开只读接口一样不要求管理员 session。

这里的 `user_id` 是 Palworld REST `/players` 已公开返回并由服务用于累计/策略匹配的稳定标识，不是认证令牌。知道 UID 即可查询对应的公开只读快照；部署者仍应只在可信内网、Tailscale 或 ZeroTier 中暴露这些接口。

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

构建上下文应为 **playtime-guard 仓库根目录**（不是 `webui/`），以便 Dockerfile 多阶段使用 Git LFS 物化地图瓦片：

```bash
# 推荐：先在宿主把 LFS 对象拉齐（尤其是 smudge 被跳过的 clone）
git lfs pull --include="webui/public/map/tiles/**"

# 从仓库根构建（compose/sidecars 同理：context=./playtime-guard, dockerfile=webui/Dockerfile）
docker build -f webui/Dockerfile -t palrest-webui:test .
docker run --rm -p 127.0.0.1:18081:8080 \
  -e PALREST_API_UPSTREAM=http://palworld-playtime-guard:8080 \
  palrest-webui:test
```

地图瓦片存放在 `webui/public/map/tiles`（Git LFS）。构建阶段会尝试 `git lfs pull`；若工作区已是真实 PNG 则直接使用。若瓦片仍是 LFS pointer 文本，镜像仍可构建，运行时按瓦片回退到 `https://palworld.gg/images/tiles/...`。

`PALREST_API_UPSTREAM` 是 WebUI 容器内 Caddy 访问 Go sidecar 的地址。默认值为 `http://palworld-playtime-guard:8080`，适合与 sidecar 位于同一 Docker 网络的部署。通常不需要设置 `PALREST_API_BASE_URL`；保持为空时浏览器只访问 WebUI 容器，由 Caddy 反代 API 请求。

## 桌面悬浮条

Windows/macOS 悬浮条位于 `overlay/`。它与 WebUI/Caddy 使用同一个服务根地址读取 snapshot、玩家列表和私有地图瓦片；安装、网络示例、平台限制、构建签名流程及实机验收清单见 [overlay/README.md](overlay/README.md)。

## Analytics、保留与 schema

WebUI 顶部的 Overview / Analytics 可在运行状态和玩家活动分析之间切换。Analytics 显示当前在线人数及其 `as_of` 观测时间、今日累计观测玩家时长、今日并发峰值和活跃玩家数，并提供今日/本周排行、最近 7/30 天服务器并发曲线，以及所选玩家按本地日期汇总的每日活动。排行中的在线标记来自当前在线快照；本周按当前策略时区的周一到周日计算。

活动采集覆盖 REST API 成功返回的所有玩家，不受全局规则、玩家覆盖、豁免或策略是否启用影响。采集从部署此版本后开始，不回填历史数据。首次成功观测只建立基线；两次成功观测间隔超过 `server.max_observation_gap` 时，该区间会被丢弃。轮询/API/存储失败以及过长间隔代表“未知”，不能解释为在线人数为零或玩家活动为零。

并发数据按 5 分钟桶返回。`average_count` 是已观测时段按时间加权的平均在线人数，`max_count` 是桶内已观测峰值，`coverage` 表示该桶被成功覆盖的时间比例；没有有效观测的桶会保留为缺口（平均值和峰值为 `null`、覆盖率为 `0`），不会补零。单玩家查询会在存在该玩家活动数据时返回范围内逐日序列，未观测到活动的日期为零；未知玩家返回 404。

`GET /api/v1/analytics/summary` 返回 `online_count`、可空的 `as_of`、`today_observed_ms`、`peak_count`、可空的 `peak_at`、`active_players`、`ranking_period` 和 `ranking`。`GET /api/v1/analytics/activity` 返回 `range`、`timezone`、左闭右开的本地日期边界 `start`/`end`、`concurrency`，以及可空的 `player`。查询参数只接受上面列出的值；无效值返回 400。

`observation.raw_retention` 默认 `90d`。统一观察清理由成功玩家观察触发，最多每天一次、每类最多删除 500 行，因此积压时可能暂时超过截止时间。它清理未被长期服务器事实引用的原始 activity event、trajectory、private sample 和 server metric；`server_observation_state` 当前 metrics/info/settings 基线、`server_runtime_state` 当前重启代际、内容寻址的 server documents/occurrences 和被它们引用的事件不会因该 raw cleanup 丢失。原有 Analytics 会话、并发桶和逐日统计仍使用独立的 90 天增量清理。当前 Policy 的 `timezone` 决定新的逐日归属和查询边界；修改只影响后续观察，不重分桶历史。

观察配置可省略，默认值如下。所有字段都是启动配置；修改后热重载会在 `/api/v1/status.config_reload_error` 明确要求重启，不会修改 SQLite 中的 Policy 文档或玩家策略状态。

```yaml
observation:
  server_document_interval: 5m
  trajectory_min_distance: 100
  trajectory_ping_change_threshold: 10
  trajectory_max_interval: 5m
  raw_retention: 90d
```

图表刷新时，新绘图区会在约 550 ms 内淡入并轻微水平平移，不会在新旧路径形状之间插值；系统启用“减少动态效果”时会停用该动画。

SQLite schema migration 当前最高版本为 v12；启动时从已有版本按顺序、事务化升级。v9 加入统一事件、轨迹、server metrics/documents、当前 server state 和敏感访问审计，v10 加入 private samples，v11 为玩家会话时间线查询补充索引，v12 加入持久化 server runtime epoch 与轨迹代际索引。升级是 additive；已有轨迹从 epoch 0 开始，不会把旧 Analytics、Policy 或观察数据重写成伪造历史。

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
# save-worker stage COPYs the PalworldSaveTools submodule (no remote git clone).
git submodule update --init --depth 1 PalworldSaveTools
# Image builds use BuildKit cache mounts (mod/gocache/pip/npm); do not run go test inside Docker.
DOCKER_BUILDKIT=1 docker build -t palworld-playtime-guard:test .
# WebUI map tiles: materialise LFS on the host once (Dockerfile no longer copies .git).
git lfs pull --include="webui/public/map/tiles/**" --exclude=""
DOCKER_BUILDKIT=1 docker build -f webui/Dockerfile -t palrest-webui:test .
cd webui && npm test && npm run build
git diff --check
```
