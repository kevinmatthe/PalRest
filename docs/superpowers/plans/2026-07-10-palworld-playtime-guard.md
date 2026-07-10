# Palworld Playtime Guard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build and deploy a Go sidecar that persistently accounts Palworld playtime in configurable daily or weekly periods, warns approaching players, kicks over-limit players, and exposes an internal read-only API.

**Architecture:** A single Go process coordinates a typed YAML configuration, calendar-aware policy service, transactional SQLite repository, Palworld REST client, deterministic reconciliation engine, and versioned HTTP server. Domain interfaces isolate external effects so accounting and enforcement are tested against an injected clock and fakes before production adapters are added.

**Tech Stack:** Go 1.26.4, Go standard library HTTP and templates, `go.yaml.in/yaml/v3` v3.0.4, `modernc.org/sqlite` v1.53.0, Docker multi-stage builds, Docker Compose.

---

## File Map

- `go.mod`, `go.sum`: module and pinned dependencies.
- `cmd/playtime-guard/main.go`: signal-aware process assembly only.
- `internal/config/config.go`, `config_test.go`: strict YAML decoding, defaults, validation, and password lookup.
- `internal/domain/types.go`: shared player, policy, period, usage, warning, enforcement, and status types.
- `internal/policy/service.go`, `service_test.go`: inheritance and calendar boundary calculations.
- `internal/store/sqlite.go`, `migrations.go`, `sqlite_test.go`: SQLite lifecycle, migrations, queries, and reconciliation transactions.
- `internal/palworld/client.go`, `client_test.go`: authenticated REST calls and response validation.
- `internal/guard/service.go`, `service_test.go`: observations, accounting, warnings, enforcement, and retry decisions.
- `internal/poller/poller.go`, `poller_test.go`: scheduled reconciliation and continuity reset on list failures.
- `internal/api/server.go`, `server_test.go`: health and `/api/v1` read-only routes.
- `internal/app/app.go`, `app_test.go`: dependency construction, reload loop, graceful startup and shutdown.
- `config.example.yaml`: disabled deployable configuration.
- `.gitignore`: runtime configuration, database, and build artifact exclusions.
- `Dockerfile`, `.dockerignore`: reproducible non-root container.
- `README.md`: configuration, operation, API, and enablement procedure.
- `../sidecars.yaml`: integration into the parent Palworld stack.

### Task 1: Module And Strict Configuration

**Files:**
- Create: `go.mod`
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Create: `config.example.yaml`

- [ ] **Step 1: Create the module and install pinned dependencies**

Run:

```bash
go mod init github.com/kevinmatt/palworld-playtime-guard
go get go.yaml.in/yaml/v3@v3.0.4 modernc.org/sqlite@v1.53.0
```

Expected: `go.mod` declares Go 1.26 and both modules appear in `go.mod`/`go.sum`.

- [ ] **Step 2: Write failing strict-decoding and validation tests**

Tests create YAML strings and call `config.Parse([]byte(yaml), lookupEnv)`. Assert the valid disabled sample receives defaults; unknown keys fail; invalid timezone, reset time, weekly weekday, warning ordering, and missing password fail; and `ADMIN_PASSWORD` is obtained only through `lookupEnv`.

```go
func TestParseRejectsUnknownField(t *testing.T) {
    _, err := Parse([]byte("version: 1\nunknown: true\n"), func(string) (string, bool) { return "", false })
    if err == nil || !strings.Contains(err.Error(), "field unknown not found") { t.Fatalf("unexpected error: %v", err) }
}
```

- [ ] **Step 3: Verify the tests fail because `Parse` is absent**

Run: `go test ./internal/config`

Expected: build failure naming undefined `Parse` and configuration types.

- [ ] **Step 4: Implement typed configuration, defaults, and validation**

Use `yaml.NewDecoder`, `KnownFields(true)`, and a second decode expecting `io.EOF`. Define `Duration` with `UnmarshalYAML`, `Config`, `Server`, `PolicyConfig`, `Rule`, `Enforcement`, `HTTP`, and `Storage`. Resolve the named password environment variable during `Parse`, but keep it in an unexported runtime field so serialization and API DTOs cannot reveal it.

```go
func Parse(data []byte, lookup func(string) (string, bool)) (Config, error) {
    cfg := defaults()
    dec := yaml.NewDecoder(bytes.NewReader(data))
    dec.KnownFields(true)
    if err := dec.Decode(&cfg); err != nil { return Config{}, fmt.Errorf("decode config: %w", err) }
    if err := expectEOF(dec); err != nil { return Config{}, err }
    if err := cfg.validate(lookup); err != nil { return Config{}, err }
    return cfg, nil
}
```

- [ ] **Step 5: Add the disabled sample and run tests**

Run: `go test ./internal/config`

Expected: PASS, including parsing `config.example.yaml` with a fake password lookup.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/config config.example.yaml
git commit -m "feat: add strict guard configuration"
```

### Task 2: Policy Resolution And Fixed Periods

**Files:**
- Create: `internal/domain/types.go`
- Create: `internal/policy/service.go`
- Create: `internal/policy/service_test.go`

- [ ] **Step 1: Write failing tests for inheritance and calendar boundaries**

Table tests must cover daily 04:00 boundaries, weekly Monday 04:00 boundaries, exact reset instants, Asia/Shanghai conversion, a DST spring-forward and fall-back timezone, an override that changes only `Limit`, and an exempt override.

```go
func TestDailyPeriodBeforeResetUsesPreviousDay(t *testing.T) {
    svc := mustService(t, "Asia/Shanghai")
    got := svc.Period(ruleDaily("04:00"), time.Date(2026, 7, 10, 3, 0, 0, 0, mustLocation(t)))
    want := time.Date(2026, 7, 9, 4, 0, 0, 0, mustLocation(t))
    if !got.Start.Equal(want) { t.Fatalf("start=%v want=%v", got.Start, want) }
}
```

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/policy`

Expected: build failure because policy service does not exist.

- [ ] **Step 3: Implement domain types and policy service**

Define `ResolvedPolicy`, `Period`, `Player`, `Usage`, `WarningState`, `EnforcementState`, and `Snapshot`. `Resolve(userID)` copies the default, overlays only non-nil override fields, and returns `Enabled=false` for exempt users. `Period(rule, now)` uses local calendar construction, converts boundaries to UTC for persistence, and builds a key from a SHA-256 hash of canonical policy identity plus UTC start.

- [ ] **Step 4: Verify GREEN and commit**

Run: `go test ./internal/policy`

Expected: PASS.

```bash
git add internal/domain internal/policy
git commit -m "feat: resolve policies and fixed periods"
```

### Task 3: Transactional SQLite Repository

**Files:**
- Create: `internal/store/migrations.go`
- Create: `internal/store/sqlite.go`
- Create: `internal/store/sqlite_test.go`

- [ ] **Step 1: Write failing migration and persistence tests**

Open a temporary database, assert WAL and foreign keys, upsert a player, add usage idempotently within a transaction, insert a unique warning event, append enforcement events, close/reopen, and verify all state remains. Also force a transaction rollback and assert no partial usage or event row exists.

```go
func TestReconcileRollsBackUsageAndEventsTogether(t *testing.T) {
    repo := openTemp(t)
    err := repo.WithTx(context.Background(), func(tx *Tx) error {
        if err := tx.AddUsage(sampleUsage(30*time.Second)); err != nil { return err }
        return errors.New("stop")
    })
    if err == nil { t.Fatal("expected rollback") }
    if got := repo.UsageCount(t.Context()); got != 0 { t.Fatalf("rows=%d", got) }
}
```

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/store`

Expected: build failure because repository APIs are undefined.

- [ ] **Step 3: Implement migrations and repository methods**

Register `modernc.org/sqlite`, open with one writer connection, enable `_pragma=foreign_keys(1)`, `_pragma=journal_mode(WAL)`, and a busy timeout. Apply embedded ordered SQL migrations in a transaction. Store durations as integer milliseconds and timestamps as RFC 3339 UTC. Expose transaction-scoped methods required by guard reconciliation and read methods required by the API.

- [ ] **Step 4: Verify GREEN and commit**

Run: `go test ./internal/store`

Expected: PASS including reopen and rollback tests.

```bash
git add internal/store
git commit -m "feat: persist guard state in sqlite"
```

### Task 4: Deterministic Accounting Engine

**Files:**
- Create: `internal/guard/service.go`
- Create: `internal/guard/service_test.go`

- [ ] **Step 1: Write failing observation tests**

Use an injected fake clock and in-memory repository. Cover first observation adding zero, a continuous second observation adding elapsed time, a missing player breaking continuity, explicit poll failure clearing all continuity, a gap above `max_observation_gap`, process recreation adding no downtime, a reset crossing split into two period increments, disabled and exempt policies adding no usage, and reduced live limits using existing usage.

```go
func TestFirstObservationDoesNotChargeTime(t *testing.T) {
    h := newHarness(t)
    h.observe(player("steam_1"), at("2026-07-10T00:00:00Z"))
    if got := h.currentUsage("steam_1"); got != 0 { t.Fatalf("usage=%s", got) }
}
```

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/guard -run 'Test(First|Continuous|Failed|Gap|Reset|Disabled|Exempt)'`

Expected: build failure because `guard.Service` is absent.

- [ ] **Step 3: Implement accounting without external side effects**

Maintain only last successful online observations in memory. `Observe(ctx, now, players)` computes increments, splits across boundaries, and commits player metadata and usage before returning decisions. `PollFailed()` clears continuity. Never infer unobserved online time.

- [ ] **Step 4: Verify GREEN and commit**

Run: `go test ./internal/guard`

Expected: accounting tests PASS.

```bash
git add internal/guard
git commit -m "feat: account observed player time"
```

### Task 5: Warning And Enforcement Decisions

**Files:**
- Modify: `internal/guard/service.go`
- Modify: `internal/guard/service_test.go`

- [ ] **Step 1: Write failing warning and kick tests**

Cover crossing one threshold, jumping across multiple thresholds selecting only the latest relevant warning, successful warning deduplication, failed warning retry backoff, first kick at the limit, successful kick suppression while absent, kick on reconnect, failed kick exponential backoff capped at the configured maximum, and a new period clearing old enforcement state.

```go
func TestReconnectOverLimitProducesAnotherKick(t *testing.T) {
    h := overLimitHarness(t)
    h.observeOnlineAndKickSuccess()
    h.observeOffline()
    got := h.observeOnline()
    if len(got.Kicks) != 1 || got.Kicks[0].UserID != "steam_1" { t.Fatalf("kicks=%v", got.Kicks) }
}
```

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/guard -run 'Test(Warning|Kick|Reconnect|NewPeriod)'`

Expected: assertions fail because decisions and retry state are not implemented.

- [ ] **Step 3: Implement persisted, idempotent decisions**

Within the reconciliation transaction, create pending warning records using `(user_id, period_key, threshold_ms)` uniqueness and compute enforcement eligibility from the latest attempt. Add `RecordWarningResult` and `RecordKickResult` methods that sanitize errors, increment attempts, and schedule the next attempt. Mark a successful kick with the current online generation; a later offline-to-online transition creates a new generation eligible for enforcement.

- [ ] **Step 4: Verify GREEN and commit**

Run: `go test ./internal/guard`

Expected: PASS.

```bash
git add internal/guard internal/store
git commit -m "feat: decide warnings and enforcement"
```

### Task 6: Palworld REST Client

**Files:**
- Create: `internal/palworld/client.go`
- Create: `internal/palworld/client_test.go`

- [ ] **Step 1: Write failing HTTP contract tests**

Use `httptest.Server` to assert Basic Auth, `GET /v1/api/players`, response fields `name`, `accountName`, `playerId`, and `userId`, `POST /v1/api/announce` with `{message}`, and `POST /v1/api/kick` with `{userid,message}`. Cover non-2xx status, malformed JSON, response size limit, timeout, and error messages that do not contain credentials.

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/palworld`

Expected: build failure because `Client` is absent.

- [ ] **Step 3: Implement the bounded REST client**

Construct requests with context, JSON content type, Basic Auth username `admin`, the secret password, and a shared timeout-enabled `http.Client`. Limit response bodies, require 2xx, decode strict expected response shapes, and close bodies on every path.

- [ ] **Step 4: Verify GREEN and commit**

Run: `go test ./internal/palworld`

Expected: PASS.

```bash
git add internal/palworld
git commit -m "feat: add Palworld REST client"
```

### Task 7: Poll Reconciliation And Side Effects

**Files:**
- Create: `internal/poller/poller.go`
- Create: `internal/poller/poller_test.go`

- [ ] **Step 1: Write failing reconciliation tests**

Fake the Palworld client and guard service. Assert a successful list flows through observation then sends warning/kick decisions and records each result; a list failure calls `PollFailed` and performs no side effects; a repository/observation error performs no side effects; and one slow cycle never overlaps another.

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/poller`

Expected: build failure because `Poller` is absent.

- [ ] **Step 3: Implement `Run` and `RunOnce`**

Use interfaces local to the poller package. `Run` owns one ticker and invokes `RunOnce` serially. `RunOnce` lists players, obtains persisted decisions, renders validated templates, performs announcements and kicks, and records outcomes. Update a mutex-protected status snapshot with last attempt, last success, sanitized error, and online count.

- [ ] **Step 4: Verify GREEN and commit**

Run: `go test ./internal/poller -race`

Expected: PASS with no race reports.

```bash
git add internal/poller
git commit -m "feat: reconcile polls and enforcement effects"
```

### Task 8: Read-Only HTTP API

**Files:**
- Create: `internal/api/server.go`
- Create: `internal/api/server_test.go`

- [ ] **Step 1: Write failing route and redaction tests**

Test `/healthz`, `/readyz`, `/api/v1/status`, `/api/v1/players`, `/api/v1/players/{userId}`, `/api/v1/policies`, unknown users/routes, malformed escaped IDs, request IDs, content type, method rejection, and absence of `ADMIN_PASSWORD`, the actual password, authorization values, and IP fields in every JSON body.

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/api`

Expected: build failure because handler construction is absent.

- [ ] **Step 3: Implement stable DTOs and handlers**

Use Go 1.22+ `http.ServeMux` patterns, explicit DTO structs, `json.Encoder`, whole-millisecond durations, RFC 3339 UTC times, a bounded request ID, security headers, `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, and `IdleTimeout`. Depend only on status, policy, and query interfaces.

- [ ] **Step 4: Verify GREEN and commit**

Run: `go test ./internal/api`

Expected: PASS.

```bash
git add internal/api
git commit -m "feat: expose internal read-only API"
```

### Task 9: Application Wiring And Configuration Reload

**Files:**
- Create: `internal/app/app.go`
- Create: `internal/app/app_test.go`
- Create: `cmd/playtime-guard/main.go`

- [ ] **Step 1: Write failing lifecycle and reload tests**

Start with temporary config/database and a fake Palworld server. Assert initial readiness is false then true after a successful poll; a valid atomic config replacement changes resolved policy; invalid replacement retains the prior policy and exposes reload error; cancellation stops polling, shuts HTTP down, and closes SQLite.

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/app`

Expected: build failure because application assembly is absent.

- [ ] **Step 3: Implement application lifecycle**

`app.New(configPath)` loads configuration, constructs repository/client/services, and returns an `App`. `Run(ctx)` starts HTTP, poller, and a low-frequency file fingerprint watcher in an `errgroup`-equivalent implemented with standard goroutines and an error channel. Reload only after a complete successful read and validation. `main` parses `-config`, installs SIGINT/SIGTERM cancellation, configures JSON `slog`, and exits nonzero on fatal startup or runtime error. A `healthcheck <url>` subcommand performs a short bounded HTTP request and exits nonzero unless `/healthz` returns 2xx.

- [ ] **Step 4: Verify GREEN and commit**

Run: `go test ./internal/app ./cmd/playtime-guard`

Expected: PASS.

```bash
git add internal/app cmd/playtime-guard
git commit -m "feat: assemble playtime guard service"
```

### Task 10: Container And Stack Integration

**Files:**
- Create: `Dockerfile`
- Create: `.dockerignore`
- Create: `.gitignore`
- Create: `README.md`
- Modify: `../sidecars.yaml`

- [ ] **Step 1: Add a container smoke test command and verify it fails before the image exists**

Run: `docker build -t palworld-playtime-guard:test .`

Expected: FAIL because `Dockerfile` does not exist.

- [ ] **Step 2: Create a pinned multi-stage image**

Use `golang:1.26.4-alpine` for dependency download, tests, and `CGO_ENABLED=0` build. Use a minimal non-root runtime with CA certificates and timezone data, copy only the binary and sample configuration, declare `/data`, expose `8080`, and define a binary-based healthcheck command such as `playtime-guard healthcheck http://127.0.0.1:8080/healthz` so no shell or curl is required.

- [ ] **Step 3: Document operation and integrate compose**

Add `palworld-playtime-guard` to `../sidecars.yaml` with build context `./playtime-guard`, `env_file: .env.palworld`, read-only config mount, data directory mount, `expose: 8080`, dependency on Palworld health, restart policy, and container healthcheck. Do not add `ports`. Add `.gitignore` entries for `config.yaml`, `data/`, and local binaries. Document copying `config.example.yaml` to `config.yaml`, keeping `enabled: false` for the first boot, inspecting status from another container, and explicitly enabling policy after review.

- [ ] **Step 4: Build, validate compose, and run a disabled smoke test**

Run:

```bash
docker build -t palworld-playtime-guard:test .
docker compose -f ../compose.yaml config --quiet
docker run --rm -d --name palworld-playtime-guard-test -e ADMIN_PASSWORD=test -v "$PWD/config.example.yaml:/app/config.yaml:ro" -v "$PWD/.tmp-data:/data" -p 127.0.0.1:18080:8080 palworld-playtime-guard:test
```

Expected: image builds, compose exits 0, `/healthz` responds without leaking the password, and the container shuts down cleanly. Remove the smoke container and `.tmp-data` afterward.

- [ ] **Step 5: Commit**

```bash
git add Dockerfile .dockerignore .gitignore README.md
git commit -m "build: containerize and integrate playtime guard"
```

The parent stack is not a Git repository, so `../sidecars.yaml` is verified but cannot be included in this repository's commit.

### Task 11: Full Verification And Deployment

**Files:**
- Modify: `README.md` only if verification reveals an operational mismatch.

- [ ] **Step 1: Format and run static checks**

Run:

```bash
gofmt -w cmd internal
go vet ./...
go test -race ./...
```

Expected: all commands exit 0.

- [ ] **Step 2: Verify the repository and compose diff**

Run:

```bash
git diff --check
git status --short
docker compose -f ../compose.yaml config --services
docker compose -f ../compose.yaml config --quiet
```

Expected: no whitespace errors, only intended repository changes remain, the new service is listed, and compose validation succeeds. Inspect `../sidecars.yaml` directly to confirm `8080` is exposed without a published host port; do not print resolved compose environment because it contains the existing administrator password.

- [ ] **Step 3: Create runtime configuration and start disabled**

Copy `config.example.yaml` to the gitignored `config.yaml`, create `data/`, and start only the new service:

```bash
docker compose -f ../compose.yaml up -d --build palworld-playtime-guard
docker compose -f ../compose.yaml ps palworld-playtime-guard
docker compose -f ../compose.yaml logs --tail=100 palworld-playtime-guard
```

Expected: container is healthy, successfully polls Palworld, reports policy disabled, and emits no warning or kick request.

- [ ] **Step 4: Final commit if verification changed tracked files**

```bash
git add README.md
git commit -m "docs: clarify verified guard operation"
```

Skip this commit when verification did not require a tracked documentation change.
