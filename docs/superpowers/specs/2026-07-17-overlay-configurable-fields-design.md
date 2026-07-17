# 可配置悬浮条字段设计

## 目标

将当前固定四项计时悬浮条改为“固定结构、可选内容”：保持 480×76 轻透冷青 HUD 的视觉与几何稳定性，同时允许用户为每个游戏配置左侧模块、四个数据槽和底部进度轨。字段由服务端用稳定 ID 描述，桌面端保存本地布局，不复制 Guard 或具体游戏的业务规则。

第一版支持 Palworld，并保留以后增加其他游戏 Provider 的边界。设置仍保持简单，不建设自由拖拽画布、模板语言或任意表达式。

## 已确认产品决策

- 采用固定结构加可选卡槽，不采用固定预设或自由画布。
- 顶部身份栏固定显示玩家名、可用时的等级、在线状态和数据新鲜度，避免切换玩家后误读数据。
- 左侧模块和四个数据槽都使用“主项 + 一个后备项”。
- 左侧默认是“小地图 → 玩家状态徽章”。
- 每个游戏独立保存一套布局；新游戏第一次使用其官方默认布局。
- 底部进度轨可选自动、指定字段或隐藏；默认自动使用策略周期用量，无可用进度时隐藏。
- 主项不可用时使用后备项；两者都不可用时保留槽位并显示 `--`，不重排、不扩展其他槽位。
- 设置页提供实时预览、离线后备说明和“恢复当前游戏默认布局”。

## 数据协议

### 兼容边界

现有 `GET /api/v1/overlay/snapshot` 和 `overlay.snapshot/v1` 作为旧客户端兼容接口保留。新增客户端使用：

```http
GET /api/v1/overlay/presentation?game_id=<game>&user_id=<uid>
Accept: application/json
If-None-Match: "<previous-etag>"
```

成功响应 schema 为 `overlay.presentation/v1`。它沿用单玩家、强 ETag、`304 Not Modified`、新鲜度和稳定错误语义，不让旧客户端因新增 capability 被动失效。

响应包含：

- `game_id`、`user_id`、`observed_at`、`fresh_until`。
- 固定头部所需的 `identity` 和 `source_status`。
- 可选 `map` 模块数据。
- `fields`：该游戏支持的完整可展示字段目录；当前没有值的字段仍出现但标记不可用。

### 字段结构

每个字段包含：

- `id`：稳定、点分命名的字段 ID；布局只引用此值。
- `label`：适合 480×76 槽位的短标签。
- `kind`：`text`、`integer`、`duration_ms`、`timestamp`、`latency_ms`、`coordinates` 或 `status`。
- `available`：当前快照是否有权威值。
- `value`：由 `kind` 决定的原始值；不可用时省略。
- `tone`：`normal`、`warning`、`danger` 或 `muted`。
- 可选 `progress`：0 到 1，仅可用于进度轨。

客户端根据 `kind` 做通用紧凑格式化；客户端不根据剩余时间重新推导告警、策略或执行状态。字段标签、可用性、tone 和 progress 均由 Provider 决定。

不可用字段必须留在目录中，使玩家离线时仍能预先配置延迟、坐标等字段。无效类型、越界 progress、重复 ID 或有 `available=false` 却携带 value 的响应会被客户端拒绝为不兼容数据。

### Palworld 第一版字段

| 稳定 ID | 类型 | 数据来源 |
| --- | --- | --- |
| `identity.account` | text | `Player.AccountName` |
| `identity.uid` | text | 规范化查询 UID |
| `identity.level` | integer | `Player.Level` |
| `presence.status` | status | 当前权威在线状态 |
| `presence.last_online` | timestamp | `Player.LastOnline` |
| `network.latency` | latency_ms | 新鲜且在线的 Ping |
| `location.coordinates` | coordinates | 新鲜且在线的世界坐标 |
| `activity.today` | duration_ms | Analytics 今日观测在线时间 |
| `activity.week` | duration_ms | Analytics 本周观测在线时间 |
| `policy.strategy` | text | Guard 解析后的策略类型 |
| `policy.cycle_used` | duration_ms | Guard 权威周期已用时间，带 progress |
| `policy.remaining` | duration_ms | Guard 权威剩余/额度/休息时间，带 tone 和 progress |
| `policy.period_end` | timestamp | 当前策略周期结束或重置时间 |
| `policy.enforcement` | status | Guard 当前执行状态 |

Provider 为禁用或豁免策略返回上述策略字段但标记不可用。API 继续禁止 IP、凭据、管理员事件、原始策略文档和 private samples。

## 本地布局配置

桌面配置升级为 schema 2，并保留 base URL、游戏、UID、缩放、锁定与原生几何字段。新增按 game ID 索引的 layout profile：

```json
{
  "layouts": {
    "palworld": {
      "left": { "primary": "map", "fallback": "player_badge" },
      "slots": [
        { "primary": "network.latency", "fallback": "presence.last_online" },
        { "primary": "activity.today", "fallback": "activity.week" },
        { "primary": "policy.strategy", "fallback": "policy.enforcement" },
        { "primary": "policy.period_end", "fallback": "policy.remaining" }
      ],
      "progress": { "mode": "auto", "field": "policy.cycle_used" }
    }
  }
}
```

规则：

- `slots` 必须恰好四项，主字段与后备字段不能相同。
- 字段 ID 只校验安全格式和长度，不要求保存时一定存在于当前目录；这样离线、服务降级或版本回退不会破坏配置。
- 运行时未知字段按不可用处理并尝试后备。
- `progress.mode` 为 `auto`、`field` 或 `hidden`。`auto` 先尝试配置中的首选字段，再按 Provider 字段顺序选择第一个可用 progress；`field` 只使用指定字段；两种模式都找不到合法 progress 时隐藏进度轨。
- schema 1 配置迁移到 schema 2 时保留连接、UID、缩放、锁定和几何，并为 Palworld 写入官方默认布局。
- 原生几何保存继续只更新几何字段，不覆盖 layout profile；设置保存也不得用旧几何覆盖原生新位置。

## 渲染与交互

Overlay 保持 480×76、62×62 左侧模块、固定头部、四等宽槽和 2px 进度轨。

字段解析流程：

1. 按 layout 找主字段。
2. 主字段 `available=true` 时渲染主字段。
3. 否则尝试后备字段。
4. 两者都不可用时显示该主字段标签和 `--`。

玩家状态徽章由固定头部的公开 identity 数据生成：名字首个可显示字符、等级和在线状态。它不需要头像服务或额外网络请求。

字段 tone 控制该槽值的颜色。整体边框和状态语义仍取 Provider 的最高风险 tone；离线、过期和断开连接继续整体降饱和。底部进度轨优先遵循已配置字段的 progress 与 tone。

## 设置页

现有 560×520 设置窗口保持尺寸不变，避免再次引入窗口裁切问题。`HUD 布局`作为可滚动内容中的新分区：

- 在窄窗口中实时预览置于控件上方，不使用宽屏双栏硬布局。
- 顶部身份栏以只读形式展示。
- 左侧模块、四个槽和进度轨均使用紧凑选择器。
- 选择器按身份、实时、活动和策略分组；暂不可用字段仍可选择，并显示“当前不可用”。
- 修改布局只更新本地预览；点击保存后与连接设置一起原子持久化，并通过现有跨 WebView 配置事件热更新悬浮窗。
- 保存失败保留原配置；已持久化但跨窗口同步失败继续显示现有的同步失败提示。
- “恢复默认布局”只重置当前游戏布局，不改服务地址、UID、位置、缩放或锁定状态。
- 底部保存操作区继续固定可见，内容区独立滚动。

## 错误、兼容与降级

- 新客户端遇到 presentation endpoint `404` 时显示明确的“服务版本不支持可配置字段”，不静默写入错误配置。
- `503`、断开或过期状态保留最后一次合法 presentation，并按现有 stale/disconnected 语义展示。
- 新字段加入目录不会改变已有布局；删除字段会触发该槽后备或 `--`。
- 未配置的新游戏使用客户端随版本提供的官方默认 layout；服务端字段目录决定其中字段当前是否可用。
- 旧 `/snapshot` 接口继续服务旧客户端，首版不设置删除日期。

## 测试与验收

### Go

- presentation 合约 JSON、字段稳定 ID、类型/value/available 不变量与强 ETag/304。
- 在线、离线、未知、禁用策略、豁免、告警和执行状态字段 fixture。
- 旧 snapshot endpoint 回归，证明旧客户端接口未改变。
- 禁止字段扫描，确保响应不含 IP、凭据、管理员或 private sample 数据。

### TypeScript / React

- presentation 严格解析各 kind、重复 ID、非法 progress 与 availability 不变量。
- 四槽主/后备/双缺失选择、未知字段降级与格式化。
- 地图到玩家徽章后备、固定头部、tone 和进度轨选择。
- schema 1 到 2 迁移、每游戏 profile、几何合并和无效布局拒绝。
- 设置页 560×520 客户区内预览、滚动内容和固定保存区可达。
- 保存后悬浮 WebView 热更新布局且不中断为旧配置；同步失败文案保持正确。

### 原生与手动

- Rust 配置 round-trip、未来 schema 保护、原生几何与 layout 合并。
- Windows 与 macOS 构建保持现有生命周期、拖动和鼠标穿透行为。
- 实机检查在线/离线切换不改变 480×76 几何，不发生槽位跳动。
- 切换主/后备字段、恢复默认、保存、重启后布局一致。

## 非目标

- 自由拖拽或缩放 HUD 内部组件。
- 用户表达式、脚本、模板语言和自定义 HTTP 字段。
- 多套命名布局或手动快速切换配置。
- 服务器 FPS、在线人数、服务器运行时间、世界天数、Guild/Base 或 save-import 字段；这些可在未来以新稳定字段 ID 增量加入。
- 在客户端重新实现 Guard 策略或从私有数据推导展示字段。
