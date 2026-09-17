# 有证据的游玩小结 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans / superpowers:dispatching-parallel-agents to implement independent tasks; verify and review the integrated result.

**Goal:** 在地图提供随观察窗口和回放游标变化的小结、成长曲线、时间加权停留热度，以及可展开证据的探索提示。

**Architecture:** 复用公开时间线和 progress API，在纯 TypeScript 模块中派生可审计结果，不新增事实表或执法规则。只使用游标之前已完成的有效观测对；增长统计只纳入完整落在窗口内的已保存差分。界面明确是「观察窗口小结」，不将部分采样伪称完整游玩 session。

**Tech Stack:** 现有 React、TypeScript、Leaflet、Vitest、Playwright；不新增依赖。

## 1. 证据与边界规则

- [x] `webui/src/map/journeySummary.ts` 及测试：复用 `prepareTrajectory` / `connectionBreak`，无效数据、重复时间、跨玩家/segment/epoch、超过 5 分钟或 50,000 单位跳跃均断开。
- [x] 汇总已结束观测边的时长、路径长度、移动时长、窗口未知时长、位置新鲜度与等级观测变化；不从最后一点推算在线时长，不利用游标之后的点补足历史。
- [x] 按 10,000 游戏单位网格汇总有效静止边的时长，权重来自时间而不是采样点数；每个区域保留起止时间和样本证据，坐标为近似区域。
- [x] progress 仅纳入 `interval_start >= start && interval_end <= cursor` 的持久化变化。窗口内跨世界、回档/不一致边界、缺失指标和截断必须显式标记；不能给出跨边界的单一净增长。
- [x] 四项趋势保留原 checkpoint ID/时间/值，只连接兼容、有效且没有缺失观测的相邻记录，未知不补零。
- [x] 探索提示须同时满足：完整未截断数据、至少 5 分钟有效观测、覆盖率至少 60%、移动时间至少 2 分钟、新增传送点至少 1，且进度无世界/口径/回档边界。提示为「可能有探索活动」，列规则和差分/位置证据，不输出概率。

## 2. 小结与成长图

- [x] `webui/src/components/WorkspaceJourney.tsx`、`JourneyTrend.tsx`、`webui/src/journey.css`：紧凑统计、覆盖率条、未知/截断/新鲜度提示、四项成长图及证据展开。
- [x] 每个进度数字可展开原始差分区间，点击定位时间；停留区域提供有位置证据的地图定位。无额外坐标的变化只跳时间。
- [x] 键盘可操作，图表有可读文字/数据表替代，兼容减少动态效果。手机收起详情后继续使用地图，不遮住底部控制。

## 3. 数据与地图联动

- [x] `webui/src/map/useJourneyTimeline.ts`：仅选择小结或启用热度图层且工作台可见时读取，实时每分钟串行更新、取消竞态；历史复用既有时间线结果，不因游标每帧查询。实时与历史缓存隔离。
- [x] `MapWorkspace.tsx` 增小结入口与停留热度开关；保留选择、跟随和进度回放功能；数据投影按实际时间每秒限频，暂停/拖动立即更新，不驱动地图实例重建。
- [x] `WorldMap.tsx` 用独立 Leaflet layer 绘制时长加权圆点，tooltip 用安全 DOM；切换模式/玩家/图层时清理。区域定位使用聚合位置，并明确近似与观测时间。

## 4. 验收

- [x] 先运行关键失败测试，再实现并验证：跨边界、future、truncation、unknown、采样密度不改变热度、取消请求、隐藏暂停及回放联动。
- [x] 完整 Web 测试（`npm test -- --run --maxWorkers=2`）、构建、Go 回归；浏览器桌面/移动端验证展开证据、热度图层与时间跳转。
- [x] 独立代码审查并关闭发现，更新 README 和设计状态。
提交要求：由助手执行签名提交并核验签名，保留用户原有四项 Docker 相关工作区改动。

## 验证记录（2026-09-17）

- Web：41 个测试文件、307 条测试通过；TypeScript 与 Vite 生产构建通过。Vite 保留单包超过 500 kB 的体积提示，未新增依赖。
- Go：`go test ./...` 全部通过。
- Chromium：使用合成公开 API 数据与真实地图瓦片，验证 375、414、768、1024、1440 像素宽度；无横向溢出，面板与回放控件避让，成长图/证据展开、热区定位、时间跳转、回退清除未来热度与推断均通过。
- 回放性能：1.5 秒连续播放中观察到小结时间更新 1 次，拖动立即生效，不产生额外历史查询；证据行仅展开时挂载。
- 独立审查发现并关闭两项问题：短距离阈值导致高频移动误算停留，改为统一速度阈值并补稀疏/密集采样回归；按游戏时间取整无法限制高速播放计算，改为实际时间限频并稳定组件 props。复核未发现遗留重要问题。
- 手工视觉检查修复统计卡片受全局 `dl` 样式影响的数字换行，桌面与手机截图复查通过。
