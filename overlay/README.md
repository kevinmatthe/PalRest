# PalREST Game Overlay

PalREST Game Overlay 是一个轻量的 Windows/macOS 桌面悬浮条，用于显示 Palworld 玩家身份、延迟、游玩计时、频控剩余时间和私有小地图。应用只通过 PalREST 的公开只读 HTTP API 取数；它不注入游戏进程，也不读取游戏内存。

## 使用前提

- 应用仅支持手动启动。安装与首次运行都不会注册 Windows 登录启动项或 macOS Login Item；需要时请从开始菜单、Applications 或安装目录自行打开。
- Palworld 应使用无边框窗口或窗口化全屏。独占全屏可能覆盖系统级悬浮窗，且不保证鼠标穿透行为。
- 服务地址必须是 WebUI/Caddy 对外提供的同源根地址，例如 Tailscale 的 `https://palbox.tailnet.ts.net:9443`，或 ZeroTier 网络中的 `http://10.147.20.8:8088`。
- 该同源必须同时提供 `GET /api/v1/overlay/snapshot`、`GET /api/v1/players` 和 `/map/tiles/...`。直接填写只暴露 sidecar `:8080` 的原始地址虽然可能返回 snapshot/players，但不能提供地图瓦片，因此不是完整可用的服务地址。
- 小地图只从已配置服务地址的同一私有主机加载瓦片，不会退回 `palworld.gg` 或任何其他公网地图源。

首次打开托盘/菜单栏中的“设置”，填写服务地址并选择 UID：

- Windows 会尝试从当前 Steam 用户精确识别 Palworld UID；找不到精确匹配时安全回退为手动选择。
- macOS 不读取 Steam 身份，始终由用户手动选择 UID。保存后该选择会持久化。
- UID 是 Palworld REST `/players` 已公开返回的玩家标识，不是登录凭据；应用没有额外鉴权流程。

正常模式下悬浮条不抢焦点并允许点击穿透。需要移动或缩放时，从托盘/菜单栏进入调整模式；锁定后恢复穿透。

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
- macOS 构建通过受保护的 `overlay-release` environment，并具备完整 Apple secrets。

确认版本号、默认分支和 environment 后执行：

```bash
git tag overlay-v0.1.0
git push origin overlay-v0.1.0
```

CI 会构建 NSIS/MSI、签名并公证 macOS app/DMG，验证签名与公证票据，然后自动创建同名 GitHub Release。Windows 产物在配置 Windows 代码签名前仍属于未签名安装包；macOS secrets 缺失或任一测试失败时不会创建 Release。

## macOS 正式签名与公证

GitHub 的受保护 environment `overlay-release` 必须配置以下 6 个 secrets：

- `APPLE_CERTIFICATE`
- `APPLE_CERTIFICATE_PASSWORD`
- `APPLE_SIGNING_IDENTITY`
- `APPLE_ID`
- `APPLE_PASSWORD`
- `APPLE_TEAM_ID`

正式交付有两条受保护路径：推送与 `overlay/package.json` 版本一致的 `overlay-v*` 标签，会由 `.github/workflows/overlay-release.yml` 自动创建 GitHub Release；需要只生成签名 artifact 而不创建 Release 时，可从仓库默认分支手动运行 `.github/workflows/overlay.yml`。两条路径都会在自动化测试成功后进入 `overlay-release` environment；缺少任一 secret 就明确失败，不生成或上传冒充正式交付的产物。environment 应启用所需审批与分支保护。

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
