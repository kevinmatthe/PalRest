# Palworld Playtime Guard

Palworld 防沉迷 sidecar。它通过 REST API 轮询在线玩家，按固定的每日或每周周期累计在线时长，在到达提醒阈值时发送全服公告，并将超限玩家踢出服务器。

## 特性

- 全服默认规则，以及按 Palworld `userId` 设置的覆盖和豁免。
- 可配置时区、每日或每周重置时间。
- SQLite 持久化用时、提醒和执法审计，重启不会丢失当前周期累计值。
- API 或数据库故障时停止计时和执法，避免误判。
- 配置规则和消息模板可热重载；连接、监听、存储和重试设置变更需要重启。
- Docker 内网只读 API，为未来 WebUI 提供稳定的 `/api/v1` 契约。

## 初次运行

在父级 Palworld 栈目录执行：

```bash
cp playtime-guard/config.example.yaml playtime-guard/config.yaml
mkdir -p playtime-guard/data
docker compose up -d --build palworld-playtime-guard
docker compose ps palworld-playtime-guard
docker compose logs --tail=100 palworld-playtime-guard
```

示例配置中的 `policy.default.enabled` 为 `false`。首次启动只验证连接和 API，不累计受限用时、不公告、也不 kick。检查配置后将其改为 `true`；sidecar 会在约一秒内热重载。

Palworld REST API 密码从父栈 `.env.palworld` 的 `ADMIN_PASSWORD` 注入，不应写入 `config.yaml`。

## 规则配置

```yaml
policy:
  timezone: Asia/Shanghai
  default:
    enabled: true
    period: daily
    reset_at: "04:00"
    limit: 2h
    warning_before: [30m, 10m, 5m, 1m]
  overrides:
    steam_123456:
      limit: 4h
    steam_789012:
      exempt: true
```

每周规则使用 `period: weekly`，并增加 `reset_weekday: Monday`。覆盖规则未填写的字段继承默认规则。玩家名称只用于显示，累计和覆盖始终以 REST API 返回的 `userId` 为准。

## 只读 API

服务仅在 Docker 网络中暴露 `8080`，没有宿主机端口映射：

- `GET /healthz`：进程和 SQLite 状态。
- `GET /readyz`：配置有效且至少成功轮询一次。
- `GET /api/v1/status`：轮询、在线人数和配置重载状态。
- `GET /api/v1/players`：在线玩家当前周期用时和剩余额度。
- `GET /api/v1/players/{userId}`：已知玩家当前状态。
- `GET /api/v1/policies`：脱敏后的默认规则和用户覆盖。

可以从同一 Docker 网络中的容器查询，例如：

```bash
docker run --rm --network homelab-v2 curlimages/curl:latest \
  http://palworld-playtime-guard:8080/api/v1/status
```

## 开发验证

```bash
go test -race ./...
go vet ./...
docker build -t palworld-playtime-guard:test .
```
