# Server Observation CAS State Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Preserve authoritative server baselines independently of raw retention and make concurrent writers classify transitions against an exact durable predecessor.

**Architecture:** Add a non-retained `server_observation_state` row for metrics and each document kind. Atomic writes compare an optional expected state token, insert raw history/event when required, and advance state in one transaction; conflicts return `ErrObservationConflict`, causing the service to reload, re-derive, and retry at most three times. Settings keep every JSON numeric lexeme as `json.Number`, while bounded canonical parsing supplies semantic equivalence.

**Tech Stack:** Go, `database/sql`, modernc SQLite, `encoding/json`, `math/big`, SHA-256.

---

### Task 1: Durable state and foreign-key schema

**Files:**
- Modify: `internal/store/migrations.go`
- Modify: `internal/store/sqlite_test.go`

- [ ] Add failing fresh/v8 migration tests for `server_observation_state`, event foreign keys, and event lookup indexes; run the named migration tests and confirm RED.
- [ ] Add a keyed state table containing `kind`, `observed_at`, nullable metric columns, document hash, and a shape check; add event foreign keys with default RESTRICT and indexes.
- [ ] Run store migration tests and commit `feat: add authoritative server observation state`.

### Task 2: CAS repository writes and retention-independent latest reads

**Files:**
- Modify: `internal/store/server_observations.go`
- Modify: `internal/store/observations.go`
- Modify: `internal/store/observations_test.go`

- [ ] Add failing tests proving unchanged A@t3 advances the watermark, B@t2 is rejected after reopen, two cleanups do not remove current state, and lower uptime after cleanup emits from the restored baseline.
- [ ] Add failing CAS tests using `Expected *ServerMetricToken` and `Expected *ServerDocumentToken`; assert a mismatch returns `ErrObservationConflict` and inserts no history or event.
- [ ] Implement state reads/writes inside the existing atomic transaction. Exact replay compares stored raw/event rows when present; unchanged documents advance state without an occurrence. Cleanup may delete raw metrics and unlinked events while document-occurrence events remain protected.
- [ ] Recompute `sha256(canonical)` inside the store and reject mismatched hashes; replace fixture hashes with `documentHash`-equivalent test helpers.
- [ ] Run store tests and commit `feat: add CAS server observation state`.

### Task 3: Bounded service conflict retries

**Files:**
- Modify: `internal/observation/server.go`
- Modify: `internal/observation/server_metrics.go`
- Modify: `internal/observation/server_documents.go`
- Split: `internal/observation/server_test.go`

- [ ] Add controlled interleaving tests for two services writing metric restart candidates and document A→B/C transitions; confirm RED because stale pre-derived events currently commit.
- [ ] Pass the loaded state token as `Expected`, catch only `ErrObservationConflict`, reload/re-derive, generate a stable event for that attempt, and stop after three conflicts.
- [ ] Preserve ambiguous exact replay by accepting the state token that names the already-committed observation.
- [ ] Split tests into `server_metrics_test.go`, `server_documents_test.go`, `server_canonical_test.go`, and `server_durability_test.go`; run race-enabled observation tests and commit `fix: retry server observation CAS conflicts`.

### Task 4: Exact bounded settings numbers

**Files:**
- Modify: `internal/palworld/client.go`
- Modify: `internal/palworld/client_test.go`
- Modify: `internal/observation/server_canonical.go`
- Modify: `internal/observation/server_canonical_test.go`

- [ ] Add failing end-to-end tests proving all settings numbers remain `json.Number`, equivalent decimal/exponent forms canonicalize together, `1e-400` differs from zero, and fractions above `2^53` remain distinct.
- [ ] Add failing rejection tests for more than 256 significant digits and exponent magnitude above 1024.
- [ ] Remove client float conversion. Validate the numeric grammar/limits before `big.Rat`; then normalize finite decimals with output bounded by the accepted digit/exponent limits.
- [ ] Run client/canonical tests and commit `fix: bound exact Palworld setting numbers`.

### Task 5: Integration and full verification

**Files:**
- Modify as required: `internal/app/app_test.go`, `internal/poller/*_test.go`

- [ ] Run migration/store/observation/client/app/poller tests uncached.
- [ ] Run `go test -race ./... -count=1`, `go vet ./...`, and `git diff --check`.
- [ ] Review schema/CAS/retention behavior against every review item, commit any final test-only maintenance, and report all SHAs.
