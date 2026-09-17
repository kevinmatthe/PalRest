# 玩家进度 Checkpoint Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** 将真实存档中可验证的帕鲁统计和解锁集合接入地图，展示可复核的区间变化。

**Architecture:** Python Worker 从稳定副本读取，输出兼容 v1 的可选进度字段。Go 在原有导入事务内保存 checkpoint、维护可比较基线并生成去重差分；独立查询 API 返回时间窗及窗前基线。React 在本地按回放游标选择进度，不把实时数据或未来观测泄漏进历史。

**Tech Stack:** 现有 palsav、Python unittest、Go/GORM/SQLite、React/Leaflet/Vitest；不新增产品依赖。

## 1. 存档契约与字段验证

- [x] 在只读真实历史存档验证所属实例、捕获记录、图鉴标志、传送点集合、世界身份和来源时间，记录匿名结构证据。
- [x] `tools/save_worker/` 测试稳定副本、缺失不补零、有效集合去重、跨文件一致性；运行 Python 单测确认失败后实现。
- [x] schema 保持 `palrest.save_snapshot.v1`，parser 升至 2。新增 `source.world_id/source_time/source_time_kind/consistent/consistency_reason`；`players[].progress.schema_version=1`，四项 `metrics` 含 `state/value/ids/reason`。known 指标非负；未知与不支持没有数值。

## 2. 原子存储与差分

- [x] 在 `internal/store/save_progress_test.go` 先覆盖首次、重复、乱序、缺失、跨世界、口径变更、计数回退、原子失败、精确身份和历史窗基线。
- [x] `internal/store/save_progress.go` 添加 checkpoint/head/change 模型及查询，`save_imports.go` 在同一事务调用。集合保留新增/移除，变更保留来源、版本和时间区间。
- [x] 不一致样本中断比较；下一个有效样本重新建基线。时间不递增不回退 head。累计捕获回退或解锁集合减少作为回档/口径边界，不推断行为。
- [x] `internal/saveworker/importer.go` 限制同一 importer 的解析并发为一，等待可取消；测试取消与释放。
- [x] 运行 `go test ./internal/store ./internal/saveworker`。

## 3. API 与地图详情

- [x] `internal/api/save_progress.go` 提供 `GET /api/v1/players/{userID}/progress?start=...&end=...&limit=200`，复用 31 天范围验证；不暴露文件路径或解析原文。
- [x] 响应含 `status/baseline/checkpoints/changes/checkpoint_total/change_total`。仅精确且唯一的 REST player_id 绑定，无名字匹配。
- [x] `webui/src/components/WorkspaceProgress.tsx` 及 hook 查询一次历史窗后本地筛选；实时慢轮询可取消，隐藏页停止轮询，切玩家不串数据。
- [x] 地图可折叠进度卡显示四项统计、来源时间、区间变化、边界和截断；点击变化定位回放时间。移动端控件避让。
- [x] `go test ./internal/api`、`cd webui && npm test -- --run` 验证范围/错误/身份/未知/回放和竞态。

## 4. 集成验收与签名提交

- [x] 真实备份解析前后对比，只输出匿名汇总，不提交个人存档数据。
- [x] 完整 `go test ./...`、Python 单测、Web 测试和 `npm run build`；浏览器检查桌面和移动布局及回放联动。
- [x] 更新 README 与设计状态，检查 diff，保持用户已有四项 Docker 文件改动不进入本次提交。
- [x] 助手执行 `git commit -S` 并核验 `%G?=G`；用户在自己的终端解锁密钥，commit 由助手完成。

## 实现与验证记录

- Python：22 项测试通过，涵盖缺失槽位、不完整角色字段、多公会冲突、规范化后重复 GUID；生成目录明确排除 Human，753 个 Pal ID 与 370 个 NPC ID 无交集。
- Python 合同保持 v1/parser 2，新增 `source.progress_definition` 保存分类目录 SHA256，作为口径边界；`progress.unattributed_pals` 仅提示公会未归属数量，不参与个人变化。
- 来源时间使用原始 Level mtime UTC。真实根 Timestamp 是服务器本地墙钟，只比较原始保存代，不将其错误标为 UTC。
- 补齐旧指纹重现及所有世界的乱序保护：不回退 head，但设置待处理边界，下一次建立新基线。当前 head 重复导入保持正常连续性。
- `go test ./...` 通过；`go test -race ./internal/store ./internal/saveworker ./internal/api` 通过。
- Web：35 个文件、247 项测试通过；生产构建通过（现有单 chunk 大于 500 kB 提示，产物 gzip 约 165 kB）。
- Playwright 实测 375/414/768/1024/1440 五档屏宽，无横向溢出、无 pageerror，进度展开与回放终点跳转通过。
- 匿名真实端到端：11 名玩家各两个 checkpoint，4 条预期变化（+2 个人帕鲁、+2 捕获记录、+2 图鉴、+1 传送点），重复导入幂等；临时测试使用 Go overlay，不把存档或私人数据加入仓库。
- Dockerfile 仅增加分类目录文件 COPY，本次提交不包含用户原有四项 Docker 相关文件改动。
