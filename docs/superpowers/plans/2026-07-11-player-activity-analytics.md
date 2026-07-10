# Player Activity Analytics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add policy-independent online sessions, 5-minute concurrency analytics, daily player totals, rankings, and responsive 7/30-day WebUI charts with smooth updates.

**Architecture:** A focused `analytics.Service` consumes every successful poll before policy enforcement and persists one atomic observation through repository methods. SQLite stores sessions, concurrency buckets, and local-date player aggregates; additive read-only API endpoints return presentation-ready series. The React UI keeps operational data and analytics data in separate views and draws lightweight SVG charts.

**Tech Stack:** Go 1.24, `database/sql`, modernc SQLite, `net/http`, React 19, TypeScript, SVG, CSS, Vitest, Testing Library.

---

## File Map

- Create `internal/analytics/service.go`: observation state machine, interval acceptance, timezone/day and bucket splitting, retention trigger.
- Create `internal/analytics/service_test.go`: deterministic service behavior and repository-contract tests.
- Create `internal/store/analytics.go`: analytics transactions, aggregate queries, and cleanup.
- Create `internal/store/analytics_test.go`: SQLite persistence, ordering, rollback, and retention tests.
- Modify `internal/store/migrations.go`: schema version 6 SQL.
- Modify `internal/store/sqlite.go`: apply migration 6.
- Modify `internal/poller/poller.go`: notify analytics after successful player listing.
- Modify `internal/poller/poller_test.go`: recorder invocation and failure behavior.
- Modify `internal/app/app.go`: construct and wire analytics into poller and API.
- Modify `internal/app/app_test.go`: application wiring expectations.
- Create `internal/api/analytics.go`: query parsing, DTO conversion, handlers.
- Modify `internal/api/server.go`: analytics interface, dependency, and routes.
- Modify `internal/api/server_test.go`: summary/activity endpoint contract tests.
- Modify `webui/src/api.ts`: analytics response types and request helpers.
- Create `webui/src/components/AnalyticsDashboard.tsx`: analytics controls, metrics, ranking, and composition.
- Create `webui/src/components/AnalyticsDashboard.test.tsx`: interactions, missing data, stale state, and responsive semantics.
- Create `webui/src/components/ActivityChart.tsx`: accessible SVG line/bar rendering and animated updates.
- Create `webui/src/components/ActivityChart.test.tsx`: path gaps, updates, and reduced-motion tests.
- Modify `webui/src/main.tsx`: Overview/Analytics navigation and analytics loading.
- Modify `webui/src/styles.css`: responsive analytics layout and motion rules.
- Modify `webui/src/test/setup.ts`: stable `matchMedia` test shim if not already present.
- Modify `README.md`: document analytics semantics and endpoints.

### Task 1: Add analytics schema migration

**Files:**
- Modify: `internal/store/migrations.go`
- Modify: `internal/store/sqlite.go`
- Modify: `internal/store/sqlite_test.go`

- [ ] **Step 1: Write the failing migration test**

Add `TestOpenMigratesAnalyticsSchema` to `internal/store/sqlite_test.go`. Open a fresh repository, query `schema_migrations` for version 6, then assert the three tables and unique open-session index exist:

```go
func TestOpenMigratesAnalyticsSchema(t *testing.T) {
    repo := openTestRepository(t)
    var version int
    require.NoError(t, repo.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version))
    require.Equal(t, 6, version)
    for _, name := range []string{"player_sessions", "concurrency_buckets", "player_daily_stats"} {
        var count int
        require.NoError(t, repo.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&count))
        require.Equal(t, 1, count, name)
    }
}
```

Use the file's existing repository helper and assertion style instead of adding `testify` if it is not already imported.

- [ ] **Step 2: Run the test and confirm RED**

Run: `go test ./internal/store -run TestOpenMigratesAnalyticsSchema -count=1`

Expected: FAIL because the maximum migration version is 5 and analytics tables are absent.

- [ ] **Step 3: Add schema version 6**

Append this migration to `internal/store/migrations.go`:

```go
const schemaV6 = `
CREATE TABLE player_sessions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT NOT NULL REFERENCES players(user_id) ON DELETE CASCADE,
    started_at TEXT NOT NULL,
    ended_at TEXT,
    last_observed_at TEXT NOT NULL,
    close_reason TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX player_sessions_one_open
ON player_sessions(user_id) WHERE ended_at IS NULL;
CREATE INDEX player_sessions_range
ON player_sessions(started_at, ended_at);

CREATE TABLE concurrency_buckets (
    bucket_start TEXT PRIMARY KEY,
    weighted_count_ms INTEGER NOT NULL DEFAULT 0 CHECK(weighted_count_ms >= 0),
    observed_ms INTEGER NOT NULL DEFAULT 0 CHECK(observed_ms >= 0),
    max_count INTEGER NOT NULL DEFAULT 0 CHECK(max_count >= 0),
    max_observed_at TEXT
);

CREATE TABLE player_daily_stats (
    user_id TEXT NOT NULL REFERENCES players(user_id) ON DELETE CASCADE,
    local_date TEXT NOT NULL,
    observed_ms INTEGER NOT NULL DEFAULT 0 CHECK(observed_ms >= 0),
    first_observed_at TEXT NOT NULL,
    last_observed_at TEXT NOT NULL,
    session_count INTEGER NOT NULL DEFAULT 0 CHECK(session_count >= 0),
    PRIMARY KEY(user_id, local_date)
);
CREATE INDEX player_daily_stats_range
ON player_daily_stats(local_date, observed_ms DESC, user_id);
`
```

In `Repository.migrate`, apply `schemaV6` when `version < 6`, then call `recordMigration(ctx, tx, 6)`.

- [ ] **Step 4: Run migration tests**

Run: `go test ./internal/store -run 'TestOpenMigratesAnalyticsSchema|TestMigration' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/migrations.go internal/store/sqlite.go internal/store/sqlite_test.go
git commit -m "feat: add analytics storage schema"
```

### Task 2: Implement atomic analytics persistence

**Files:**
- Create: `internal/store/analytics.go`
- Create: `internal/store/analytics_test.go`

- [ ] **Step 1: Write failing transaction tests**

Create tests for opening/closing one session, accumulating a bucket twice, upserting a daily total, and rollback. Use these input types in the tests:

```go
interval := store.AnalyticsInterval{
    Start: time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC),
    End: time.Date(2026, 7, 11, 12, 0, 30, 0, time.UTC),
    OnlineUserIDs: []string{"u1", "u2"},
    LocalDate: "2026-07-11",
}
err := repo.RecordAnalyticsObservation(ctx, store.AnalyticsObservation{
    At: interval.End,
    Players: players,
    JoinedUserIDs: []string{"u1", "u2"},
    Intervals: []store.AnalyticsInterval{interval},
})
```

Assert `weighted_count_ms=60000`, `observed_ms=30000`, `max_count=2`; each player receives `30000` daily milliseconds; and each joined player has one open session. Force a foreign-key error in a second observation and assert no bucket or session row from that transaction survives.

- [ ] **Step 2: Run the new store tests and confirm RED**

Run: `go test ./internal/store -run 'TestRecordAnalytics|TestAnalyticsRollback' -count=1`

Expected: FAIL because the analytics types and repository method do not exist.

- [ ] **Step 3: Implement write models and transaction**

Define focused persistence models in `internal/store/analytics.go`:

```go
type AnalyticsInterval struct {
    Start, End time.Time
    OnlineUserIDs []string
    LocalDate string
}

type AnalyticsObservation struct {
    At time.Time
    Players []domain.Player
    JoinedUserIDs, LeftUserIDs []string
    Intervals []AnalyticsInterval
}

func (r *Repository) RecordAnalyticsObservation(ctx context.Context, observation AnalyticsObservation) error {
    return r.WithTx(ctx, func(tx *Tx) error {
        // Upsert every observed player first, open joined sessions, update continuing
        // sessions, close left sessions, then accumulate every split interval.
        return tx.recordAnalyticsObservation(observation)
    })
}
```

Implement prepared SQL helpers that:

- use `Tx.UpsertPlayer` for observed players;
- insert joined sessions with `started_at=last_observed_at=observation.At`;
- update open sessions' `last_observed_at` for currently online users;
- close leaving sessions with `ended_at=observation.At, close_reason='observed_offline'`;
- upsert a bucket using `weighted_count_ms += onlineCount * durationMS`, `observed_ms += durationMS`, and update the maximum only when the new count is greater;
- upsert one daily row per online user and interval, incrementing `session_count` only for users in `JoinedUserIDs` on the interval containing `observation.At`.

Reject non-positive intervals and intervals spanning more than one UTC 5-minute bucket or local date. This keeps splitting responsibility in the service and makes malformed writes fail atomically.

- [ ] **Step 4: Run store tests**

Run: `go test ./internal/store -run 'TestRecordAnalytics|TestAnalyticsRollback' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/analytics.go internal/store/analytics_test.go
git commit -m "feat: persist analytics observations"
```

### Task 3: Implement the policy-independent analytics recorder

**Files:**
- Create: `internal/analytics/service.go`
- Create: `internal/analytics/service_test.go`

- [ ] **Step 1: Write failing service tests**

Use a fake repository capturing `store.AnalyticsObservation`. Cover first observation, a normal 30-second continuation, join/leave, a 90-second gap rejected against a 75-second maximum, a UTC bucket boundary, and midnight in `Asia/Shanghai`.

```go
func TestObserveSplitsBucketAndLocalMidnight(t *testing.T) {
    repo := &fakeRecorder{}
    location, _ := time.LoadLocation("Asia/Shanghai")
    service := analytics.New(repo, 75*time.Second, location)
    service.Observe(ctx, time.Date(2026, 7, 11, 15, 59, 50, 0, time.UTC), players("u1"))
    err := service.Observe(ctx, time.Date(2026, 7, 11, 16, 0, 20, 0, time.UTC), players("u1"))
    require.NoError(t, err)
    require.Equal(t, []time.Duration{10 * time.Second, 20 * time.Second}, intervalDurations(repo.last.Intervals))
    require.Equal(t, []string{"2026-07-11", "2026-07-12"}, intervalDates(repo.last.Intervals))
}
```

- [ ] **Step 2: Run tests and confirm RED**

Run: `go test ./internal/analytics -count=1`

Expected: FAIL because the package does not exist.

- [ ] **Step 3: Implement the state machine and splitting**

Define:

```go
type Recorder interface {
    RecordAnalyticsObservation(context.Context, store.AnalyticsObservation) error
}

type Service struct {
    mu sync.Mutex
    repo Recorder
    maxGap time.Duration
    location *time.Location
    lastAt time.Time
    online map[string]domain.Player
}

func New(repo Recorder, maxGap time.Duration, location *time.Location) *Service
func (s *Service) Observe(ctx context.Context, at time.Time, players []domain.Player) error
```

Within the lock, build `joined` and `left` sets against the last successful state. Only create duration intervals when `0 < at-lastAt <= maxGap`; the interval's online users are the players from the previous observation. Split repeatedly at the earliest of the next UTC 5-minute boundary, next local midnight, or final end. Persist first; only replace `lastAt` and `online` after repository success so a failed write can be retried consistently.

Add `Current()` returning the copied online IDs and `as_of` time for summary queries.

- [ ] **Step 4: Run service tests**

Run: `go test ./internal/analytics -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/analytics/service.go internal/analytics/service_test.go
git commit -m "feat: record player activity observations"
```

### Task 4: Wire recorder into polling and application startup

**Files:**
- Modify: `internal/poller/poller.go`
- Modify: `internal/poller/poller_test.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`

- [ ] **Step 1: Write failing poller wiring tests**

Add a fake `Analytics` implementation and assert successful player listings invoke it once before `guard.Observe`. Assert list failure invokes neither analytics nor guard. Assert analytics failure prevents guard processing and sets poll status error.

```go
type Analytics interface {
    Observe(context.Context, time.Time, []domain.Player) error
}
```

- [ ] **Step 2: Run the focused tests and confirm RED**

Run: `go test ./internal/poller ./internal/app -run 'Analytics|Wiring' -count=1`

Expected: FAIL because `poller.New` has no analytics dependency.

- [ ] **Step 3: Add the dependency and startup wiring**

Change `poller.New` to accept `analytics Analytics` after the guard argument. In `RunOnce`, call `p.analytics.Observe(ctx, now, players)` after listing and before guard observation; return and set status error on failure.

In `app.New`, load the configured policy timezone, construct `analytics.New(repo, cfg.Server.MaxObservationGap.Duration, location)`, retain it on `App`, and pass it to poller. Pass the same service and repository to the API constructor in Task 6.

- [ ] **Step 4: Run affected Go tests**

Run: `go test ./internal/poller ./internal/app -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/poller/poller.go internal/poller/poller_test.go internal/app/app.go internal/app/app_test.go
git commit -m "feat: wire analytics into polling"
```

### Task 5: Add analytics queries and 90-day cleanup

**Files:**
- Modify: `internal/store/analytics.go`
- Modify: `internal/store/analytics_test.go`
- Modify: `internal/analytics/service.go`
- Modify: `internal/analytics/service_test.go`

- [ ] **Step 1: Write failing query and cleanup tests**

Seed daily and bucket rows directly, then test:

- today/week ranking duration descending with name and user ID tie-breaks;
- 7/30-day buckets ordered ascending with explicit missing points added by the query service;
- selected-player daily totals;
- cleanup deletes closed sessions and aggregates before cutoff but retains an old open session;
- cleanup is triggered at most once per 24 hours and its failure does not fail observation.

- [ ] **Step 2: Run tests and confirm RED**

Run: `go test ./internal/store ./internal/analytics -run 'AnalyticsSummary|AnalyticsActivity|AnalyticsCleanup' -count=1`

Expected: FAIL because query and cleanup methods are absent.

- [ ] **Step 3: Implement query models and SQL**

Add these store models and methods:

```go
type RankingRow struct { UserID, Name string; Observed time.Duration }
type ConcurrencyBucket struct { Start time.Time; Average *float64; Max *int; Coverage float64 }
type DailyActivity struct { Date string; Observed time.Duration }

func (r *Repository) Ranking(ctx context.Context, startDate, endDate string) ([]RankingRow, error)
func (r *Repository) Concurrency(ctx context.Context, start, end time.Time) ([]ConcurrencyBucket, error)
func (r *Repository) PlayerDailyActivity(ctx context.Context, userID, startDate, endDate string) ([]DailyActivity, error)
func (r *Repository) CleanupAnalytics(ctx context.Context, cutoff time.Time, cutoffDate string, batchSize int) error
```

Calculate average as `weighted_count_ms / observed_ms`, coverage as `min(observed_ms / 300000, 1)`, and order rankings by `SUM(observed_ms) DESC, players.name COLLATE NOCASE, user_id`. Cleanup uses repeated `DELETE ... WHERE rowid IN (SELECT ... LIMIT ?)` statements inside one transaction and excludes sessions with `ended_at IS NULL`.

- [ ] **Step 4: Schedule non-fatal cleanup**

Give `analytics.Service` a `lastCleanup` field and a repository `CleanupAnalytics` capability. After a successful observation, when 24 hours elapsed, invoke cleanup with `at.AddDate(0, 0, -90)` and a batch size of 500. Log failure with `slog.Warn`; update `lastCleanup` so a persistent cleanup error cannot run every 30-second poll.

- [ ] **Step 5: Run query and service tests**

Run: `go test ./internal/store ./internal/analytics -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/store/analytics.go internal/store/analytics_test.go internal/analytics/service.go internal/analytics/service_test.go
git commit -m "feat: query and retain activity analytics"
```

### Task 6: Expose read-only analytics API endpoints

**Files:**
- Create: `internal/api/analytics.go`
- Modify: `internal/api/server.go`
- Modify: `internal/api/server_test.go`

- [ ] **Step 1: Write failing HTTP contract tests**

Create table tests for:

```text
GET /api/v1/analytics/summary
GET /api/v1/analytics/summary?ranking=week
GET /api/v1/analytics/summary?ranking=year     -> 400
GET /api/v1/analytics/activity
GET /api/v1/analytics/activity?range=30d&user_id=u1
GET /api/v1/analytics/activity?range=90d      -> 400
GET /api/v1/analytics/activity?user_id=missing -> 404
```

Assert JSON field names `online_count`, `as_of`, `today_observed_ms`, `peak_count`, `peak_at`, `active_players`, `ranking`, `range`, `timezone`, `concurrency`, and optional `player`.

- [ ] **Step 2: Run tests and confirm RED**

Run: `go test ./internal/api -run Analytics -count=1`

Expected: FAIL with route-not-found responses.

- [ ] **Step 3: Define API interfaces and routes**

In `server.go`, add an `Analytics` query interface and an `OnlineState` interface, store them on `Server`, extend `New`, and register:

```go
mux.HandleFunc("GET /api/v1/analytics/summary", server.getAnalyticsSummary)
mux.HandleFunc("GET /api/v1/analytics/activity", server.getAnalyticsActivity)
```

In `analytics.go`, parse `ranking` as `today|week` and `range` as `7d|30d`. Derive local date boundaries in the current policy timezone, query the repository, generate every expected 5-minute timestamp and local calendar date, and fill absent points with `null`. Map `store.ErrNotFound` for a supplied player to 404 and all other query failures to the existing `query_failed` 500 response.

- [ ] **Step 4: Run API and full Go tests**

Run: `go test ./internal/api -count=1 && go test ./... -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/analytics.go internal/api/server.go internal/api/server_test.go internal/app/app.go
git commit -m "feat: expose activity analytics API"
```

### Task 7: Add typed WebUI client and accessible chart primitives

**Files:**
- Modify: `webui/src/api.ts`
- Create: `webui/src/components/ActivityChart.tsx`
- Create: `webui/src/components/ActivityChart.test.tsx`
- Modify: `webui/src/test/setup.ts`

- [ ] **Step 1: Write failing chart tests**

Test that a line series with `[2, null, 4]` renders two separate SVG path segments, a bar series exposes date and duration through accessible labels, rerendering with a new point adds the `is-updating` class, and reduced motion suppresses it.

```tsx
render(<ActivityChart kind="line" label="Server concurrency" points={points} />);
expect(screen.getByRole('img', { name: /server concurrency/i })).toBeInTheDocument();
expect(screen.getAllByTestId('line-segment')).toHaveLength(2);
```

- [ ] **Step 2: Run the test and confirm RED**

Run: `cd webui && npm test -- ActivityChart.test.tsx`

Expected: FAIL because `ActivityChart` does not exist.

- [ ] **Step 3: Add API types and fetch helpers**

Define `AnalyticsSummary`, `RankingEntry`, `ConcurrencyPoint`, `PlayerActivity`, and `AnalyticsActivity` with the exact JSON fields from Task 6. Add:

```ts
export function getAnalyticsSummary(ranking: 'today' | 'week', signal?: AbortSignal) {
  return getJSON<AnalyticsSummary>(`/api/v1/analytics/summary?ranking=${ranking}`, signal);
}

export function getAnalyticsActivity(range: '7d' | '30d', userID?: string, signal?: AbortSignal) {
  const params = new URLSearchParams({ range });
  if (userID) params.set('user_id', userID);
  return getJSON<AnalyticsActivity>(`/api/v1/analytics/activity?${params}`, signal);
}
```

- [ ] **Step 4: Implement the SVG primitive**

`ActivityChart` accepts line concurrency points or daily bars, calculates a stable `viewBox`, splits line segments at null values, renders an accessible `<svg role="img">`, and provides text/tooltips for exact values. Track the previous serialized points in a ref; on changed data, set `is-updating` for 550ms unless `matchMedia('(prefers-reduced-motion: reduce)').matches`.

- [ ] **Step 5: Run chart tests**

Run: `cd webui && npm test -- ActivityChart.test.tsx`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add webui/src/api.ts webui/src/components/ActivityChart.tsx webui/src/components/ActivityChart.test.tsx webui/src/test/setup.ts
git commit -m "feat: add analytics chart primitives"
```

### Task 8: Build Analytics Dashboard and navigation

**Files:**
- Create: `webui/src/components/AnalyticsDashboard.tsx`
- Create: `webui/src/components/AnalyticsDashboard.test.tsx`
- Modify: `webui/src/main.tsx`
- Modify: `webui/src/styles.css`

- [ ] **Step 1: Write failing dashboard interaction tests**

Mock the two analytics request helpers and test:

- four metrics and ranking rows render;
- today/week buttons refetch summary;
- 7/30-day buttons refetch activity;
- selecting a known player requests its daily series;
- null buckets reach `ActivityChart` as gaps;
- an API error retains the previous data and shows the notice;
- tab controls use `aria-current="page"` and filter controls use `aria-pressed`.

- [ ] **Step 2: Run tests and confirm RED**

Run: `cd webui && npm test -- AnalyticsDashboard.test.tsx`

Expected: FAIL because the component does not exist.

- [ ] **Step 3: Implement `AnalyticsDashboard`**

Keep local `rankingPeriod`, `range`, and `selectedUserID` state. Accept `players`, `refreshKey`, and an error callback from `App`; fetch summary and activity together on first render and when filters change, abort obsolete requests, and preserve prior successful data during refresh. Compose metric cards, the server line chart, ordered ranking, searchable `<select>` player control, and the daily bar chart.

- [ ] **Step 4: Add navigation and loading ownership**

Change `view` in `main.tsx` to `'dashboard' | 'analytics' | 'policy'`. Render an Overview/Analytics tab group below the header whenever policy management is closed. Pass known players and `manualRefreshKey` into analytics; the global refresh button increments the same key so both views obey the current 10-second cadence.

- [ ] **Step 5: Add responsive and motion CSS**

Add `.analytics-metrics` with four desktop columns, `.analytics-main` with `minmax(0,2fr) minmax(18rem,1fr)`, single-column rules under the existing tablet breakpoint, and two then one metric columns on narrower screens. Give buttons/selects `min-height:44px`. Animate SVG series with `transform/opacity 550ms ease`, and disable all chart transitions inside `@media (prefers-reduced-motion: reduce)`.

- [ ] **Step 6: Run frontend tests and build**

Run: `cd webui && npm test && npm run build`

Expected: all Vitest suites PASS and Vite build completes without TypeScript errors.

- [ ] **Step 7: Commit**

```bash
git add webui/src/components/AnalyticsDashboard.tsx webui/src/components/AnalyticsDashboard.test.tsx webui/src/main.tsx webui/src/styles.css
git commit -m "feat: add player activity dashboard"
```

### Task 9: Document and verify the complete feature

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update operator documentation**

Add an Analytics section explaining that collection starts after deployment, includes all API-observed players regardless of policy, uses conservative gap handling, retains 90 days, treats missing data as unknown rather than zero, and exposes the two new GET endpoints with their query parameters.

- [ ] **Step 2: Run formatting and static checks**

Run:

```bash
gofmt -w internal/analytics internal/store/analytics.go internal/api/analytics.go internal/poller/poller.go internal/app/app.go
go vet ./...
```

Expected: `gofmt` produces no remaining diff on a second run and `go vet` exits 0.

- [ ] **Step 3: Run complete verification**

Run:

```bash
go test -race ./...
cd webui && npm test && npm run build
```

Expected: all Go race-enabled tests pass, all Vitest suites pass, and the production WebUI build succeeds.

- [ ] **Step 4: Inspect migration and API manually**

Start the service with a temporary SQLite database and query both endpoints. Confirm initial history is empty, current status includes `as_of`, and repeated polls begin populating 5-minute and daily points without requiring an enabled policy.

- [ ] **Step 5: Commit documentation**

```bash
git add README.md
git commit -m "docs: document player activity analytics"
```
