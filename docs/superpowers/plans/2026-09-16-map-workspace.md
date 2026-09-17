# 地图工作台 Implementation Plan

> Execute inline with executing-plans; approved design: `../specs/2026-09-16-map-workspace-checkpoints-design.md`.

**Goal:** 完成设计阶段 A：地图首页、同图实时/历史切换、连续回放和可折叠玩家面板。

**Architecture:** 新建 MapWorkspace 管理选择、模式和历史请求；WorldMap 只维护一个 Leaflet 实例及稳定玩家标记。独立的纯函数处理轨迹连续性和时间插值，独立 hook 管理可暂停的播放时钟与串行实时轮询。既有时间轴保留为完整证据入口。

**Tech Stack:** 现有 React 19、Leaflet 1.9、TypeScript、Vitest；无需新增产品依赖。

## 任务与验收

- [x] 1. `webui/src/map/workspacePlayback.test.ts`：测试中点插值、边界时间、不同玩家/轨迹段/epoch、长缺口、疑似传送、无效点、暂停/倍速时钟。运行 `npm test -- src/map/workspacePlayback.test.ts` 观察失败，再实现 `workspacePlayback.ts`。
- [x] 2. `webui/src/components/MapWorkspace.test.tsx`：测试选择与折叠、历史数据隔离、请求切换取消、失败保留实时数据、回到实时、暂停与拖动。新增 `useWorkspaceData.ts` 和 `usePlaybackClock.ts`，串行请求，清理 timer/AbortController/RAF。
- [x] 3. `webui/src/map/WorldMap.tsx` 和 `worldMapMarkers.ts`：地图生命周期、瓦片回退、稳定 marker、短位置过渡、焦点浮窗、跟随暂停、图层与分段轨迹。使用 DOM 文本节点输出玩家名；仅在连续观测间插值。
- [x] 4. `webui/src/components/MapWorkspace.tsx`、`WorkspaceRoster.tsx`、`WorkspacePlaybackBar.tsx`、`map-workspace.css`：深色地图工作台，右侧折叠名单、底部时间控制、位置浮窗、事件列表与可选统计。小屏抽屉、44px 触摸目标、减少动态效果。
- [x] 5. 修改 `webui/src/main.tsx`：默认地图，保留总览/分析/时间轴/策略入口，共用玩家选择。更新 `main.test.tsx` 的首页期望，保留其余导航契约。
- [x] 6. 运行 `npm test -- --reporter=dot`、`npm run build`；浏览器检查 375/768/1024/1440 视口、真实地图瓦片、实时更新与历史播放；检查 `git diff --check`。文档记录结果与本阶段边界。

## 明确行为

- 历史游标使用毫秒时间；1× 为每秒推进 60 秒历史，2×/4×/8×按比例推进；标签明确标注压缩关系。
- 超过 5 分钟、不同段/epoch、相邻距离至少 50,000 游戏单位的边不连接；不向第一点之前或末点之后外推。
- 回放可跳过明确标识的缺口；没有点时允许查看事件，但禁用轨迹播放。
- 获取最近 500 条轨迹及事件；总量超过响应时始终显示采样限制并提供完整证据入口，不暗示窗口完整。
- 历史请求仅由玩家/时间窗口/显式重载驱动，不被全局 10 秒刷新重置。
- 新帕鲁/解锁 checkpoint 属于阶段 B，不显示未经验证的数值。本次实现为其提供已有事件查询到地图时间游标的接入位置。

## 参考

- Leaflet 1.9.4: https://leafletjs.com/reference.html （marker.setLatLng、tooltip、map 生命周期）
- React useEffect: https://react.dev/reference/react/useEffect （取消请求、对称清理、StrictMode）
- Context7 因本机缺少 API key 不可用，以上官方文档已核对。

## 实施与验证结果

- 新增独立的串行轮询、历史缓存/显式重载、连续时间时钟、分段插值和持久化标记控制。
- MapWorkspace 在切换到总览/分析/证据时保留挂载，后台停止位置轮询并暂停回放；返回时恢复工作区。
- 移动端默认收起玩家抽屉，选择玩家后收起；历史滑块支持秒级键盘步进和 Shift 分钟步进。
- 为旧、新地图统一加入 Leaflet 1.9.4 销毁保护。实际触发库的缩放计时器，证实原先销毁后抛出 `_leaflet_pos` 异常，修复后通过回归。
- 前端全量测试：32 个文件、229 项通过。生产构建通过；仍有原有类型的单包超过 500 kB 提示（当前 JS 约 535 kB，gzip 约 163 kB）。本阶段没有新增产品依赖。
- Chromium 使用模拟 API 与仓库实际地图瓦片验证 375/768/1024/1440 宽度、标记 DOM 复用、播放/暂停、显式历史重载、跳过缺口、证据往返状态保留、减少动态效果及手机回到实时；未使用真实玩家数据。
- 界面截图位于本次工作环境 `/tmp/playguard-browser/`；浏览器验证脚本为同目录 `check.cjs`。
- 在阶段 A 提交时，B/C 尚未实现；后续已分别通过 `2026-09-16-progress-checkpoints.md` 与 `2026-09-17-evidence-journey.md` 完成，并通过 `2026-09-17-map-design-completion.md` 补齐原始设计遗漏项。以上记录保留阶段 A 当时的验证范围；本系列不改变 Guard 计时/执法与运行中部署。
