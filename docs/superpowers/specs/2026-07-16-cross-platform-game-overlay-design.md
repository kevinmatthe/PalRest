# 跨平台游戏悬浮条设计

## 目标

为 Playtime Guard 增加一个轻量桌面悬浮条。首版支持 Windows 10/11 x64 与 macOS 14+ Apple Silicon，在检测到《幻兽帕鲁》运行时显示本人状态、小地图、延迟和时间统计；游戏退出后隐藏并继续驻留托盘或菜单栏。

悬浮条只读，不读取游戏内存、不注入游戏进程、不安装系统服务，也不承担频控计算。现有 Go sidecar 仍是玩家状态、Analytics 与频控数据的唯一权威源。

设计同时为更多游戏保留清晰扩展边界，但首版只实现 Palworld。扩展性来自稳定的核心、能力协议和游戏适配器，而不是首版动态插件系统。

## 已确认的产品决策

- 只覆盖无边框窗口或窗口化全屏；不支持独占全屏 Hook。
- 正常模式完全鼠标穿透；通过托盘或菜单栏进入调整模式。
- 不设置全局快捷键。
- 不随系统登录自动启动。用户手动启动后，程序持续驻留，直到主动退出。
- Windows 优先通过当前 SteamID64 自动匹配 `steam_<SteamID64>`；失败时允许手选。
- macOS 首次直接从已知玩家列表手选 `userId`，之后本地记住。
- `userId` 仅是查询筛选条件，不是认证凭据。
- 服务运行于 Tailscale、ZeroTier 等可信私网；悬浮接口无需额外鉴权。
- 玩家元信息只包括角色名称、等级、在线状态等，不读取当前出战帕鲁数据。
- 首版正式支持 Windows 与 macOS；Linux 只保留平台接口边界，不纳入构建和验收。

## 范围

### 首版包含

- Tauri 2 + React 桌面应用。
- Windows 托盘与 macOS 菜单栏常驻。
- Palworld 进程检测与悬浮条自动显示、隐藏。
- 透明、无边框、置顶、鼠标穿透窗口。
- 服务地址、游戏、玩家、窗口位置和缩放设置。
- 玩家名称、等级、在线状态、延迟与数据更新时间。
- 今日、本周、本频控周期用时与频控剩余时间。
- 以本人为中心、北向固定的 Palworld 局部小地图。
- Palworld Provider 与版本化的 Overlay Snapshot API。
- Windows x64 与 macOS Apple Silicon 构建、测试及安装产物。

### 首版不包含

- 独占全屏覆盖、游戏注入、内存读取或图形 API Hook。
- 当前出战帕鲁、背包、生命、技能等客户端游戏状态。
- 全局快捷键。
- 开机或登录自启动。
- 公网暴露所需的账号、配对码、令牌、TLS 或授权系统。
- 动态插件下载、插件市场或运行时加载第三方代码。
- Linux 正式支持。
- WebSocket 或 Server-Sent Events。
- 桌面端统计历史持久化。

## 总体架构

数据流如下：

```text
Palworld Server
      │ 现有 REST 轮询
      ▼
Playtime Guard / Go
  ├─ 现有玩家、位置、Analytics 与 Guard 服务
  ├─ OverlayProvider 接口
  ├─ PalworldOverlayProvider
  └─ overlay.snapshot/v1 HTTP API
      │ Tailscale/ZeroTier 私网，只读 HTTP
      ▼
Tauri Desktop Overlay
  ├─ Overlay Core
  ├─ Windows / macOS Platform Adapter
  ├─ Palworld Game Adapter
  └─ React Overlay + Settings UI
```

桌面端不得直接访问 Palworld 管理 REST API，也不得持有 Palworld 管理密码。所有业务统计在服务端完成，桌面端只负责选择本人、获取快照和展示。

## 扩展模型

系统分成三个稳定边界。

### Overlay Core

跨游戏、跨平台复用，负责：

- 应用生命周期、托盘或菜单栏与设置窗口。
- 服务连接、HTTP 轮询、ETag、内存快照缓存与错误状态机。
- 悬浮窗口位置、屏幕、缩放、锁定与鼠标穿透状态。
- 按能力显示身份、指标、计时和地图区域。

核心中不得出现 Palworld 进程名、Steam `userId` 格式、Palworld 坐标投影或频控策略字段。

### Platform Adapter

封装操作系统差异：

- 进程枚举。
- 托盘或菜单栏行为。
- 窗口置顶、透明、穿透与屏幕坐标。
- Windows Steam 当前账号发现。
- 本地配置目录和原子写入。

首版提供 `windows` 与 `macos` 实现。Linux 实现可以在未来加入，但不允许为了未交付平台降低首版两个平台的行为一致性。

### Game Adapter / Provider

游戏适配器位于桌面端，负责：

- 可识别的游戏进程。
- 本地账号到服务端 `userId` 的候选映射。
- 游戏特定字段标签、告警色与进度含义。
- 地图投影、瓦片和玩家标记。

Provider 位于 Go 服务端，负责把游戏已有数据源转换为通用快照。首版只实现 Palworld Adapter 与 Palworld Provider。

未来第二款游戏加入时，可以增加 Adapter 和 Provider；没有地图、延迟或频控能力的游戏直接省略相应能力，不返回伪造的零值。

首版不建设动态插件系统。加入第二款游戏后，再根据真实部署需求决定游戏模块是编译时注册还是外部插件。

## 服务端组件

新增独立的 Overlay 领域边界：

- `OverlayProvider`：按 `game_id` 与 `user_id` 生成能力快照。
- `PalworldOverlayProvider`：组合现有玩家、在线位置、Analytics 和 Guard 查询。
- Overlay API handler：解析参数、校验长度与格式、生成 ETag、映射稳定错误。

Overlay handler 不直接访问 SQLite 或 Palworld client。Provider 通过现有服务查询，保持 API、业务与存储解耦。

首版不新增数据库表，也不改变现有轮询、观察连续性、频控计算或执法行为。

## API 契约

请求：

```http
GET /api/v1/overlay/snapshot?game_id=palworld&user_id=<url-encoded-user-id>
Accept: application/json
If-None-Match: "<previous-etag>"
```

成功响应使用 `overlay.snapshot/v1`，包含：

- `schema`：固定为 `overlay.snapshot/v1`。
- `game_id`：首版为 `palworld`。
- `user_id`：规范化后的查询身份。
- `observed_at`：快照数据的权威观测时间。
- `fresh_until`：服务端判定快照仍新鲜的截止时间。
- `source_status`：`online`、`offline` 或 `unknown`。
- `capabilities`：快照实际具备的能力列表。
- `identity`：显示名称、可选账号名和可选等级。
- `latency`：能力存在时返回毫秒值。
- `timers`：带稳定 ID、显示标签、毫秒值、语义和可选进度的有序计时项。
- `map`：能力存在时返回坐标、投影 ID、瓦片集 ID 与瓦片基地址。

Palworld 首版计时项的稳定 ID 为：

- `today_observed`
- `week_observed`
- `policy_cycle_used`
- `policy_remaining`

固定窗口、冷却与额度策略可以调整显示标签、语义和进度，但不能改变稳定 ID 所代表的值。Provider 负责策略差异，Overlay Core 不复制 Guard 规则。

响应包含强 ETag。快照没有变化时返回 `304 Not Modified`，不返回响应体。

稳定错误：

- 缺失或非法参数：`400 invalid_request`
- 不支持的游戏：`404 game_not_supported`
- 未知玩家：`404 player_not_found`
- Provider 暂时不可用：`503 snapshot_unavailable`

接口只能返回指定玩家的单人快照，不提供批量 Overlay 查询。它不得返回 IP、private samples、管理员事件、服务器凭据或原始 settings。

## 时间语义

今日与本周采用 Analytics 的观测在线时间，并使用服务端策略时区划分自然日和自然周。它们不是客户端 Steam 游戏时长，也不填补 sidecar 停止、轮询失败或未知区间。

本频控周期与频控剩余直接采用 Guard 当前解析策略的权威状态：

- 固定窗口显示当前日或周周期的已用和剩余。
- 冷却策略显示当前游玩段或休息段的相应状态。
- 额度策略显示可用额度及恢复语义。

服务端必须显式返回适合当前策略的标签与 tone。客户端只格式化持续时间和颜色，不重新推导策略状态。

## 桌面应用组件

桌面应用位于独立 `overlay/` 目录，包含：

- Rust/Tauri shell：平台适配、进程检测、窗口控制、托盘、配置 I/O。
- React Overlay Core：快照状态机、轮询和能力式布局。
- Palworld Adapter：字段呈现与地图实现。
- Overlay Window：透明只读展示窗口。
- Settings Window：普通可交互设置窗口。

不在首版建立 npm workspace 或发布共享包。模块通过小型接口和 JSON 契约 fixture 保持边界；第二款游戏出现后再提取真正共享的包。

## 平台行为

### Windows

- 支持 Windows 10/11 x64。
- 检测 Palworld Windows 客户端进程。
- 从当前 Steam 状态取得 SteamID64，并尝试精确匹配 `steam_<SteamID64>`。
- 无法确认时打开玩家选择界面，不按名称猜测。
- 使用透明、无边框、始终置顶的 WebView2 窗口。

### macOS

- 支持 macOS 14+ 与 Apple Silicon。
- 检测 Mac App Store Palworld 进程。
- 首次由用户从服务端已知玩家列表选择 `userId`。
- 使用菜单栏应用和透明、无边框、置顶窗口。
- 正式分发产物需要 Developer ID 签名和 Apple 公证；缺少凭据时只能产出开发或未公证构建，不能宣称完成正式分发。

两个平台都不读取游戏内存。进程检测只决定悬浮条显示与隐藏。

## 生命周期与交互

- 应用默认不注册登录自启动。
- 用户手动打开后，主交互入口位于托盘或菜单栏。
- 未检测到游戏时不显示悬浮窗口。
- 检测到游戏后 2 秒内显示悬浮窗口。
- 游戏退出后 2 秒内隐藏悬浮窗口。
- 正常模式不获取焦点且完全鼠标穿透。
- 托盘或菜单栏提供：显示状态、调整位置、设置、重新选择玩家、退出。
- 调整模式临时取消鼠标穿透，显示虚线边框和拖动提示。
- 完成调整后保存当前屏幕、位置和缩放，并恢复穿透。
- 不提供全局快捷键。

## 界面设计

默认悬浮条约为 `480 × 76 px`，使用选定的双层紧凑布局：

- 左侧为约 `62 × 62 px` 的局部小地图。
- 右上显示玩家名称、等级、在线状态、延迟与更新时间。
- 右下显示今日、本周、本周期和频控剩余四项。
- 底部细进度条表达频控进度。

小地图北向固定并始终以本人为中心。Palworld 当前快照没有可靠朝向，因此首版只绘制位置点，不绘制方向箭头。未来游戏或数据源提供可靠 `heading` 能力时可以增加。

状态表达：

- 正常：青绿色重点色。
- 临近限额：琥珀色边框、状态和剩余时间；不闪烁、不弹窗。
- 已达限制或休息中：红色或策略指定 tone，文本明确说明状态。
- 玩家离线：保留累计统计，淡化最后位置并标明离线。
- 数据断连或过期：保留本次进程中的最后有效值，整体降饱和并显示最后更新时间。
- 调整模式：显示虚线外框与拖动提示。

Settings Window 只包含服务地址、游戏、玩家选择、缩放和重新定位，不把管理 Policy 或服务器操作带入桌面工具。

## 本地配置与隐私

本地仅持久化：

- 服务地址。
- `game_id` 与 `user_id`。
- 当前屏幕、窗口位置与缩放。
- 锁定和鼠标穿透状态。
- 配置 schema 版本。

配置使用平台应用数据目录和原子替换写入。它不保存玩家快照、统计历史、地图轨迹、IP、管理员凭据或 Palworld REST 密码。

`userId` 在本设计中是公开查询标识，不是秘密。可信私网成员可能查询其他已知玩家；这是明确接受的边界。若未来服务暴露公网，必须单独设计认证，不能把 UID 当作认证。

## 地图资源

现有 Palworld 地图目录约 126 MB、5461 个瓦片，不随桌面安装包完整复制。

- 瓦片通过配置的私网服务基地址按需加载。
- 使用 WebView 的标准磁盘缓存缓存已访问瓦片。
- 断网时可以继续显示已缓存区域，但不得承诺完整离线地图。
- 不使用 `palworld.gg` 或其他公网服务作为隐式回退。
- Provider 返回稳定的瓦片集和投影 ID，Palworld Adapter 负责解释。

## 轮询与错误状态

- 客户端每 2 秒请求一次快照。
- 使用 ETag 避免未变化响应体。
- 单次请求必须有有界超时；失败后指数退避，最大间隔 30 秒。
- 一次成功请求立即恢复 2 秒间隔。
- 服务端通过 `fresh_until` 定义新鲜度，客户端不自行猜测 sidecar 轮询周期。
- 本次应用运行中保留最后有效快照；退出后不持久化快照。

错误处理：

- 玩家离线与服务断连是不同状态。
- `404 player_not_found` 触发重新选择提示，不自动选择同名玩家。
- `503` 或网络失败保留最后快照并显示断连。
- 未知 schema 或不支持的重大版本不渲染数据，在托盘或菜单栏提示升级。
- 缺失能力只隐藏对应区域，不使整个悬浮条失败。
- 瓦片失败只影响地图，不影响身份、延迟和计时。

## 测试策略

### Go

- Provider 聚合正确性。
- 今日与本周的时区和周边界。
- 三种 Guard 策略的计时标签、值与 tone。
- 在线、离线、unknown 与缺失能力。
- 参数校验、未知游戏、未知玩家和 Provider 故障。
- ETag 与 `304`。
- JSON 响应不包含 IP、private samples、凭据和管理员数据。

### React / TypeScript

- `overlay.snapshot/v1` fixture 解析。
- 正常、警告、限制、离线、断连、过期和协议不兼容状态。
- 能力缺失时的自适应布局。
- 持续时间格式化与策略提供标签。
- Palworld 坐标投影和局部地图中心。
- Settings 校验和玩家重新选择。

### Rust / Tauri

- 配置 schema、原子保存和迁移。
- Windows SteamID64 到 Palworld `userId` 的精确转换与失败回退。
- 游戏进程匹配规则。
- 平台适配接口的共享契约测试。
- 单实例行为与退出清理。

### CI 与真实平台验证

CI 分别在 Windows x64 与 macOS Apple Silicon runner 上执行单元测试、前端测试和安装产物构建。Go 输出与前端解析共享同一组 JSON fixture，防止协议漂移。

置顶、穿透、焦点、托盘、进程联动和多显示器坐标必须在真实 Windows 与 Apple Silicon Mac 上执行人工验收，不能仅靠无界面 CI 宣称通过。

## 验收标准

- 手动启动后只驻留托盘或菜单栏，不注册登录自启动。
- Palworld 启动后 2 秒内显示，退出后 2 秒内隐藏。
- 正常模式不抢焦点且鼠标完全穿透。
- 调整模式可拖动，重新启动后恢复正确屏幕、位置和缩放。
- Windows 能精确自动匹配 Steam UID；失败时安全退回手选。
- macOS 能手选 UID 并在后续启动中复用。
- 正常网络下每 2 秒刷新；未变化快照使用 `304`。
- Tailscale 断开后不把未知数据显示为零，并显示最后更新时间。
- 今日、本周、频控周期与剩余时间符合服务端权威查询。
- 地图只访问配置的私网瓦片源，无公网隐式回退。
- API、配置和日志不包含 IP、管理员样本或凭据。
- Windows 安装包通过真实安装测试。
- macOS 签名、公证产物通过 Gatekeeper 测试；若没有签名凭据，该项明确保持未完成。

## 实施顺序约束

实施应先建立协议 fixture 与 Palworld Provider，再实现纯 React 状态界面，随后接入 Tauri Core 与平台适配，最后完成真实平台窗口行为和打包。不得先复制现有 WebUI 整页或把 Guard 业务逻辑移入客户端。
