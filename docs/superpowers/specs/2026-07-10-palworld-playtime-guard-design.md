# Palworld Playtime Guard Design

## Purpose

Build a Docker sidecar that polls the Palworld REST API, tracks player playtime within configurable fixed reset periods, warns players as they approach their allowance, and kicks players who exceed it. The design must support a future WebUI without coupling enforcement logic to a particular UI framework.

## Scope

The first release supports:

- Daily and weekly fixed reset periods with configurable timezone and reset time.
- One server-wide default policy plus per-user overrides and exemptions.
- Configurable warning thresholds sent through Palworld's server-wide announcement API.
- Enforcement through Palworld's kick API.
- Persistent usage, warning, and enforcement state in SQLite.
- A versioned, read-only HTTP API for health, status, policies, and current player usage.
- Configuration reload without restarting the process.

Rolling windows, policy write APIs, authentication for a publicly exposed API, and the WebUI itself are outside this release.

## Deployment

Create `playtime-guard/` as a standalone Go module and container build context. Add a `palworld-playtime-guard` service to `sidecars.yaml` with these properties:

- Join the existing `homelab-v2` Docker network.
- Reach Palworld at `http://palworld-server:8212/v1/api`.
- Read `ADMIN_PASSWORD` from the existing `.env.palworld` file.
- Mount `./playtime-guard/config.yaml` read-only at `/app/config.yaml`.
- Persist SQLite at `./playtime-guard/data/guard.db`.
- Expose port `8080` only to the Docker network, without a host port mapping.
- Use a container healthcheck against `/healthz`.
- Start with the default policy disabled so deployment cannot kick existing players until explicitly enabled.

No credential is copied into the image, configuration file, database, HTTP response, or application log.

## Architecture

The process contains six bounded components:

1. `PalworldClient` authenticates to the Palworld REST API, lists online players, sends announcements, and kicks a player by `userId`.
2. `PolicyService` validates configuration, resolves default and per-user policies, and computes the current period key and next reset time.
3. `UsageService` converts successful online observations into persisted usage, evaluates warnings, and decides whether enforcement is required.
4. `SQLiteRepository` owns schema migrations and transactional access to players, usage periods, warning events, and enforcement events.
5. `Poller` coordinates one reconciliation cycle at a time and applies failure and retry policies.
6. `HTTPServer` maps read-only service queries into stable versioned response DTOs. It does not access SQLite or the Palworld client directly.

Internal domain models remain separate from configuration structs, database rows, and HTTP DTOs. This lets a future management API or WebUI reuse the service layer without changing enforcement behavior or existing HTTP contracts.

## Player Identity

The Palworld REST API `userId` is the stable policy and usage key. Player names are display metadata only; changing a name cannot reset an allowance. The sidecar stores the most recently observed name and last-online timestamp. It does not store player IP addresses.

## Configuration

The initial configuration shape is:

```yaml
version: 1

server:
  base_url: http://palworld-server:8212/v1/api
  password_env: ADMIN_PASSWORD
  poll_interval: 30s
  request_timeout: 5s
  max_observation_gap: 75s

policy:
  timezone: Asia/Shanghai
  default:
    enabled: false
    period: daily
    reset_at: "04:00"
    limit: 2h
    warning_before: [30m, 10m, 5m, 1m]
  overrides:
    steam_123456:
      limit: 4h
    steam_789012:
      exempt: true

enforcement:
  kick_message: "Playtime limit reached. You can play again after {{ .ResetAt }}."
  announce_message: "{{ .PlayerName }} has {{ .Remaining }} of playtime remaining."
  kick_retry_initial: 15s
  kick_retry_max: 5m

http:
  listen: 0.0.0.0:8080

storage:
  path: /data/guard.db
```

Weekly policies also require `reset_weekday` using an English weekday name. An override may replace the limit, period, reset time, reset weekday, or warning thresholds, or mark a user exempt. Omitted fields inherit from the default policy.

Durations use Go duration syntax. The configuration loader rejects non-positive limits, invalid or duplicate warning thresholds, warnings greater than or equal to the limit, invalid timezones, missing weekly reset weekdays, unknown template fields, and unsupported schema versions.

The process watches the configuration file. A valid change is applied atomically to the next poll. An invalid change is logged and exposed in status while the last valid configuration remains active. Policy changes do not erase recorded usage. A reduced limit can therefore make a player immediately eligible for warning or enforcement on the next successful poll.

## Period Semantics

A daily period begins at the configured local `reset_at` and ends at the next local occurrence. A weekly period begins on `reset_weekday` at `reset_at` and ends at the next weekly occurrence. Timezone-aware calendar operations determine both boundaries; a period can therefore be 23 or 25 hours across daylight-saving transitions.

The period key is derived from the policy identity and the UTC start instant. This prevents collisions when a user changes from daily to weekly rules or changes reset time. Expired period rows remain available for audit but are not included in current usage.

## Polling And Accounting

Only a successful player-list response produces an observation. For each online user:

1. The first successful observation records presence but adds no time.
2. A later successful observation adds the elapsed time only if the same user was online in the previous successful observation and the gap does not exceed `max_observation_gap`.
3. A failed poll clears continuity. The next successful poll establishes presence again but adds no time.
4. A process restart also starts with no continuity, so downtime is never charged.
5. If an observation interval crosses a reset boundary, the interval is split between the old and new periods transactionally.

The default `30s` poll interval and `75s` maximum gap tolerate one delayed cycle without backfilling long outages. SQLite updates for one reconciliation cycle occur in a transaction. If that transaction fails, no warnings or kicks are issued from the unpersisted state.

## Warning And Enforcement Flow

When persisted usage crosses a warning threshold, the sidecar creates one warning event per user, period, and threshold. Because Palworld announcements are server-wide, the message includes the player's current display name. Successfully sent events are never repeated. Failed events are retried with bounded backoff while the warning remains relevant; crossing a later threshold makes the older pending warning obsolete.

At or above the allowance, the sidecar requests a kick using `userId` and a message containing the next reset time. Every attempt is audited. Failures use exponential backoff capped by `kick_retry_max`. A successful kick suppresses further attempts until the user is observed online again. A reconnecting over-limit user is kicked again on the next successful reconciliation cycle and remains ineligible until the period changes, the policy changes, or the user becomes exempt.

The default disabled policy records online presence but does not accrue restricted usage, announce, or kick. Enabling it starts accounting from the next successful observation and does not charge time from before activation.

## Persistence

SQLite uses embedded, ordered migrations and enables foreign keys and WAL mode. Core tables are:

- `players`: stable user ID, latest display name, first-seen time, and last-online time.
- `usage_periods`: user ID, policy identity, period key, UTC boundaries, accumulated milliseconds, and update time.
- `warning_events`: user ID, period key, threshold, state, attempt count, last error summary, and timestamps.
- `enforcement_events`: user ID, period key, action, result, error summary, and timestamp.
- `schema_migrations`: applied migration versions.

Usage increments and event creation use uniqueness constraints and transactions so retries and process restarts remain idempotent. Error summaries are bounded and sanitized before persistence.

## Read-Only HTTP API

All JSON responses use UTC RFC 3339 timestamps, duration values in whole milliseconds, and a versioned `/api/v1` prefix.

- `GET /healthz` reports process liveness, SQLite availability, last successful poll, and degraded state. It returns success while the process can serve requests and access SQLite.
- `GET /readyz` returns success only after a valid configuration is loaded and at least one Palworld player-list request has succeeded.
- `GET /api/v1/status` returns poll health, last poll error summary, online count, active configuration version, reload status, and default policy summary.
- `GET /api/v1/players` returns currently online players with resolved policy, used time, remaining time, next reset, warning state, and enforcement state.
- `GET /api/v1/players/{userId}` returns the same current-period detail for one known player, including warning thresholds already sent.
- `GET /api/v1/policies` returns the validated default policy and overrides with no environment values or credentials.

Unknown players and routes return `404`; malformed parameters return a structured `400`; internal failures return a structured `500` with a request ID and no sensitive details. The API has request timeouts, response size limits, and structured access logs. It is intentionally unauthenticated because Docker networking is its access boundary in this release.

## Failure Handling

- Player-list failure: do not account, warn, or enforce; clear observation continuity and mark polling degraded.
- Announcement failure: persist the failed attempt and retry with bounded backoff.
- Kick failure: persist the failed attempt and retry with exponential backoff.
- SQLite write failure: abort the cycle before external side effects and mark the service degraded.
- Configuration reload failure: retain the last valid configuration and expose the error summary.
- Palworld authentication failure: treat as a player-list failure and surface a sanitized status error.
- Shutdown signal: stop accepting new poll cycles, finish the active transaction or request within its timeout, close HTTP and SQLite cleanly, then exit.

Logs are structured JSON and include user IDs, player names, period keys, decisions, and request IDs where relevant. They never include passwords, authorization headers, or player IP addresses.

## Testing Strategy

Implementation follows test-driven development. Unit and integration tests cover:

- Daily and weekly boundaries, non-midnight resets, timezones, daylight-saving transitions, and interval splitting.
- Default policy inheritance, per-user overrides, exemptions, and live policy changes.
- First observation, continuous online accounting, offline transitions, failed polls, oversized gaps, and restart behavior.
- Warning threshold crossing, deduplication, obsolete warnings, retries, and template rendering.
- Initial enforcement, reconnect enforcement, success suppression, cooldown, and retry backoff.
- SQLite migrations, uniqueness, transactions, restart recovery, and write failures.
- HTTP response contracts, readiness and degraded states, unknown users, and credential redaction.
- Full reconciliation against a fake Palworld HTTP server and temporary SQLite database.
- Container image build, healthcheck, and `docker compose config` validation.

Tests use an injected clock and deterministic fake Palworld server. Production logic does not depend directly on wall-clock time in tests.

## Acceptance Criteria

The release is accepted when:

1. The image builds and all Go tests pass.
2. Compose validates with the new sidecar and exposes no host port for it.
3. A fake player accumulates only observed online time across polls and process restarts preserve persisted usage.
4. Daily and weekly reset boundaries produce the expected new allowance.
5. Default, override, and exempt policies resolve correctly by `userId`.
6. Each configured warning is sent at most once per user and period after success.
7. An over-limit player is kicked, and reconnecting before reset triggers enforcement again.
8. Palworld or SQLite failures cannot cause unpersisted accounting or erroneous enforcement.
9. Read-only API responses expose current status and usage without credentials or player IPs.
10. The deployed sample configuration remains disabled until an administrator explicitly enables it.
