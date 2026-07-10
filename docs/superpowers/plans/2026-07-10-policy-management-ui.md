# Policy Management UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a password-manager-compatible administrator login modal and a responsive, typed Policy manager backed exclusively by SQLite after one-time initialization from code and optional YAML defaults.

**Architecture:** Split bootstrap Policy defaults from the complete runtime `config.Policy`, let `policy.Service` prefer an existing SQLite document and seed only an empty database, and keep the existing singleton document representation. Split the React monolith into focused authentication, Policy form, and duration-control modules; the dashboard remains readable without authentication and opens a dedicated master-detail management view for authenticated writes.

**Tech Stack:** Go 1.24, SQLite (`modernc.org/sqlite`), React 19, TypeScript, Vite 7, Vitest, Testing Library, Lucide React, CSS.

---

## File Structure

- `internal/config/config.go`: complete code defaults, partial YAML bootstrap types, merge logic, and validation.
- `internal/config/config_test.go`: omitted/partial Policy YAML and obsolete override rejection.
- `internal/policy/service.go`: database-authoritative load/seed behavior and atomic update ordering.
- `internal/policy/service_test.go`: database precedence, initial seed, and failed persistence behavior.
- `internal/api/server.go`: Policy source response and existing typed save contract.
- `internal/api/server_test.go`: source metadata and invalid save contracts.
- `internal/app/app.go`: ensure config hot reload never changes the database-backed Policy.
- `internal/app/app_test.go`: hot reload regression coverage.
- `config.example.yaml`: document YAML as optional bootstrap defaults without overrides.
- `webui/src/api.ts`: add Policy source and preserve nullable override fields.
- `webui/src/duration.ts`: pure millisecond/value/unit conversions.
- `webui/src/duration.test.ts`: duration conversion tests.
- `webui/src/components/AdminLoginModal.tsx`: standard accessible credential form.
- `webui/src/components/AdminLoginModal.test.tsx`: password-manager and modal-state tests.
- `webui/src/components/DurationField.tsx`: reusable positive duration input.
- `webui/src/components/PolicyManager.tsx`: master-detail navigation, draft ownership, add/delete/save flows.
- `webui/src/components/PolicyManager.test.tsx`: inheritance, conditional fields, selection, and draft tests.
- `webui/src/components/RuleForm.tsx`: global and override typed rule fields.
- `webui/src/main.tsx`: dashboard/view orchestration and removal of inline login/raw JSON editor.
- `webui/src/styles.css`: login modal, management layout, fields, responsive navigation, and touch sizing.
- `webui/package.json`, `webui/package-lock.json`: frontend test runner and DOM testing dependencies.

### Task 1: Code Defaults and Partial YAML Bootstrap

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `config.example.yaml`

- [ ] **Step 1: Write failing configuration tests**

Add tests that parse a minimal valid server configuration with no `policy`, a partial `policy.default.limit`, and an obsolete `policy.overrides` key. Assert the first two yield the complete safe defaults and the last fails as an unknown field.

```go
func bootstrapConfig(policy string) string {
    return `version: 1
server:
  base_url: http://palworld-server:8212/v1/api
  password_env: ADMIN_PASSWORD
  poll_interval: 30s
  request_timeout: 5s
  max_observation_gap: 75s
` + policy + `
enforcement:
  kick_message: "reset {{ .ResetAt }}"
  announce_message: "{{ .PlayerName }}: {{ .Remaining }}"
  kick_retry_initial: 15s
  kick_retry_max: 5m
http:
  listen: 0.0.0.0:8080
storage:
  path: /data/guard.db
`
}

func TestParseUsesCodePolicyDefaultsWhenPolicyIsOmitted(t *testing.T) {
    cfg, err := Parse([]byte(bootstrapConfig("")), env)
    if err != nil { t.Fatal(err) }
    if cfg.Policy.Timezone != "Asia/Shanghai" || cfg.Policy.Default.Strategy != "fixed_window" || cfg.Policy.Default.Limit.Duration != 2*time.Hour {
        t.Fatalf("policy=%+v", cfg.Policy)
    }
    if len(cfg.Policy.Default.WarningBefore) != 4 || cfg.Policy.Default.Enabled {
        t.Fatalf("default=%+v", cfg.Policy.Default)
    }
}

func TestParseOverlaysPartialYAMLPolicyOnCodeDefaults(t *testing.T) {
    cfg, err := Parse([]byte(bootstrapConfig("policy:\n  default:\n    enabled: true\n    limit: 90m\n")), env)
    if err != nil { t.Fatal(err) }
    if !cfg.Policy.Default.Enabled || cfg.Policy.Default.Limit.Duration != 90*time.Minute || cfg.Policy.Default.ResetAt != "04:00" {
        t.Fatalf("default=%+v", cfg.Policy.Default)
    }
}

func TestParseRejectsYAMLPolicyOverrides(t *testing.T) {
    _, err := Parse([]byte(bootstrapConfig("policy:\n  overrides:\n    player: { exempt: true }\n")), env)
    if err == nil || !strings.Contains(err.Error(), "overrides") { t.Fatalf("err=%v", err) }
}
```

- [ ] **Step 2: Run the focused tests and verify RED**

Run: `go test ./internal/config -run 'TestParse(UsesCodePolicyDefaults|OverlaysPartialYAMLPolicy|RejectsYAMLPolicyOverrides)' -v`

Expected: FAIL because omitted and partial Policy values do not currently produce a valid complete Policy, while YAML overrides are currently accepted.

- [ ] **Step 3: Implement bootstrap defaults and merge types**

Keep `Policy` as the complete runtime model. Decode the YAML `policy` key through a private pointer-based bootstrap structure so absence is distinguishable from explicit zero values, overlay it onto `DefaultPolicy()`, and expose the complete default for the Policy service.

```go
func DefaultPolicy() Policy {
    return Policy{
        Timezone: "Asia/Shanghai",
        Default: Rule{
            Enabled: false, Strategy: "fixed_window", Period: "daily", ResetAt: "04:00",
            Limit: Duration{2 * time.Hour},
            CooldownEvery: Duration{2 * time.Hour}, CooldownRest: Duration{30 * time.Minute},
            CreditRecoverEvery: Duration{time.Hour}, CreditRecoverAmount: Duration{30 * time.Minute}, CreditMax: Duration{3 * time.Hour},
            WarningBefore: []Duration{{30 * time.Minute}, {10 * time.Minute}, {5 * time.Minute}, {time.Minute}},
        },
        Overrides: map[string]RuleOverride{},
    }
}
```

Implement `UnmarshalYAML` on a private top-level decode model or `Config` so the public JSON/runtime shape remains unchanged and `KnownFields(true)` still rejects `policy.overrides`.

- [ ] **Step 4: Update the example configuration**

Remove `policy.overrides`, explain that `policy` is used only when SQLite has no Policy, and show a partial bootstrap block. Keep all runtime strategy examples in comments but state that ongoing edits belong in WebUI.

- [ ] **Step 5: Run config tests and verify GREEN**

Run: `go test ./internal/config -v`

Expected: PASS.

- [ ] **Step 6: Commit Task 1**

```bash
git add internal/config/config.go internal/config/config_test.go config.example.yaml
git -c commit.gpgsign=false commit -m "feat: add policy bootstrap defaults"
```

### Task 2: Database-Authoritative Policy Service

**Files:**
- Modify: `internal/policy/service.go`
- Modify: `internal/policy/service_test.go`
- Modify: `internal/app/app_test.go`

- [ ] **Step 1: Write failing service precedence tests**

Add this in-memory repository fake, then cover existing document precedence and write failure retaining the active Policy.

```go
type fakePolicyRepo struct {
    document string
    missing bool
    writes int
    writeErr error
}

func (f *fakePolicyRepo) PolicyDocument(context.Context) (string, error) {
    if f.missing { return "", store.ErrNotFound }
    return f.document, nil
}

func (f *fakePolicyRepo) UpsertPolicyDocument(_ context.Context, document string, _ time.Time) error {
    if f.writeErr != nil { return f.writeErr }
    f.document = document
    f.missing = false
    f.writes++
    return nil
}

func TestNewPrefersStoredPolicyOverSeed(t *testing.T) {
    stored := basePolicy()
    stored.Default.Limit = duration(4 * time.Hour)
    data, _ := config.MarshalPolicy(stored)
    repo := &fakePolicyRepo{document: string(data)}
    seed := basePolicy()
    seed.Default.Limit = duration(time.Hour)
    svc, err := New(repo, seed)
    if err != nil { t.Fatal(err) }
    if got := svc.Resolve("player").Limit; got != 4*time.Hour { t.Fatalf("limit=%v", got) }
    if repo.writes != 0 { t.Fatalf("writes=%d", repo.writes) }
}

func TestSetPolicyWriteFailureRetainsActivePolicy(t *testing.T) {
    repo := &fakePolicyRepo{missing: true}
    svc, err := New(repo, basePolicy())
    if err != nil { t.Fatal(err) }
    before := svc.Resolve("player").Revision
    repo.writeErr = errors.New("disk full")
    next := basePolicy(); next.Default.Limit = duration(3*time.Hour)
    if err := svc.SetPolicy(t.Context(), next); err == nil { t.Fatal("expected error") }
    if after := svc.Resolve("player").Revision; after != before { t.Fatalf("revision changed: %s", after) }
}
```

- [ ] **Step 2: Run the focused tests and verify RED**

Run: `go test ./internal/policy -run 'Test(NewPrefersStoredPolicyOverSeed|SetPolicyWriteFailureRetainsActivePolicy)' -v`

Expected: FAIL because the newly specified fake-repository behavior has not yet been integrated with the service test setup.

- [ ] **Step 3: Refactor service initialization and atomic updates**

Extract `loadOrSeed(ctx, repo, seed)` and `validatedPolicy(data)` helpers. Preserve the strict order validate → marshal → persist → load timezone → swap memory. Do not add any config-reload hook to `Service`.

- [ ] **Step 4: Add a hot-reload regression test**

Replace `TestReloadAppliesValidPolicyAndRetainsOldOnInvalid` with `TestReloadDoesNotReplaceDatabasePolicy`: store a Policy through `application.policies.SetPolicy`, edit YAML Policy values, call `application.reload()`, and assert `application.policies.Resolve("player")` is unchanged while the edited enforcement message is present in `application.CurrentConfig()`.

- [ ] **Step 5: Run Policy and app tests and verify GREEN**

Run: `go test ./internal/policy ./internal/app -v`

Expected: PASS.

- [ ] **Step 6: Commit Task 2**

```bash
git add internal/policy/service.go internal/policy/service_test.go internal/app/app_test.go
git -c commit.gpgsign=false commit -m "fix: make database policy authoritative"
```

### Task 3: Policy API Source and Validation Contract

**Files:**
- Modify: `internal/api/server.go`
- Modify: `internal/api/server_test.go`
- Modify: `webui/src/api.ts`

- [ ] **Step 1: Write failing API tests**

Assert both GET and successful PUT include `"source":"database"`, and an invalid strategy returns `400 invalid_policy` without calling the fake service setter. Change `fakePolicies` to a pointer with a `setCalls` counter.

```go
func TestPoliciesReportDatabaseSource(t *testing.T) {
    server := testServer()
    res := httptest.NewRecorder()
    server.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/policies", nil))
    if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"source":"database"`) {
        t.Fatalf("code=%d body=%s", res.Code, res.Body.String())
    }
}
```

- [ ] **Step 2: Run the API test and verify RED**

Run: `go test ./internal/api -run 'TestPoliciesReportDatabaseSource' -v`

Expected: FAIL because `source` is absent.

- [ ] **Step 3: Add source metadata and preserve nullable overrides**

Add `"source": "database"` in `policyResponse`. Keep pointer fields in `overrideDTO` so omitted values still mean inheritance. In `webui/src/api.ts`, add `source: 'database'` to `Policies`.

- [ ] **Step 4: Tighten validation tests for strategies and warning bounds**

Add table-driven API cases for unknown strategy, invalid weekly reset, zero duration, and warnings greater than or equal to the effective allowance. Implement missing checks in `config.ValidatePolicy` only after each failing case is observed.

- [ ] **Step 5: Run API and config tests and verify GREEN**

Run: `go test ./internal/api ./internal/config -v`

Expected: PASS.

- [ ] **Step 6: Commit Task 3**

```bash
git add internal/api/server.go internal/api/server_test.go internal/config/config.go internal/config/config_test.go webui/src/api.ts
git -c commit.gpgsign=false commit -m "feat: expose database policy source"
```

### Task 4: Frontend Test Harness and Duration Controls

**Files:**
- Modify: `webui/package.json`
- Modify: `webui/package-lock.json`
- Modify: `webui/vite.config.ts`
- Create: `webui/src/test/setup.ts`
- Create: `webui/src/duration.ts`
- Create: `webui/src/duration.test.ts`
- Create: `webui/src/components/DurationField.tsx`

- [ ] **Step 1: Check current Vitest and Testing Library documentation**

Use the Context7 skill before changing dependencies. Confirm the current Vite-compatible Vitest configuration, jsdom environment, and `@testing-library/jest-dom/vitest` setup import.

- [ ] **Step 2: Install the documented test dependencies**

Run the versions confirmed from current primary documentation:

```bash
cd webui
npm install --save-dev vitest jsdom @testing-library/react @testing-library/user-event @testing-library/jest-dom
```

Add scripts `"test": "vitest run"` and `"test:watch": "vitest"`; configure `test.environment = 'jsdom'` and `setupFiles = './src/test/setup.ts'`.

- [ ] **Step 3: Write failing duration conversion tests**

```ts
import { describe, expect, it } from 'vitest';
import { fromMilliseconds, toMilliseconds } from './duration';

describe('duration conversion', () => {
  it('converts hours to integer milliseconds', () => expect(toMilliseconds(1.5, 'hours')).toBe(5_400_000));
  it('chooses hours for exact hour values', () => expect(fromMilliseconds(7_200_000)).toEqual({ value: 2, unit: 'hours' }));
  it('chooses minutes otherwise', () => expect(fromMilliseconds(5_400_000)).toEqual({ value: 90, unit: 'minutes' }));
  it('rejects non-positive values', () => expect(() => toMilliseconds(0, 'minutes')).toThrow('positive'));
});
```

- [ ] **Step 4: Run the test and verify RED**

Run: `npm test -- src/duration.test.ts`

Expected: FAIL because `duration.ts` does not exist.

- [ ] **Step 5: Implement conversion helpers and reusable field**

```ts
export type DurationUnit = 'minutes' | 'hours';
export function toMilliseconds(value: number, unit: DurationUnit) {
  if (!Number.isFinite(value) || value <= 0) throw new Error('Duration must be positive');
  return Math.round(value * (unit === 'hours' ? 3_600_000 : 60_000));
}
export function fromMilliseconds(ms: number) {
  return ms > 0 && ms % 3_600_000 === 0
    ? { value: ms / 3_600_000, unit: 'hours' as const }
    : { value: ms / 60_000, unit: 'minutes' as const };
}
```

Create `DurationField` as a labeled number input plus unit select that emits milliseconds and shows a local positive-value error.

- [ ] **Step 6: Run the test and build and verify GREEN**

Run: `npm test -- src/duration.test.ts && npm run build`

Expected: PASS.

- [ ] **Step 7: Commit Task 4**

```bash
git add webui/package.json webui/package-lock.json webui/vite.config.ts webui/src/test webui/src/duration.ts webui/src/duration.test.ts webui/src/components/DurationField.tsx
git -c commit.gpgsign=false commit -m "test: add frontend form test harness"
```

### Task 5: Standard Administrator Login Modal

**Files:**
- Create: `webui/src/components/AdminLoginModal.tsx`
- Create: `webui/src/components/AdminLoginModal.test.tsx`
- Modify: `webui/src/main.tsx`
- Modify: `webui/src/styles.css`

- [ ] **Step 1: Write failing password-manager compatibility test**

Render the open modal and assert labels, names, autocomplete values, password type, and submit behavior.

```tsx
it('uses standard credential fields', () => {
  render(<AdminLoginModal open busy={false} onClose={() => {}} onLogin={vi.fn()} />);
  expect(screen.getByLabelText('Username')).toHaveAttribute('name', 'username');
  expect(screen.getByLabelText('Username')).toHaveAttribute('autocomplete', 'username');
  expect(screen.getByLabelText('Password')).toHaveAttribute('name', 'password');
  expect(screen.getByLabelText('Password')).toHaveAttribute('autocomplete', 'current-password');
  expect(screen.getByLabelText('Password')).toHaveAttribute('type', 'password');
});
```

Add user-event cases for failed login retaining username, closing clearing password, successful submit calling `onLogin`, and Escape calling `onClose`.

- [ ] **Step 2: Run the modal test and verify RED**

Run: `npm test -- src/components/AdminLoginModal.test.tsx`

Expected: FAIL because the component does not exist.

- [ ] **Step 3: Implement the modal and integrate it**

Create a semantic `role="dialog"`, labelled title, backdrop, close button, standard form, error live region, initial username focus, Escape handling, and password cleanup. Replace `AdminLogin` inline inputs in `main.tsx` with a login button, modal state, and the existing session callbacks.

- [ ] **Step 4: Add mobile-first modal styles**

Add backdrop, centered card, field, error, and action styles. Base width should be `calc(100% - 2rem)` with a desktop `max-width`; controls must have `min-height: 2.75rem`.

- [ ] **Step 5: Run modal tests and build and verify GREEN**

Run: `npm test -- src/components/AdminLoginModal.test.tsx && npm run build`

Expected: PASS.

- [ ] **Step 6: Commit Task 5**

```bash
git add webui/src/components/AdminLoginModal.tsx webui/src/components/AdminLoginModal.test.tsx webui/src/main.tsx webui/src/styles.css
git -c commit.gpgsign=false commit -m "feat: add standard admin login modal"
```

### Task 6: Typed Rule Form and Override Inheritance

**Files:**
- Create: `webui/src/components/RuleForm.tsx`
- Create: `webui/src/components/PolicyManager.tsx`
- Create: `webui/src/components/PolicyManager.test.tsx`
- Modify: `webui/src/api.ts`

- [ ] **Step 1: Write failing conditional-field tests**

Cover fixed-window limit, weekly weekday visibility, cooldown fields, credit fields, and override inheritance omission.

```tsx
it('shows only fields for the selected strategy', async () => {
  const user = userEvent.setup();
  render(<PolicyManager policies={policies} players={players} busy={false} onSave={vi.fn()} />);
  await user.selectOptions(screen.getByLabelText('Strategy'), 'cooldown');
  expect(screen.getByLabelText('Play duration')).toBeInTheDocument();
  expect(screen.getByLabelText('Required rest')).toBeInTheDocument();
  expect(screen.queryByLabelText('Fixed allowance')).not.toBeInTheDocument();
});
```

Add a save test that selects an override, leaves enabled on inherit, customizes only the limit, and asserts the payload has no `enabled`, strategy, schedule, or warning keys for that override.

- [ ] **Step 2: Run Policy manager tests and verify RED**

Run: `npm test -- src/components/PolicyManager.test.tsx`

Expected: FAIL because the manager and form do not exist.

- [ ] **Step 3: Implement RuleForm**

Render enabled state, timezone for global only, strategy, period/reset schedule, conditional strategy duration fields, and warning chips. For overrides, render `inherit/custom` controls and translate inherited groups to omitted properties rather than copied defaults.

- [ ] **Step 4: Implement PolicyManager draft ownership**

Own a deep-cloned editable Policy, selected key, dirty flag, save error, and selection confirmation state. Render the global item and override list in the master pane and the selected `RuleForm` in detail. Call `onSave` with a complete Policy document and reset the baseline from the resolved response.

- [ ] **Step 5: Run Policy manager tests and verify GREEN**

Run: `npm test -- src/components/PolicyManager.test.tsx`

Expected: PASS.

- [ ] **Step 6: Commit Task 6**

```bash
git add webui/src/components/RuleForm.tsx webui/src/components/PolicyManager.tsx webui/src/components/PolicyManager.test.tsx webui/src/api.ts
git -c commit.gpgsign=false commit -m "feat: add typed policy rule editor"
```

### Task 7: Player Override Management and Responsive Policy View

**Files:**
- Modify: `webui/src/components/PolicyManager.tsx`
- Modify: `webui/src/components/PolicyManager.test.tsx`
- Modify: `webui/src/main.tsx`
- Modify: `webui/src/styles.css`

- [ ] **Step 1: Write failing override lifecycle tests**

Test known-player selection, manual User ID entry, duplicate/blank rejection, player-name rendering, exemption preservation, deletion confirmation, and dirty-selection protection. Add a narrow-layout state test that the detail view has a back action after selecting an override.

- [ ] **Step 2: Run lifecycle tests and verify RED**

Run: `npm test -- src/components/PolicyManager.test.tsx`

Expected: FAIL on missing add/delete/navigation behavior.

- [ ] **Step 3: Implement known-player and manual override creation**

Add an accessible add dialog with a known-player search/select mode and manual User ID mode. Filter out IDs already in `policies.overrides`, initialize a new override as `{ exempt: false }`, and select it after creation.

- [ ] **Step 4: Implement delete, dirty guards, and mobile navigation**

Require explicit delete confirmation, preserve non-exemption fields when toggling exempt, and block selection/back navigation until the user saves or discards dirty changes. Use a view-state class so mobile shows either master or detail and desktop shows both.

- [ ] **Step 5: Replace raw editor with dedicated Policy view**

Remove `PolicyEditor` and its JSON textarea from `main.tsx`. Add a dashboard/Policy navigation control visible to authenticated administrators, render `PolicyManager` with current players and policies, and keep read-only Policy summary cards on the dashboard.

- [ ] **Step 6: Implement responsive visual styling**

Use a mobile-first one-column management layout, then introduce the master-detail grid at the project's desktop breakpoint. Style strategy choices, inheritance controls, warning chips, add/delete dialogs, sticky save actions, and a visible database-source badge. Preserve 44-pixel touch targets and existing muted green/industrial console visual language.

- [ ] **Step 7: Run frontend tests and build and verify GREEN**

Run: `npm test && npm run build`

Expected: PASS with no TypeScript errors.

- [ ] **Step 8: Commit Task 7**

```bash
git add webui/src/components/PolicyManager.tsx webui/src/components/PolicyManager.test.tsx webui/src/main.tsx webui/src/styles.css
git -c commit.gpgsign=false commit -m "feat: add visual player policy management"
```

### Task 8: Full Verification and Documentation Alignment

**Files:**
- Modify: `README.md`
- Modify: `docs/superpowers/plans/2026-07-10-policy-management-ui.md`

- [ ] **Step 1: Update README configuration and UI documentation**

Document database precedence, one-time YAML bootstrap behavior, removal of YAML overrides, standard admin login, and visual Policy management. Include the exact environment variables already used for administrator credentials.

- [ ] **Step 2: Run formatting and static builds**

Run:

```bash
gofmt -w internal/config/config.go internal/config/config_test.go internal/policy/service.go internal/policy/service_test.go internal/api/server.go internal/api/server_test.go internal/app/app_test.go
go vet ./...
```

Expected: no output from `go vet` and no formatting diff after a second `gofmt`.

- [ ] **Step 3: Run complete automated verification**

Run:

```bash
go test ./...
cd webui
npm test
npm run build
```

Expected: all Go and frontend tests pass and Vite produces `webui/dist`.

- [ ] **Step 4: Perform browser acceptance checks**

Start the Go API with a temporary SQLite database and the Vite dev server, then use browser automation at approximately 1440×900 and 390×844 to verify login field semantics, dashboard read-only behavior, Policy master-detail navigation, strategy field switching, known/manual override addition, save, delete, and logout locking.

- [ ] **Step 5: Check the worktree and diff**

Run: `git status --short && git diff --check && git log --oneline -10`

Expected: only intentional files are modified, `git diff --check` is empty, and task commits are present.

- [ ] **Step 6: Mark completed plan checkboxes and commit final alignment**

```bash
git add README.md docs/superpowers/plans/2026-07-10-policy-management-ui.md
git -c commit.gpgsign=false commit -m "docs: document policy management workflow"
```
