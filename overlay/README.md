# PalREST Game Overlay

PalREST Game Overlay 是一个轻量的 Windows/macOS 桌面悬浮条，用于显示 Palworld 玩家身份、延迟、游玩计时、频控剩余时间和私有小地图。应用只通过 PalREST 的公开只读 HTTP API 取数；它不注入游戏进程，也不读取游戏内存。

## 使用前提

- 应用仅支持手动启动。安装与首次运行都不会注册 Windows 登录启动项或 macOS Login Item；需要时请从开始菜单、Applications 或安装目录自行打开。
- Palworld 应使用无边框窗口或窗口化全屏。独占全屏可能覆盖系统级悬浮窗，且不保证鼠标穿透行为。
- 服务地址必须是 WebUI/Caddy 对外提供的同源根地址，例如 Tailscale 的 `https://palbox.tailnet.ts.net:9443`，或 ZeroTier 网络中的 `http://10.147.20.8:8088`。
- 该同源必须同时提供 `GET /api/v1/overlay/snapshot`、`GET /api/v1/players`、`GET /api/v1/overlay/presentation`、`GET /api/v1/live/positions` 和 `/map/tiles/...`。直接填写只暴露 sidecar `:8080` 的原始地址虽然可能返回 snapshot/players，但不能提供地图瓦片，因此不是完整可用的服务地址。
- 小地图只从已配置服务地址的同一私有主机加载瓦片，不会退回 `palworld.gg` 或任何其他公网地图源。

首次打开托盘/菜单栏中的“设置”，填写服务地址并选择 UID：

- Windows 会尝试从当前 Steam 用户精确识别 Palworld UID；找不到精确匹配时安全回退为手动选择。
- macOS 不读取 Steam 身份，始终由用户手动选择 UID。保存后该选择会持久化。
- UID 是 Palworld REST `/players` 已公开返回的玩家标识，不是登录凭据；应用没有额外鉴权流程。

正常模式下悬浮条不抢焦点并允许点击穿透。需要移动或缩放时，从托盘/菜单栏进入调整模式；锁定后恢复穿透。

## 全员地图与性能

按 **Ctrl+Shift+M**（macOS 为 **Cmd+Shift+M**）打开或关闭全员地图，也可通过托盘的 **Team map** 打开。快捷键被其他应用占用时使用托盘入口。地图为独立的不透明置顶窗口，可移动、缩放窗口；常驻悬浮条保持原有大小和鼠标穿透。

- 默认适配所有有有效坐标的在线玩家；青绿色标记包含自己，黄色为其他玩家。
- 支持查看全员、跟随自己、鼠标缩放/拖动，以及点击下方玩家名定位。手动缩放或拖动后停止自动跟随。
- 重叠玩家合并为一个标记；点击标记可查看全部成员。自己离线不影响查看其他在线玩家。
- 相邻采样在 15 秒内分别靠近不同传送点（半径 25,000 世界单位），且位移至少 50,000 世界单位时，显示连接传送点中心的虚线方向箭头。标记为“疑似传送”，约 30 秒后消失，最多保留 20 条；断连、隐藏恢复与乱序响应不跨段推断。判定参数在 `overlay/src/map/teamTeleports.ts`，复用 WebUI 的传送点目录，支持原始和归一化坐标。
- 每 5 秒串行查询位置。底部显示来源观测时间，断连/过期保留最后位置并提示；位置不做逐帧插值。
- Esc、窗口关闭按钮或再次按快捷键会销毁地图窗口及其 WebView；未打开时没有全员地图请求。窗口不可见时暂停前端轮询。

常驻 HUD 去除了背景模糊和整层滤镜，关闭小地图动画，正常状态请求间隔从 2 秒改为 5 秒；原生隐藏时停止 HUD 轮询并卸载地图。Windows 进程检测仅刷新匹配游戏所需的进程信息，不再周期采集全机 CPU/内存/磁盘统计。

帧率验收需要在 Windows 游戏内进行：固定场景、分辨率与帧率限制，分别记录退出 overlay、仅 HUD、全员地图打开、地图关闭后的平均 FPS、1% low 及 overlay CPU/GPU 占用，各观察约一分钟。自动化测试不能代替实机 FPS 对比；若仍严重掉帧，请记录 Windows/WebView2/显卡驱动版本与游戏显示模式，以继续定位窗口合成开销。

## 本地开发与构建

需要 Node.js 20.19+/22.13+、Rust stable，以及目标平台的 Tauri 2 原生构建前提。依赖与安装包必须在目标系统上构建；本项目不要求通过 `apt` 修改系统。

前端检查：

```bash
npm --prefix overlay ci
npm --prefix overlay test
npm --prefix overlay run build
```

Windows（PowerShell，在仓库根目录）：

```powershell
npm --prefix overlay ci
cargo test --manifest-path overlay/src-tauri/Cargo.toml
npm --prefix overlay run tauri -- build --bundles nsis,msi
```

产物位于 `overlay/src-tauri/target/release/bundle/nsis/` 和 `overlay/src-tauri/target/release/bundle/msi/`。

安装时优先运行 NSIS `.exe`，也可以使用 `.msi` 交给 Windows Installer。当前项目尚未配置 Windows 代码签名证书，因此首次运行可能出现 SmartScreen 提示；正式公开分发前应补 Windows 签名。

macOS（Terminal，在仓库根目录）：

```bash
npm --prefix overlay ci
cargo test --manifest-path overlay/src-tauri/Cargo.toml
npm --prefix overlay run tauri -- build --bundles app,dmg
```

开发用 `.app`/`.dmg` 位于 `overlay/src-tauri/target/release/bundle/macos/` 与 `overlay/src-tauri/target/release/bundle/dmg/`。无正式凭据时它们只是 ad-hoc 开发产物，不能宣称已通过正式签名、公证或 Gatekeeper 验收。

安装正式产物时打开 `.dmg`，将应用拖入 `Applications`。`.app.zip` 用于保留 bundle 文件权限的直接分发或调试。

## GitHub CI 自动构建与 Release

`.github/workflows/overlay.yml` 会在 push、PR 和手动运行时构建 Windows/macOS 开发产物，结果可从对应 Actions run 的 Artifacts 下载。

`.github/workflows/overlay-release.yml` 负责正式 Release。发布标签必须同时满足：

- 格式为 `overlay-v<overlay/package.json 中的版本>`，例如当前版本 `overlay-v0.1.0`。
- 标签提交属于仓库默认分支的历史。
- Go、Overlay 前端及两个原生平台测试全部通过。
- macOS 默认发布明确标记为 `development-unsigned` 的 ad-hoc 产物，不要求 Apple 账号。

确认版本号、默认分支和 environment 后执行：

```bash
git tag overlay-v0.1.0
git push origin overlay-v0.1.0
```

CI 会构建 NSIS/MSI 和 macOS app/DMG，然后自动创建同名 GitHub Release。默认上传的 macOS 文件名包含 `development-unsigned`，首次运行需要用户在系统设置中明确允许；不需要 Apple Developer 账号。Windows 产物在配置 Windows 代码签名前同样属于未签名安装包。任一必需测试或平台构建失败时不会创建 Release。

## macOS 正式签名与公证

正式 Apple 签名是可选升级项。默认不需要创建 `overlay-release` environment，也不需要任何 Apple secrets。只有希望发布签名、公证版本时，才设置仓库变量 `ENABLE_APPLE_SIGNING=true`，创建受保护 environment `overlay-release`，并配置以下 6 个 secrets：

- `APPLE_CERTIFICATE`
- `APPLE_CERTIFICATE_PASSWORD`
- `APPLE_SIGNING_IDENTITY`
- `APPLE_ID`
- `APPLE_PASSWORD`
- `APPLE_TEAM_ID`

未设置 `ENABLE_APPLE_SIGNING` 时，推送与 `overlay/package.json` 版本一致的 `overlay-v*` 标签会直接发布未签名 macOS 版本。设置为 `true` 后，标签流程会在 unsigned 构建通过后进入 `overlay-release` environment，重新构建签名、公证版本；缺少任一 secret 或签名验证失败都会阻止 Release。需要只生成签名 artifact 而不创建 Release 时，也可从仓库默认分支手动运行 `.github/workflows/overlay.yml`。environment 应启用所需审批与分支保护。

## Windows/macOS 实机 smoke checklist

下表是发布前必须在真实平台逐项记录的验收清单。当前仓库工作环境是 Linux，未执行 Windows/macOS 实机操作；“CI 待验”表示相关平台 workflow 仍需产生可核对的运行记录，不表示已经通过。

| # | 验收项 | Windows 结果 | macOS 结果 |
|---:|---|---|---|
| 1 | 手动启动后只出现托盘/菜单栏入口；系统中没有新增登录启动项。 | 本机未执行 / CI 待验 | 本机未执行 / CI 待验 |
| 2 | 启动 Palworld 后 2 秒内显示悬浮条；退出后 2 秒内隐藏。 | 本机未执行 / CI 待验 | 本机未执行 / CI 待验 |
| 3 | 正常模式不获取焦点，鼠标点击能到达游戏。 | 本机未执行 / CI 待验 | 本机未执行 / CI 待验 |
| 4 | 调整模式可拖动；锁定恢复穿透；重启恢复显示器、位置和缩放。 | 本机未执行 / CI 待验 | 本机未执行 / CI 待验 |
| 5 | Windows 精确 Steam UID 可预选或安全回退；macOS 手动 UID 可持久化。 | 本机未执行 / CI 待验 | 本机未执行 / CI 待验 |
| 6 | 断开 Tailscale 后保留进程内最后一份快照并显示数据年龄，不把值清零。 | 本机未执行 / CI 待验 | 本机未执行 / CI 待验 |
| 7 | 玩家离线与 provider 断连在视觉上保持不同状态。 | 本机未执行 / CI 待验 | 本机未执行 / CI 待验 |
| 8 | 小地图网络请求只发往配置的私有主机。 | 本机未执行 / CI 待验 | 本机未执行 / CI 待验 |
| 9 | Windows 安装包可干净安装与卸载。 | 本机未执行 / CI 待验 | 本机未执行 / CI 待验（macOS 不适用 Windows 安装包） |
| 10 | 已签名并公证的 macOS 产物通过 Gatekeeper；凭据缺失时正式分发必须标记未完成。 | 本机未执行 / CI 待验（Windows 不适用 Gatekeeper） | 本机未执行 / CI 待验；凭据状态未核验，正式分发未完成 |
