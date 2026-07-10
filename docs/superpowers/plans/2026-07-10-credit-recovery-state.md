# Credit Recovery State Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop credit recovery during online play and expose the actual amount recovered during the most recently completed offline interval.

**Architecture:** Persist the last settled recovery amount beside the existing credit balance in `policy_states`. Make online/offline state explicit when calculating credit, settle and persist recovery only on an offline-to-online transition, and map the new state through snapshots, API DTOs, and the player usage UI.

**Tech Stack:** Go 1.24, SQLite, React 19, TypeScript, Vitest.

---

### Task 1: Persist Last Settled Recovery

**Files:**
- Modify: `internal/store/migrations.go`
- Modify: `internal/store/sqlite.go`
- Modify: `internal/store/sqlite_test.go`

- [ ] **Step 1: Write a failing SQLite round-trip test**

Add `LastCreditRecovered time.Duration` to the desired `PolicyState` test fixture and assert the repository returns it unchanged after `UpsertPolicyState` and `PolicyState`.

```go
state := PolicyState{
    UserID: "credit-user", PolicyRevision: "revision", Strategy: "credit",
    Credit: 45 * time.Minute, LastCreditAt: now.Add(-time.Hour),
    LastCreditRecovered: 15 * time.Minute, UpdatedAt: now,
}
```

- [ ] **Step 2: Run the focused store test and verify RED**

Run: `go test ./internal/store -run TestPolicyStateRoundTrip -v`

Expected: FAIL because `PolicyState` and the schema do not contain `LastCreditRecovered`.

- [ ] **Step 3: Add migration and repository mapping**

Add schema migration version 5:

```sql
ALTER TABLE policy_states
ADD COLUMN last_credit_recovered_ms INTEGER NOT NULL DEFAULT 0
CHECK (last_credit_recovered_ms >= 0);
```

Extend `PolicyState`, SELECT/Scan, and INSERT/UPDATE mapping with `last_credit_recovered_ms`.

- [ ] **Step 4: Run store tests and verify GREEN**

Run: `go test ./internal/store -v`

Expected: PASS.

- [ ] **Step 5: Commit persistence**

```bash
git add internal/store/migrations.go internal/store/sqlite.go internal/store/sqlite_test.go
git -c commit.gpgsign=false commit -m "feat: persist last credit recovery"
```

### Task 2: Enforce Offline-Only Recovery

**Files:**
- Modify: `internal/domain/types.go`
- Modify: `internal/guard/service.go`
- Modify: `internal/guard/service_test.go`

- [ ] **Step 1: Write failing guard tests**

Add tests for continuous online observations and online snapshot reads:

```go
func TestCreditDoesNotRecoverWhileContinuouslyOnline(t *testing.T) {
    // Configure 30m recovery per hour and 1h maximum.
    // Observe at t0, t0+30m, and t0+60m while online.
    // Assert remaining credit is 0, not 30m.
}

func TestOnlineCreditSnapshotDoesNotRecoverBetweenPolls(t *testing.T) {
    // Consume 30m, advance injected snapshot clock by 30m without an offline observation,
    // query Snapshot, and assert remaining stays 30m.
}
```

Extend the existing offline recovery test to assert `LastCreditRecovered == 30*time.Minute`, then add a capped case where only 10 minutes of capacity is available and the recorded recovery is 10 minutes.

- [ ] **Step 2: Run focused guard tests and verify RED**

Run: `go test ./internal/guard -run 'TestCredit(DoesNotRecoverWhileContinuouslyOnline|PolicyRecoversWhileOfflineAndConsumesOnline|RecoveryRecordsActualCappedAmount)|TestOnlineCreditSnapshotDoesNotRecoverBetweenPolls' -v`

Expected: FAIL because current refresh and snapshot paths accrue without checking online state and snapshots expose no last recovery.

- [ ] **Step 3: Implement explicit recovery eligibility**

Determine continuity before strategy refresh. Pass `recoverCredit = !continuous` into strategy refresh, pass `recoverCredit = false` to online usage reads, and `recoverCredit = !snapshot.Online` to snapshot reads.

Change accrual to return actual recovered credit:

```go
func accrueCredit(state store.PolicyState, rule domain.ResolvedPolicy, now time.Time) (store.PolicyState, time.Duration) {
    before := state.Credit
    // existing proportional recovery and cap
    return state, state.Credit - before
}
```

When settling an offline-to-online transition, assign the returned amount to `state.LastCreditRecovered`. Add `LastCreditRecovered` to `domain.PlayerSnapshot`.

- [ ] **Step 4: Run guard tests and verify GREEN**

Run: `go test ./internal/guard -v`

Expected: PASS.

- [ ] **Step 5: Commit state-machine fix**

```bash
git add internal/domain/types.go internal/guard/service.go internal/guard/service_test.go
git -c commit.gpgsign=false commit -m "fix: recover credit only while offline"
```

### Task 3: Expose and Render Credit Recovery

**Files:**
- Modify: `internal/api/server.go`
- Modify: `internal/api/server_test.go`
- Modify: `webui/src/api.ts`
- Modify: `webui/src/main.tsx`
- Modify: `webui/src/styles.css`
- Create: `webui/src/components/PlayerCredit.test.tsx`

- [ ] **Step 1: Write failing API tests**

Create one credit snapshot and one fixed-window snapshot. Assert the credit DTO includes `credit_available_ms` and `last_credit_recovered_ms`, while the fixed-window DTO omits both fields.

```go
if !strings.Contains(creditBody, `"credit_available_ms":2700000`) ||
   !strings.Contains(creditBody, `"last_credit_recovered_ms":900000`) {
    t.Fatalf("body=%s", creditBody)
}
```

- [ ] **Step 2: Run API tests and verify RED**

Run: `go test ./internal/api -run TestPlayerCreditRecoveryFields -v`

Expected: FAIL because the DTO has no credit-specific fields.

- [ ] **Step 3: Map API and TypeScript fields**

Add pointer/omitempty fields to `playerDTO` and populate them only when `snapshot.Policy.Strategy == "credit"`. Add optional `credit_available_ms` and `last_credit_recovered_ms` properties to `Player`.

- [ ] **Step 4: Write a failing frontend rendering test**

Extract the player usage presentation into an exported `PlayerUsage` component and test:

```tsx
render(<PlayerUsage player={creditPlayer} />);
expect(screen.getByText('45m available')).toBeInTheDocument();
expect(screen.getByText('Last recovery 15m')).toBeInTheDocument();
```

Also assert zero renders `No recovery recorded`.

- [ ] **Step 5: Run frontend test and verify RED**

Run: `npm test --prefix webui -- src/components/PlayerCredit.test.tsx`

Expected: FAIL because the component and fields do not exist.

- [ ] **Step 6: Implement credit usage presentation**

Render credit available as the primary label, retain the existing progress bar, and add the most recent recovery as secondary copy. Non-credit strategies retain the existing used/remaining presentation.

- [ ] **Step 7: Run API/frontend tests and build**

Run: `go test ./internal/api -v && npm test --prefix webui && npm run build --prefix webui`

Expected: PASS.

- [ ] **Step 8: Commit API and UI**

```bash
git add internal/api/server.go internal/api/server_test.go webui/src/api.ts webui/src/main.tsx webui/src/styles.css webui/src/components/PlayerCredit.test.tsx
git -c commit.gpgsign=false commit -m "feat: show recent credit recovery"
```

### Task 4: Full Verification

**Files:**
- Modify: `docs/superpowers/plans/2026-07-10-credit-recovery-state.md`

- [ ] **Step 1: Run complete verification**

```bash
go test ./...
go test -race ./...
go vet ./...
npm test --prefix webui
npm run build --prefix webui
git diff --check
```

Expected: all commands exit zero.

- [ ] **Step 2: Mark this plan complete and commit it**

```bash
git add docs/superpowers/plans/2026-07-10-credit-recovery-state.md
git -c commit.gpgsign=false commit -m "docs: complete credit recovery plan"
```
