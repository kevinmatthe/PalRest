# Palworld REST and Unified Timeline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend the current poll path with complete Palworld REST observations, independently sampled server metrics and configuration versions, an immutable business event journal, and administrator-only timeline APIs with sensitive-read auditing.

**Architecture:** Keep `/players` as the only Guard-critical poll. A focused `observation.Service` persists REST-derived facts and trajectory samples in SQLite, while a separate slow sampler collects `/metrics`, `/info`, and `/settings` without changing Guard success semantics. Existing analytics remains compatible; new administrator queries read the event journal and always write an access-audit record in the same repository call.

**Tech Stack:** Go 1.24, `net/http`, `modernc.org/sqlite`, React 19, TypeScript, Vitest, existing cookie-based administrator session.

---

## File Structure

- Modify `internal/domain/types.go`: complete REST player fields and server observation types.
- Modify `internal/palworld/client.go`: bounded JSON GET helper and typed `/players`, `/metrics`, `/info`, `/settings` methods.
- Modify `internal/palworld/client_test.go`: request, decode, compatibility, and error-isolation tests.
- Modify `internal/store/migrations.go`: schema v9 for events, trajectory samples, metrics, server document versions, and access audit.
- Modify `internal/store/sqlite.go`: register migration v9.
- Create `internal/store/observations.go`: atomic observation writes and administrator timeline queries.
- Create `internal/store/observations_test.go`: repository behavior, retention-ready indexes, redaction, and audit tests.
- Create `internal/observation/service.go`: event/trajectory derivation from successful player observations.
- Create `internal/observation/service_test.go`: jitter, movement, gap, join/leave, and rollback tests.
- Modify `internal/poller/poller.go`: invoke the observation recorder only after successful `/players`, and run independent slow sampling.
- Modify `internal/poller/poller_test.go`: prove endpoint failures are isolated from Guard behavior.
- Modify `internal/app/app.go`: construct and wire the observation service.
- Modify `internal/api/server.go`: administrator timeline interfaces and routes.
- Create `internal/api/timeline.go`: validation, authorization, timeline responses, and audit-aware repository call.
- Create `internal/api/timeline_test.go`: public denial, admin success, validation, redaction, and audit tests.
- Modify `webui/src/api.ts`: administrator timeline types and request helper.
- Create `webui/src/components/PlayerTimeline.tsx`: Phase 1 tabular timeline before map replay.
- Create `webui/src/components/PlayerTimeline.test.tsx`: authorization, event source, stale, and gap rendering.
- Modify `webui/src/main.tsx`: administrator-only Timeline navigation.
- Modify `webui/src/styles.css`: responsive timeline presentation.
- Modify `README.md`: endpoints, collection semantics, sensitivity, and migration version.

## Task 1: Complete the Palworld REST Domain and Client

**Files:**
- Modify: `internal/domain/types.go`
- Modify: `internal/palworld/client.go`
- Test: `internal/palworld/client_test.go`

- [ ] **Step 1: Write failing decode tests for all read-only endpoints**

Add a table-driven server test that returns official-schema fixtures and asserts exact typed values:

```go
func TestReadOnlyEndpointsDecodeOfficialSchemas(t *testing.T) {
    mux := http.NewServeMux()
    mux.HandleFunc("GET /players", func(w http.ResponseWriter, _ *http.Request) {
        io.WriteString(w, `{"players":[{"name":"Kevin","accountName":"kevin","playerId":"ABC","userId":"steam_1","ip":"192.0.2.1","ping":28.5,"location_x":123.25,"location_y":-99.5,"level":41,"building_count":119}]}`)
    })
    mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
        io.WriteString(w, `{"serverfps":58,"currentplayernum":1,"serverframetime":17.2,"maxplayernum":32,"uptime":3600,"basecampnum":2,"days":126}`)
    })
    mux.HandleFunc("GET /info", func(w http.ResponseWriter, _ *http.Request) {
        io.WriteString(w, `{"version":"v0.7.2","servername":"Home","description":"Family","worldguid":"WORLD"}`)
    })
    mux.HandleFunc("GET /settings", func(w http.ResponseWriter, _ *http.Request) {
        io.WriteString(w, `{"Difficulty":"None","ExpRate":1.0,"ServerPlayerMaxNum":32,"RESTAPIEnabled":true}`)
    })
    server := httptest.NewServer(mux)
    defer server.Close()
    client := New(server.URL, "secret", time.Second)
    players, err := client.ListPlayers(context.Background())
    if err != nil { t.Fatal(err) }
    if got := players[0]; got.IP != "192.0.2.1" || got.Ping != 28.5 || got.LocationX != 123.25 || got.Level != 41 || got.BuildingCount != 119 { t.Fatalf("player=%+v", got) }
    metrics, err := client.Metrics(context.Background())
    if err != nil || metrics.ServerFPS != 58 || metrics.BaseCampNum != 2 { t.Fatalf("metrics=%+v err=%v", metrics, err) }
    info, err := client.Info(context.Background())
    if err != nil || info.WorldGUID != "WORLD" { t.Fatalf("info=%+v err=%v", info, err) }
    settings, err := client.Settings(context.Background())
    if err != nil || settings.Values["ServerPlayerMaxNum"] != float64(32) { t.Fatalf("settings=%+v err=%v", settings, err) }
}
```

- [ ] **Step 2: Run the client tests and verify RED**

Run: `go test ./internal/palworld -run 'TestReadOnlyEndpointsDecodeOfficialSchemas'`

Expected: FAIL because the extended player fields and client methods do not exist.

- [ ] **Step 3: Add typed domain values and a bounded GET decoder**

Add these fields/types in `internal/domain/types.go`:

```go
type Player struct {
    UserID string `json:"user_id"`; PlayerID string `json:"player_id"`
    Name string `json:"name"`; AccountName string `json:"account_name"`
    IP string `json:"-"`; Ping float64 `json:"-"`
    LocationX float64 `json:"-"`; LocationY float64 `json:"-"`
    Level int `json:"-"`; BuildingCount int `json:"-"`
    LastOnline time.Time `json:"last_online"`
}
type ServerMetrics struct {
    ServerFPS int `json:"serverfps"`; CurrentPlayerNum int `json:"currentplayernum"`
    ServerFrameTime float64 `json:"serverframetime"`; MaxPlayerNum int `json:"maxplayernum"`
    UptimeSeconds int64 `json:"uptime"`; BaseCampNum int `json:"basecampnum"`; Days int `json:"days"`
}
type ServerInfo struct { Version, ServerName, Description, WorldGUID string }
type ServerSettings struct { Values map[string]any }
```

Implement `getJSON`, `Metrics`, `Info`, and `Settings` in `internal/palworld/client.go`. Decode settings with `json.Decoder.UseNumber`, normalize numbers deliberately, and reject trailing JSON. Keep error messages body-free so credentials and server response details cannot leak.

- [ ] **Step 4: Run all client tests**

Run: `go test ./internal/palworld`

Expected: PASS.

- [ ] **Step 5: Commit the REST client expansion**

```bash
git add internal/domain/types.go internal/palworld/client.go internal/palworld/client_test.go
git commit -m "feat: read Palworld server observation endpoints"
```

## Task 2: Add the Observation Schema

**Files:**
- Modify: `internal/store/migrations.go`
- Modify: `internal/store/sqlite.go`
- Test: `internal/store/sqlite_test.go`

- [ ] **Step 1: Write a failing migration test for schema v9**

Extend the migration test to assert these tables and indexes exist after opening a fresh database and after upgrading an explicit v8 fixture:

```go
for _, table := range []string{"activity_events", "trajectory_samples", "server_metric_samples", "server_documents", "sensitive_access_audit"} {
    var name string
    if err := repo.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name); err != nil {
        t.Fatalf("missing %s: %v", table, err)
    }
}
```

- [ ] **Step 2: Run the migration test and verify RED**

Run: `go test ./internal/store -run 'Test.*Migration'`

Expected: FAIL with a missing v9 table.

- [ ] **Step 3: Add schema v9 and migration registration**

Define `schemaV9` with:

```sql
CREATE TABLE activity_events (
  id TEXT PRIMARY KEY, event_type TEXT NOT NULL,
  subject_type TEXT NOT NULL, subject_id TEXT NOT NULL,
  occurred_at TEXT NOT NULL, observed_at TEXT NOT NULL,
  source TEXT NOT NULL, source_ref TEXT NOT NULL,
  correlation_id TEXT NOT NULL, confidence TEXT NOT NULL,
  schema_version INTEGER NOT NULL, payload_json TEXT NOT NULL
);
CREATE INDEX activity_events_subject_time ON activity_events(subject_type, subject_id, occurred_at, id);
CREATE TABLE trajectory_samples (
  id INTEGER PRIMARY KEY AUTOINCREMENT, user_id TEXT NOT NULL,
  segment_id TEXT NOT NULL, observed_at TEXT NOT NULL,
  x REAL NOT NULL, y REAL NOT NULL, ping REAL NOT NULL,
  level INTEGER NOT NULL, source_ref TEXT NOT NULL,
  UNIQUE(user_id, observed_at)
);
CREATE INDEX trajectory_user_time ON trajectory_samples(user_id, observed_at);
CREATE TABLE server_metric_samples (
  observed_at TEXT PRIMARY KEY, server_fps INTEGER NOT NULL,
  current_player_num INTEGER NOT NULL, server_frame_time REAL NOT NULL,
  max_player_num INTEGER NOT NULL, uptime_seconds INTEGER NOT NULL,
  base_camp_num INTEGER NOT NULL, game_days INTEGER NOT NULL
);
CREATE TABLE server_documents (
  kind TEXT NOT NULL, content_hash TEXT NOT NULL,
  observed_at TEXT NOT NULL, canonical_json TEXT NOT NULL,
  PRIMARY KEY(kind, content_hash)
);
CREATE TABLE sensitive_access_audit (
  id INTEGER PRIMARY KEY AUTOINCREMENT, actor TEXT NOT NULL,
  action TEXT NOT NULL, subject_type TEXT NOT NULL, subject_id TEXT NOT NULL,
  range_start TEXT, range_end TEXT, outcome TEXT NOT NULL,
  requested_at TEXT NOT NULL
);
CREATE INDEX sensitive_audit_actor_time ON sensitive_access_audit(actor, requested_at);
```

Register migration 9 after v8 in `Repository.migrate`.

- [ ] **Step 4: Run store tests**

Run: `go test ./internal/store`

Expected: PASS, including fresh and upgraded databases.

- [ ] **Step 5: Commit the migration**

```bash
git add internal/store/migrations.go internal/store/sqlite.go internal/store/sqlite_test.go
git commit -m "feat: add unified observation schema"
```

## Task 3: Implement Atomic Observation Persistence and Queries

**Files:**
- Create: `internal/store/observations.go`
- Create: `internal/store/observations_test.go`

- [ ] **Step 1: Write failing repository tests**

Cover one atomic observation containing two events and one trajectory sample, duplicate event IDs rolling back the whole transaction, time-ordered timeline reads, metric upsert rejection for older timestamps, canonical server-document deduplication, sensitive query auditing, and bounded deletion of raw rows older than a cutoff while newer rows remain.

Use these concrete types in the test:

```go
obs := store.PlayerObservationWrite{Events: []store.ActivityEvent{{
    ID: "evt-1", EventType: "player_joined", SubjectType: "player", SubjectID: "steam_1",
    OccurredAt: now, ObservedAt: now, Source: "palworld_rest", SourceRef: "poll-1",
    CorrelationID: "poll-1", Confidence: "observed", SchemaVersion: 1, PayloadJSON: `{}`,
}}, Trajectories: []store.TrajectorySample{{
    UserID: "steam_1", SegmentID: "segment-1", ObservedAt: now,
    X: 123.25, Y: -99.5, Ping: 28.5, Level: 41, SourceRef: "poll-1",
}}}
```

- [ ] **Step 2: Run the observation store tests and verify RED**

Run: `go test ./internal/store -run 'TestObservation|TestTimeline|TestSensitive'`

Expected: FAIL because `observations.go` and its types do not exist.

- [ ] **Step 3: Implement focused write/query methods**

Create:

```go
func (r *Repository) RecordPlayerObservation(ctx context.Context, write PlayerObservationWrite) error
func (r *Repository) RecordServerMetrics(ctx context.Context, at time.Time, metrics domain.ServerMetrics) error
func (r *Repository) RecordServerDocument(ctx context.Context, kind string, at time.Time, canonical []byte, hash string) (bool, error)
func (r *Repository) ReadSensitivePlayerTimeline(ctx context.Context, actor, userID string, start, end time.Time, limit int) ([]ActivityEvent, []TrajectorySample, error)
func (r *Repository) CleanupRawObservations(ctx context.Context, cutoff time.Time, limit int) (deleted int, err error)
```

Validate non-empty IDs, finite coordinates/ping, `end > start`, and `1 <= limit <= 2000`. `ReadSensitivePlayerTimeline` must write an audit row with outcome `success`, `not_found`, or `error`; use one transaction for the query and audit commit so a returned successful response cannot lack its audit record. `CleanupRawObservations` deletes at most `limit` rows from each raw table in short transactions and never deletes access-audit or aggregate rows.

- [ ] **Step 4: Run repository and race tests**

Run: `go test -race ./internal/store`

Expected: PASS.

- [ ] **Step 5: Commit observation persistence**

```bash
git add internal/store/observations.go internal/store/observations_test.go
git commit -m "feat: persist Palworld observation timeline"
```

## Task 4: Derive Events and Trajectory Segments

**Files:**
- Create: `internal/observation/service.go`
- Create: `internal/observation/service_test.go`

- [ ] **Step 1: Write failing service tests**

Use an in-memory recorder fake and deterministic ID generator to cover:

- first observation creates `player_joined` and the first trajectory point;
- unchanged coordinates inside both thresholds create no point;
- movement beyond 100 world units creates a point;
- five minutes without movement creates a heartbeat point;
- a gap above `maxGap` starts a new segment and never connects coordinates;
- disappearance creates `player_left`;
- recorder failure leaves the in-memory baseline unchanged so retry derives the same transition.

Construct the service with:

```go
svc := observation.New(recorder, 75*time.Second, 100, 5*time.Minute, 90*24*time.Hour, func() string {
    sequence++
    return fmt.Sprintf("id-%d", sequence)
})
```

- [ ] **Step 2: Run the service tests and verify RED**

Run: `go test ./internal/observation`

Expected: FAIL because the package does not exist.

- [ ] **Step 3: Implement conservative derivation**

Implement:

```go
type Recorder interface {
    RecordPlayerObservation(context.Context, store.PlayerObservationWrite) error
    CleanupRawObservations(context.Context, time.Time, int) (int, error)
}
type Service struct { /* mutex, recorder, thresholds, last successful state */ }
func (s *Service) Observe(ctx context.Context, at time.Time, players []domain.Player, correlationID string) error
```

Normalize/sort unique user IDs. Treat non-finite coordinates and `(0,0)` as absent. Derive into local variables, persist, and update the baseline only after persistence succeeds. Use Euclidean world-coordinate distance only; map projection belongs to Phase 2. After a successful observation, request bounded cleanup at most once per 24 hours using `at.Add(-rawRetention)` and a batch size of 500; log cleanup failure without changing observation success.

- [ ] **Step 4: Run observation tests with race detection**

Run: `go test -race ./internal/observation`

Expected: PASS.

- [ ] **Step 5: Commit event derivation**

```bash
git add internal/observation/service.go internal/observation/service_test.go
git commit -m "feat: derive player events and trajectory segments"
```

## Task 5: Preserve Guard Fast-Path Semantics in the Poller

**Files:**
- Modify: `internal/poller/poller.go`
- Modify: `internal/poller/poller_test.go`

- [ ] **Step 1: Write failing poll orchestration tests**

Add fakes for `Metrics`, `Info`, `Settings`, and the observation recorder. Prove:

1. `/players` success invokes Analytics, observation, and Guard in that order.
2. observation persistence failure prevents Guard for that cycle and sets poll error.
3. `/metrics` failure does not prevent a successful `/players` Guard cycle.
4. `/players` failure never invokes observation or Guard, even if `/metrics` succeeds.
5. `/info` and `/settings` are sampled immediately and then no more than once per five minutes.

- [ ] **Step 2: Run focused poller tests and verify RED**

Run: `go test ./internal/poller -run 'Test.*Observation|Test.*Metrics|Test.*ServerDocuments'`

Expected: FAIL because the poller has no new collaborators.

- [ ] **Step 3: Add explicit interfaces and independent sampling**

Add:

```go
type PlayerObserver interface { Observe(context.Context, time.Time, []domain.Player, string) error }
type ServerReader interface {
    Metrics(context.Context) (domain.ServerMetrics, error)
    Info(context.Context) (domain.ServerInfo, error)
    Settings(context.Context) (domain.ServerSettings, error)
}
type ServerObservationRecorder interface {
    RecordMetrics(context.Context, time.Time, domain.ServerMetrics) error
    RecordInfo(context.Context, time.Time, domain.ServerInfo) error
    RecordSettings(context.Context, time.Time, domain.ServerSettings) error
}
```

Generate one correlation ID per player poll. Keep metric/document errors in separate status fields or structured logs; do not overwrite `LastSuccess` or `OnlineCount` when only an optional endpoint fails.

- [ ] **Step 4: Run poller and existing app tests**

Run: `go test -race ./internal/poller ./internal/app`

Expected: PASS.

- [ ] **Step 5: Commit poll integration**

```bash
git add internal/poller/poller.go internal/poller/poller_test.go
git commit -m "feat: collect independent Palworld observations"
```

## Task 6: Wire the Observation Service and Server Documents

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`
- Create: `internal/observation/server.go`
- Create: `internal/observation/server_test.go`

- [ ] **Step 1: Write failing wiring and canonicalization tests**

Test stable canonical JSON and SHA-256 hashes for logically identical settings maps with different insertion order. Extend the app construction test to verify the poller receives the same Palworld client as player and server reader and a non-nil player observer.

- [ ] **Step 2: Run tests and verify RED**

Run: `go test ./internal/observation ./internal/app`

Expected: FAIL because server observation normalization and wiring are absent.

- [ ] **Step 3: Implement server normalization and app wiring**

`internal/observation/server.go` implements the poller recorder interface and converts typed metrics directly, while info/settings use canonical JSON plus SHA-256. It emits `server_version_changed`, `server_settings_changed`, and `server_restarted` events; restart is detected only when uptime decreases between successful metric samples.

Construct both observation services in `app.New` and pass them to `poller.New`. Existing configuration reload rules remain unchanged.

- [ ] **Step 4: Run backend tests**

Run: `go test -race ./internal/observation ./internal/app ./internal/poller`

Expected: PASS.

- [ ] **Step 5: Commit application wiring**

```bash
git add internal/app/app.go internal/app/app_test.go internal/observation/server.go internal/observation/server_test.go
git commit -m "feat: wire unified Palworld observations"
```

## Task 7: Add Administrator Timeline APIs and Access Audit

**Files:**
- Modify: `internal/api/server.go`
- Create: `internal/api/timeline.go`
- Create: `internal/api/timeline_test.go`

- [ ] **Step 1: Write failing HTTP contract tests**

Cover:

```text
GET /api/v1/admin/players/steam_1/timeline?start=2026-07-01T00:00:00Z&end=2026-07-02T00:00:00Z&limit=500
GET /api/v1/admin/server/metrics?start=2026-07-01T00:00:00Z&end=2026-07-02T00:00:00Z
GET /api/v1/admin/server/documents?kind=info|settings
```

Assert unauthenticated requests return 401, invalid/missing ranges return 400, valid admin requests return ordered segments/events with `source`, `occurred_at`, `observed_at`, and `confidence`, and the fake repository receives actor `admin`. Assert JSON responses never serialize an `Authorization` header or administrator credential.

- [ ] **Step 2: Run API tests and verify RED**

Run: `go test ./internal/api -run 'TestAdminTimeline|TestAdminServerObservation'`

Expected: FAIL with route not found.

- [ ] **Step 3: Implement routes and strict validation**

Add an `ObservationQueries` interface to `server.go`, wire routes under `/api/v1/admin`, and implement response types in `timeline.go`. Reuse the existing `isAdmin` check. Require RFC3339 timestamps, `end > start`, a maximum 31-day range, and `limit` from 1 through 2000. Do not expose raw `payload_json`; decode only known event payload versions into typed response fields.

- [ ] **Step 4: Run API tests with race detection**

Run: `go test -race ./internal/api`

Expected: PASS.

- [ ] **Step 5: Commit administrator APIs**

```bash
git add internal/api/server.go internal/api/timeline.go internal/api/timeline_test.go
git commit -m "feat: expose audited player timeline APIs"
```

## Task 8: Add the Phase 1 Timeline Web View

**Files:**
- Modify: `webui/src/api.ts`
- Create: `webui/src/components/PlayerTimeline.tsx`
- Create: `webui/src/components/PlayerTimeline.test.tsx`
- Modify: `webui/src/main.tsx`
- Modify: `webui/src/main.test.tsx`
- Modify: `webui/src/styles.css`

- [ ] **Step 1: Write failing component and navigation tests**

Mock a timeline response containing joined, warning, movement sample, and left items. Assert chronological rendering, source badges, snapshot/observed confidence, explicit gap separators, player selection, date-range validation, and no Timeline navigation for unauthenticated users.

- [ ] **Step 2: Run frontend tests and verify RED**

Run: `cd webui && npm test -- --run src/components/PlayerTimeline.test.tsx src/main.test.tsx`

Expected: FAIL because the component and API helper do not exist.

- [ ] **Step 3: Implement typed API and accessible timeline**

Add:

```ts
export type TimelineItem = {
  id: string; type: string; occurred_at: string; observed_at: string;
  source: 'palworld_rest' | 'guard' | 'save_snapshot';
  confidence: 'observed' | 'snapshot_derived'; summary: string;
};
export type TrajectorySample = { segment_id: string; observed_at: string; x: number; y: number; ping: number; level: number };
export type PlayerTimelineResponse = { user_id: string; start: string; end: string; events: TimelineItem[]; trajectory: TrajectorySample[] };
```

`PlayerTimeline` uses semantic lists and `<time>`, provides player and local-date controls, and reports loading/error/empty/stale states. Phase 1 shows coordinates in an administrator detail row; Phase 2 replaces that detail with the real map replay while retaining the event list.

- [ ] **Step 4: Run frontend tests and build**

Run: `cd webui && npm test && npm run build`

Expected: PASS and Vite build completes without TypeScript errors.

- [ ] **Step 5: Commit the Timeline UI**

```bash
git add webui/src/api.ts webui/src/components/PlayerTimeline.tsx webui/src/components/PlayerTimeline.test.tsx webui/src/main.tsx webui/src/main.test.tsx webui/src/styles.css
git commit -m "feat: add administrator player timeline"
```

## Task 9: Document, Verify, and Prepare Phase 2

**Files:**
- Modify: `README.md`
- Modify: `config.example.yaml`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Document concrete behavior**

Document the three new administrator endpoints, complete REST fields, endpoint-independent failures, trajectory gaps, 90-day raw retention target, sensitive access auditing, and schema version v9. Add observation defaults to `config.example.yaml`:

```yaml
observation:
  server_document_interval: 5m
  trajectory_min_distance: 100
  trajectory_max_interval: 5m
  raw_retention: 90d
```

Add `Observation` to `config.Config` with `ServerDocumentInterval Duration`, `TrajectoryMinDistance float64`, `TrajectoryMaxInterval Duration`, and `RawRetention Duration`. Defaults are exactly the YAML values above. Validation rejects non-positive intervals/retention and a negative distance. Add table cases in `internal/config/config_test.go` for defaults, explicit values, each invalid value, and unknown keys.

- [ ] **Step 2: Run formatting and complete backend verification**

Run: `gofmt -w internal && go test -race ./... && go vet ./...`

Expected: formatting produces no remaining diff on a second run; tests and vet pass.

- [ ] **Step 3: Run complete frontend verification**

Run: `cd webui && npm test && npm run build`

Expected: tests pass and production assets build.

- [ ] **Step 4: Build the container image**

Run: `docker build -t palworld-playtime-guard:phase1-test .`

Expected: image builds successfully with the existing non-root runtime behavior.

- [ ] **Step 5: Review schema and API compatibility**

Run: `git diff HEAD~8 -- internal/store/migrations.go internal/api README.md config.example.yaml`

Expected: only additive tables/routes/configuration are present; existing public response fields and routes remain compatible.

- [ ] **Step 6: Commit documentation and final verification state**

```bash
git add README.md config.example.yaml internal/config
git commit -m "docs: describe unified Palworld observations"
```

## Phase 1 Completion Gate

Phase 1 is complete only when:

- `/players` failure behavior remains identical to the existing conservative Guard semantics;
- optional REST endpoint failure cannot stop successful Guard accounting;
- timeline and exact coordinates require an authenticated administrator;
- every successful or failed sensitive timeline repository query records an audit outcome;
- no IP address or coordinate appears in a public response or log assertion fixture;
- all backend race tests, frontend tests/build, vet, and Docker build pass;
- the worktree is clean except for changes explicitly retained by the user.

After this gate, create and execute the separate Phase 2 plan for real map assets, coordinate fixtures, trajectory downsampling, and synchronized replay.
