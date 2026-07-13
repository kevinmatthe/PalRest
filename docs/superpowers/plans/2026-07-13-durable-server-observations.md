# Durable Server Observations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make server metric and document transitions atomic, idempotent after ambiguous commits, restorable across restarts, and numerically lossless for large JSON integers.

**Architecture:** SQLite is authoritative for the latest server state. New repository methods atomically insert the primary metric/document occurrence and optional immutable activity event, compare exact replays, and reject stale or mismatched observations. The observation service consults durable latest state before deriving each transition; its in-memory state only tracks newer unchanged-document timestamps within the current process.

**Tech Stack:** Go, `database/sql`, modernc SQLite, `encoding/json`, `math/big`, existing poller/app services.

---

### Task 1: Extend schema v9 with transition occurrence history

**Files:**
- Modify: `internal/store/migrations.go`
- Modify: `internal/store/sqlite_test.go`

- [ ] **Step 1: Write failing fresh-schema and v8-upgrade tests**

Assert `server_metric_samples.event_id` exists, `server_document_observations` exists with `(kind, observed_at)` primary key, and `server_document_observations_kind_time` exists after both a fresh open and a v8-to-v9 migration.

- [ ] **Step 2: Run the migration tests and verify RED**

Run: `go test ./internal/store -run 'TestOpenMigration(Create|Upgrade)' -count=1`

Expected: FAIL because the occurrence table/index and metric event column do not exist.

- [ ] **Step 3: Extend `schemaV9`**

Add nullable `event_id` to metrics and create:

```sql
CREATE TABLE server_document_observations (
    kind TEXT NOT NULL,
    observed_at TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    event_id TEXT,
    PRIMARY KEY(kind, observed_at),
    FOREIGN KEY(kind, content_hash) REFERENCES server_documents(kind, content_hash),
    FOREIGN KEY(event_id) REFERENCES activity_events(id)
);
CREATE INDEX server_document_observations_kind_time
ON server_document_observations(kind, observed_at);
```

- [ ] **Step 4: Run migration tests and verify GREEN**

Run: `go test ./internal/store -run 'TestOpenMigration(Create|Upgrade)' -count=1`

- [ ] **Step 5: Commit**

Commit: `feat: add durable server transition occurrences`

### Task 2: Add atomic, idempotent repository transitions and latest reads

**Files:**
- Modify: `internal/store/observations.go`
- Modify: `internal/store/observations_test.go`

- [ ] **Step 1: Write failing metric transition tests**

Cover atomic metric+event insertion, exact replay success, replay event/payload mismatch rejection, stale rejection, validation of every nonnegative field, and `LatestServerMetrics` after close/reopen.

- [ ] **Step 2: Verify metric tests RED**

Run: `go test ./internal/store -run 'Test(ServerMetricObservation|LatestServerMetrics)' -count=1`

- [ ] **Step 3: Implement metric atomic API**

Add:

```go
type ServerMetricObservation struct {
    At time.Time
    Metrics domain.ServerMetrics
    Event *ActivityEvent
}

func (r *Repository) RecordServerMetricObservation(context.Context, ServerMetricObservation) error
func (r *Repository) LatestServerMetrics(context.Context) (time.Time, domain.ServerMetrics, error)
```

Inside one transaction, compare an equal timestamp row field-for-field including the associated stored event, reject stale/mismatch, otherwise insert the optional event and metric. Keep `RecordServerMetrics` as a compatibility wrapper with no event.

- [ ] **Step 4: Verify metric tests GREEN**

Run: `go test ./internal/store -run 'Test(ServerMetricObservation|LatestServerMetrics|ObservationRecordServerMetrics)' -count=1`

- [ ] **Step 5: Write failing document transition tests**

Cover first A, A→B, B→A, two blob hashes/three occurrences/two events, exact replay, mismatch/stale rejection, atomic rollback on event conflict, and latest joined canonical data after reopen.

- [ ] **Step 6: Verify document tests RED**

Run: `go test ./internal/store -run 'Test(ServerDocumentObservation|LatestServerDocument)' -count=1`

- [ ] **Step 7: Implement document atomic API**

Add:

```go
type ServerDocumentObservation struct {
    Kind string
    At time.Time
    Canonical []byte
    Hash string
    Event *ActivityEvent
}

type ServerDocumentSnapshot struct {
    Kind string
    At time.Time
    Canonical []byte
    Hash string
}

func (r *Repository) RecordServerDocumentObservation(context.Context, ServerDocumentObservation) (bool, error)
func (r *Repository) LatestServerDocument(context.Context, string) (ServerDocumentSnapshot, error)
```

Compare exact occurrence replays including the event row, prove canonical bytes for an existing hash, create an occurrence only on hash transition, and preserve `RecordServerDocument` compatibility.

- [ ] **Step 8: Run all store tests and commit**

Run: `go test ./internal/store -count=1`

Commit: `feat: atomically persist server observations`

### Task 3: Make the observation service repository-authoritative

**Files:**
- Split/modify: `internal/observation/server.go`
- Create: `internal/observation/server_metrics.go`
- Create: `internal/observation/server_documents.go`
- Modify: `internal/observation/server_test.go`

- [ ] **Step 1: Write failing ambiguous-commit and reopen tests**

Wrap a real repository so the first atomic call delegates successfully and then returns an error. Retry the service at the same or next timestamp and assert success with no duplicate. Close/reopen a temp DB for metrics/info/settings, call `Restore`, and prove restart/version/settings transitions compare with the durable prior baseline.

- [ ] **Step 2: Verify service tests RED**

Run: `go test ./internal/observation -run 'TestServer.*(Ambiguous|Reopen|Restore)' -count=1`

- [ ] **Step 3: Replace pending state with atomic writes**

Change the narrow repository interface to the new atomic/read methods. Add `Restore(context.Context) error`. Before every write, reconcile against latest durable state; exact durable replay succeeds, stale/mismatch fails, and a newer sample derives at most one safe event passed into the atomic repository method.

- [ ] **Step 4: Add recurrent transition tests**

For info and settings, record A→B→A and assert two safe events plus latest A. Assert payloads contain only hashes, safe versions, and summary fields.

- [ ] **Step 5: Run observation tests and commit**

Run: `go test -race ./internal/observation -count=1`

Commit: `fix: make server observation transitions durable`

### Task 4: Preserve canonical numeric precision

**Files:**
- Modify: `internal/palworld/client.go`
- Modify: `internal/palworld/client_test.go`
- Create: `internal/observation/server_canonical.go`
- Modify: `internal/observation/server_test.go`

- [ ] **Step 1: Write failing client/canonical tests**

Assert distinct integers above `2^53` remain distinct `json.Number` values, safe official integers remain `float64`, equivalent safe exponent/decimal forms canonicalize identically, negative zero canonicalizes to zero, and NaN/Inf are rejected.

- [ ] **Step 2: Verify numeric tests RED**

Run: `go test ./internal/palworld ./internal/observation -run 'Test.*(Large|Canonical|NegativeZero|Exponent)' -count=1`

- [ ] **Step 3: Implement exact normalization**

In the client, parse `json.Number` as an exact integer first; retain it when outside `[-(2^53-1), 2^53-1]`, otherwise preserve existing float64 compatibility. In canonicalization, use `math/big.Rat` for exact integer/decimal/exponent equivalence, emit a normalized `json.Number`, collapse all zero forms to `0`, and never mutate input.

- [ ] **Step 4: Run client and observation tests and commit**

Run: `go test ./internal/palworld ./internal/observation -count=1`

Commit: `fix: preserve exact Palworld setting numbers`

### Task 5: Wire restore and deterministic integration completion

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`
- Modify: `internal/poller/server_sampler.go`
- Modify: `internal/poller/poller.go`

- [ ] **Step 1: Write failing app restart/integration tests**

Require `App.New` to restore server baselines before poller construction. Replace fixed sleeps with a deadline loop that reads `LatestServerMetrics` and `LatestServerDocument`; cancel and join the poller only after all durable writes are visible.

- [ ] **Step 2: Verify app test RED**

Run: `go test ./internal/app -run 'TestNewWiresUnified|TestNewRestoresServer' -count=1`

- [ ] **Step 3: Wire restore and consolidate defaults**

Call `serverObservations.Restore(context.Background())` during startup and close the repository on failure. Keep observation-owned policy defaults; stop passing poller options whose values equal poller defaults unless the configured request timeout is smaller.

- [ ] **Step 4: Run final verification**

Run:

```sh
gofmt -w internal/store internal/observation internal/palworld internal/app internal/poller
go test -race ./...
go vet ./...
git diff --check
```

- [ ] **Step 5: Commit**

Commit: `feat: restore durable server observation baselines`
