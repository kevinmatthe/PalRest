# Cross-Platform Game Overlay Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a manually launched Windows/macOS Tauri overlay that shows one Palworld player's live identity, map position, latency, observed playtime, and policy timers from a new read-only Playtime Guard snapshot API.

**Architecture:** Add a capability-based `overlay.snapshot/v1` provider boundary to the Go service, then consume it from an independent Tauri 2 + React application. Keep business calculations server-side; keep desktop lifecycle/window differences behind Rust platform adapters and Palworld presentation behind a TypeScript game adapter.

**Tech Stack:** Go 1.26, SQLite-backed existing services, React 19, TypeScript 5.7+, Vite, Vitest, Leaflet, Tauri 2, Rust, `reqwest`, `sysinfo`, Windows Registry access, GitHub Actions.

---

## Scope and sequencing

This is one dependency-ordered plan with three phases:

1. A testable Go snapshot provider and HTTP contract.
2. A browser-testable React overlay and settings experience.
3. Tauri platform integration, installers, and real Windows/macOS acceptance.

Do not start Tauri window work until the provider contract and React states pass independently. Do not move Guard policy calculations, timezone calculations, or Analytics aggregation into the desktop application.

The supported release targets are Windows 10/11 x64 and macOS 14+ on Apple Silicon. Pointer/mouse passthrough must be verified on both; Linux remains outside the release matrix.

## File structure

### Service-side files

- Create `internal/overlay/types.go` — versioned transport-neutral snapshot types.
- Create `internal/overlay/palworld.go` — Palworld provider and timer semantics.
- Create `internal/overlay/palworld_test.go` — provider aggregation and failure tests.
- Create `internal/api/overlay.go` — query validation, JSON response, ETag, stable errors.
- Create `internal/api/overlay_test.go` — handler contract and sensitive-field tests.
- Modify `internal/api/server.go` — optional provider field, option, and route registration.
- Modify `internal/app/app.go` — construct and inject the Palworld provider.
- Create `testdata/overlay/palworld_snapshot_v1.json` — canonical shared Go/TypeScript fixture.
- Modify `README.md` — endpoint and trusted-private-network boundary.

### Desktop web files

- Create `overlay/package.json`, `overlay/package-lock.json` — isolated frontend/Tauri dependencies.
- Create `overlay/index.html`, `overlay/vite.config.ts`, `overlay/vitest.config.ts`, `overlay/tsconfig*.json` — build/test configuration.
- Create `overlay/src/contracts/snapshot.ts` — runtime parser and TypeScript contract.
- Create `overlay/src/core/poller.ts` — ETag polling, timeout, retry, stale-state transitions.
- Create `overlay/src/core/config.ts` — typed settings and normalization.
- Create `overlay/src/core/bridge.ts` — narrow Tauri/browser bridge.
- Create `overlay/src/games/types.ts` — game adapter interface.
- Create `overlay/src/games/palworld/adapter.ts` — Palworld metadata/tone/process metadata.
- Create `overlay/src/games/palworld/map.ts` — coordinate projection and tile URL resolution.
- Create `overlay/src/components/OverlayBar.tsx` — selected 480×76 capability layout.
- Create `overlay/src/components/PalworldMiniMap.tsx` — north-up centered local map.
- Create `overlay/src/settings/SettingsView.tsx` — service/player/scale settings.
- Create `overlay/src/App.tsx`, `overlay/src/main.tsx`, `overlay/src/styles.css` — window routing and polished states.
- Create focused `*.test.ts` / `*.test.tsx` files beside each module.

### Tauri/Rust files

- Create `overlay/src-tauri/Cargo.toml`, `Cargo.lock`, `build.rs`, `tauri.conf.json`.
- Create `overlay/src-tauri/capabilities/default.json` — least-privilege window capabilities.
- Create `overlay/icon-source.png` and generated `overlay/src-tauri/icons/` assets — Windows/macOS application icons.
- Create `overlay/src-tauri/src/main.rs`, `lib.rs` — application entry and command registration.
- Create `overlay/src-tauri/src/config.rs` — versioned atomic local configuration.
- Create `overlay/src-tauri/src/http.rs` — bounded snapshot/player-list HTTP bridge.
- Create `overlay/src-tauri/src/process.rs` — pure process matching plus monitor.
- Create `overlay/src-tauri/src/platform/mod.rs` — shared platform adapter contract.
- Create `overlay/src-tauri/src/platform/windows.rs` — Windows Steam identity discovery.
- Create `overlay/src-tauri/src/platform/macos.rs` — macOS manual-identity behavior.
- Create `overlay/src-tauri/src/lifecycle.rs` — game show/hide and click-through state.
- Create `overlay/src-tauri/src/tray.rs` — tray/menu actions.
- Create Rust unit tests in the same modules under `#[cfg(test)]`.

### Delivery files

- Create `.github/workflows/overlay.yml` — Go/frontend/Rust tests and platform artifacts.
- Create `overlay/README.md` — local development, configuration, signing, and smoke test.
- Modify `.gitignore` — ignore overlay build output without ignoring lockfiles.

## Phase 1 — Go snapshot provider

### Task 1: Freeze `overlay.snapshot/v1` with a shared fixture

**Files:**
- Create: `internal/overlay/types.go`
- Create: `testdata/overlay/palworld_snapshot_v1.json`
- Create: `internal/overlay/types_test.go`

- [ ] **Step 1: Write the failing JSON contract test**

```go
func TestSnapshotV1Fixture(t *testing.T) {
	data, err := os.ReadFile("../../testdata/overlay/palworld_snapshot_v1.json")
	if err != nil { t.Fatal(err) }
	var got overlay.Snapshot
	if err := json.Unmarshal(data, &got); err != nil { t.Fatal(err) }
	if got.Schema != overlay.SchemaV1 || got.GameID != "palworld" || got.UserID != "steam_76561198000000001" {
		t.Fatalf("identity=%+v", got)
	}
	if len(got.Timers) != 4 || got.Map == nil || got.Latency == nil {
		t.Fatalf("capabilities=%+v timers=%+v", got.Capabilities, got.Timers)
	}
	for _, forbidden := range []string{"ip", "password", "authorization", "private_samples"} {
		if strings.Contains(strings.ToLower(string(data)), forbidden) { t.Fatalf("fixture contains %q", forbidden) }
	}
}
```

- [ ] **Step 2: Run the test and verify RED**

Run: `go test ./internal/overlay -run TestSnapshotV1Fixture -count=1`

Expected: FAIL because `internal/overlay` and its types do not exist.

- [ ] **Step 3: Add the complete transport types**

```go
package overlay

import "time"

const SchemaV1 = "overlay.snapshot/v1"

type Snapshot struct {
	Schema       string       `json:"schema"`
	GameID       string       `json:"game_id"`
	UserID       string       `json:"user_id"`
	ObservedAt   time.Time    `json:"observed_at"`
	FreshUntil   time.Time    `json:"fresh_until"`
	SourceStatus string       `json:"source_status"`
	Capabilities []string     `json:"capabilities"`
	Identity     Identity     `json:"identity"`
	Latency      *Latency     `json:"latency,omitempty"`
	Timers       []Timer      `json:"timers,omitempty"`
	Map          *MapPosition `json:"map,omitempty"`
}

type Identity struct {
	DisplayName string `json:"display_name"`
	AccountName string `json:"account_name,omitempty"`
	Level       *int   `json:"level,omitempty"`
}

type Latency struct { Milliseconds float64 `json:"milliseconds"` }

type Timer struct {
	ID         string  `json:"id"`
	Label      string  `json:"label"`
	ValueMS    int64   `json:"value_ms"`
	Semantic   string  `json:"semantic"`
	Tone       string  `json:"tone"`
	Progress   *float64 `json:"progress,omitempty"`
}

type MapPosition struct {
	X           float64 `json:"x"`
	Y           float64 `json:"y"`
	Projection  string  `json:"projection"`
	TileSet     string  `json:"tile_set"`
	TileURL     string  `json:"tile_url"`
}
```

Use capability names `identity`, `latency`, `timers`, and `map`; source statuses `online`, `offline`, and `unknown`; tones `normal`, `warning`, `danger`, and `muted`.

- [ ] **Step 4: Add the canonical fixture and verify GREEN**

The fixture must contain exactly one online Palworld player, four timer IDs (`today_observed`, `week_observed`, `policy_cycle_used`, `policy_remaining`), `projection: "palworld_world_v1"`, `tile_set: "palworld_default_v1"`, and `tile_url: "/map/tiles/{z}/{x}/{y}.png"`.

Run: `go test ./internal/overlay -run TestSnapshotV1Fixture -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the frozen contract**

```bash
git add internal/overlay/types.go internal/overlay/types_test.go testdata/overlay/palworld_snapshot_v1.json
git commit -m "feat(overlay): define snapshot v1 contract"
```

### Task 2: Aggregate Palworld identity, Analytics, Guard timers, and freshness

**Files:**
- Create: `internal/overlay/palworld.go`
- Create: `internal/overlay/palworld_test.go`

- [ ] **Step 1: Write failing provider tests**

Define fakes for these narrow dependencies:

```go
type SnapshotSource interface { Snapshot(context.Context, string) (domain.PlayerSnapshot, error) }
type DailySource interface { PlayerDailyActivity(context.Context, string, string, string) ([]store.DailyActivity, error) }
type StatusSource interface { Status() domain.PollStatus }
```

Tests must prove:

```go
func TestPalworldProviderBuildsOnlineSnapshot(t *testing.T) { /* today=30m, week=2h, policy used=90m, remaining=30m, ping/map present */ }
func TestPalworldProviderUsesMondayWeekBoundary(t *testing.T) { /* Wednesday query starts Monday and ends Thursday */ }
func TestPalworldProviderMarksStalePollUnknown(t *testing.T) { /* now > lastSuccess+maxGap */ }
func TestPalworldProviderOmitsUnknownLatencyAndMap(t *testing.T) { /* NaN ping/coords */ }
func TestPalworldProviderOmitsPolicyTimersWhenDisabledOrExempt(t *testing.T) { /* still returns today/week */ }
func TestPalworldProviderMapsNotFound(t *testing.T) { /* errors.Is(err, store.ErrNotFound) */ }
```

- [ ] **Step 2: Run the provider tests and verify RED**

Run: `go test ./internal/overlay -run 'TestPalworldProvider' -count=1`

Expected: FAIL because `NewPalworldProvider` is undefined.

- [ ] **Step 3: Implement the provider boundary**

Use this public shape:

```go
type Provider interface {
	Snapshot(ctx context.Context, gameID, userID string) (Snapshot, error)
}

type PalworldProvider struct {
	snapshots SnapshotSource
	daily DailySource
	status StatusSource
	location *time.Location
	maxGap time.Duration
	now func() time.Time
}

func NewPalworldProvider(s SnapshotSource, d DailySource, status StatusSource, location *time.Location, maxGap time.Duration) *PalworldProvider
func (p *PalworldProvider) Snapshot(ctx context.Context, gameID, userID string) (Snapshot, error)
```

Implementation rules:

- Reject `gameID != "palworld"` with exported `ErrGameNotSupported`.
- Trim `userID`; reject empty IDs with `ErrInvalidRequest`.
- Query Guard once and Analytics once for Monday through tomorrow.
- Sum `today_observed` from today's row and `week_observed` from all returned rows.
- Use `Status().LastSuccess` for `observed_at`; use `last_success + maxGap` for `fresh_until`.
- Return `unknown` when there has never been a success or the last success is stale.
- Only return latency/map while the source is fresh, the player is online, and values are finite/nonnegative.
- Set `Level` only when level is positive.
- Use the first non-empty value of player name, account name, and user ID for `display_name`.
- Add policy timers only when the policy is enabled and not exempt.
- Compute progress as `used/(used+remaining)`, clamped to `[0,1]`; omit it if the denominator is zero.
- Use `danger` at `remaining <= 0`, `warning` when remaining is at or below the largest configured warning threshold, otherwise `normal`.
- For credit strategy, label the fourth timer `可用额度`; for cooldown rest state, use `休息剩余`; for fixed window use `频控剩余`.

- [ ] **Step 4: Run provider and repository tests**

Run: `go test ./internal/overlay ./internal/guard ./internal/store -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the provider**

```bash
git add internal/overlay/palworld.go internal/overlay/palworld_test.go
git commit -m "feat(overlay): aggregate Palworld snapshot"
```

### Task 3: Expose the read-only endpoint with ETag and wire the app

**Files:**
- Create: `internal/api/overlay.go`
- Create: `internal/api/overlay_test.go`
- Modify: `internal/api/server.go`
- Modify: `internal/app/app.go`

- [ ] **Step 1: Write failing handler tests**

Cover exact requests and responses:

```go
GET /api/v1/overlay/snapshot                         -> 400 invalid_request
GET /api/v1/overlay/snapshot?game_id=x&user_id=u     -> 404 game_not_supported
GET /api/v1/overlay/snapshot?game_id=palworld&user_id=missing -> 404 player_not_found
GET /api/v1/overlay/snapshot?game_id=palworld&user_id=steam_1 -> 200 + ETag + Cache-Control:no-cache
same request with If-None-Match -> 304 with empty body
provider failure -> 503 snapshot_unavailable
```

Also test duplicate `game_id`/`user_id` parameters, a game ID longer than 64 bytes, and a user ID longer than 256 bytes return `400`. Marshal the successful body and assert it contains none of `iP`, `private_samples`, `password`, `authorization`, or `settings`.

- [ ] **Step 2: Run the handler tests and verify RED**

Run: `go test ./internal/api -run 'TestOverlay' -count=1`

Expected: FAIL because the route and provider option do not exist.

- [ ] **Step 3: Add the handler and explicit injection option**

Add to `Server`:

```go
type OverlayProvider interface {
	Snapshot(context.Context, string, string) (overlay.Snapshot, error)
}

type overlayOption struct{ provider OverlayProvider }
func WithOverlayProvider(provider OverlayProvider) any { return overlayOption{provider: provider} }
```

Register `GET /api/v1/overlay/snapshot`. In `overlay.go`, marshal once, hash the exact bytes with SHA-256, encode a quoted hex ETag, set `Cache-Control: no-cache`, and compare `If-None-Match` by exact value. Write the already-marshaled bytes plus one newline for `200`; do not call `writeJSON` after hashing.

Require exactly one `game_id` and one `user_id` query value. Trim surrounding whitespace, require lengths `1..64` and `1..256` bytes respectively, and reject control characters before calling the provider.

Map errors with `errors.Is`:

```go
overlay.ErrInvalidRequest   -> 400 invalid_request
overlay.ErrGameNotSupported -> 404 game_not_supported
store.ErrNotFound           -> 404 player_not_found
all others                  -> 503 snapshot_unavailable
```

- [ ] **Step 4: Inject the provider from `internal/app/app.go`**

Load the configured policy timezone, then append:

```go
overlayProvider := overlay.NewPalworldProvider(
	guardService,
	repo,
	poll,
	location,
	cfg.Server.MaxObservationGap.Duration,
)
apiOptions = append(apiOptions, api.WithOverlayProvider(overlayProvider))
```

Run: `gofmt -w internal/overlay internal/api/overlay.go internal/api/overlay_test.go internal/api/server.go internal/app/app.go`

Run: `go test ./internal/... -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the API slice**

```bash
git add internal/api/overlay.go internal/api/overlay_test.go internal/api/server.go internal/app/app.go
git commit -m "feat(api): expose player overlay snapshot"
```

## Phase 2 — React overlay independent of Tauri

### Task 4: Scaffold the isolated overlay app and validate the shared contract

**Files:**
- Create: `overlay/package.json`
- Create: `overlay/package-lock.json`
- Create: `overlay/index.html`
- Create: `overlay/vite.config.ts`
- Create: `overlay/vitest.config.ts`
- Create: `overlay/tsconfig.json`
- Create: `overlay/tsconfig.node.json`
- Create: `overlay/src/test/setup.ts`
- Create: `overlay/src/contracts/snapshot.ts`
- Create: `overlay/src/contracts/snapshot.test.ts`
- Create: `overlay/src/App.tsx`
- Create: `overlay/src/main.tsx`

- [ ] **Step 1: Create package metadata and install locked dependencies**

Use these dependency ranges, then commit the generated lockfile:

```json
{
  "name": "palrest-game-overlay",
  "private": true,
  "version": "0.1.0",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "tsc -b && vite build",
    "test": "vitest run",
    "tauri": "tauri"
  },
  "dependencies": {
    "@tauri-apps/api": "^2.11.1",
    "leaflet": "^1.9.4",
    "react": "^19.2.7",
    "react-dom": "^19.2.7"
  },
  "devDependencies": {
    "@tauri-apps/cli": "^2.11.4",
    "@testing-library/jest-dom": "^6.9.1",
    "@testing-library/react": "^16.3.2",
    "@types/leaflet": "^1.9.21",
    "@types/node": "^22.0.0",
    "@types/react": "^19.2.17",
    "@types/react-dom": "^19.2.3",
    "@vitejs/plugin-react": "^6.0.3",
    "jsdom": "^29.1.1",
    "typescript": "^5.7.0",
    "vite": "^8.1.5",
    "vitest": "^4.1.10"
  }
}
```

Create a minimal `main.tsx` that mounts `App`, and an `App.tsx` that renders a static `Overlay loading` text until Task 8 replaces it with window routing. Then run: `cd overlay && npm install`

Expected: `package-lock.json` is created with no audit-critical installation failure.

- [ ] **Step 2: Write the failing shared-fixture parser test**

```ts
const fixture = JSON.parse(readFileSync(new URL('../../../testdata/overlay/palworld_snapshot_v1.json', import.meta.url), 'utf8'));

it('accepts the canonical snapshot and rejects a new major schema', () => {
  expect(parseSnapshot(fixture).schema).toBe('overlay.snapshot/v1');
  expect(() => parseSnapshot({ ...fixture, schema: 'overlay.snapshot/v2' })).toThrow('unsupported snapshot schema');
});
```

- [ ] **Step 3: Run the test and verify RED**

Run: `cd overlay && npm test -- snapshot.test.ts`

Expected: FAIL because `parseSnapshot` is undefined.

- [ ] **Step 4: Implement a strict required-field parser with additive compatibility**

Export the exact TypeScript types matching Task 1. `parseSnapshot(value: unknown)` must reject non-objects, wrong schema, invalid dates, unknown source status, duplicate timer IDs, non-finite numeric fields, progress outside `[0,1]`, and map coordinates without a map capability. It must ignore unknown additive object keys.

Run: `cd overlay && npm test -- snapshot.test.ts && npm run build`

Expected: PASS and Vite build succeeds.

- [ ] **Step 5: Commit the frontend contract**

```bash
git add overlay/package.json overlay/package-lock.json overlay/index.html overlay/vite.config.ts overlay/vitest.config.ts overlay/tsconfig.json overlay/tsconfig.node.json overlay/src/test overlay/src/contracts overlay/src/App.tsx overlay/src/main.tsx
git commit -m "feat(overlay-ui): validate snapshot v1"
```

### Task 5: Implement deterministic ETag polling and stale/error states

**Files:**
- Create: `overlay/src/core/poller.ts`
- Create: `overlay/src/core/poller.test.ts`
- Create: `overlay/src/core/bridge.ts`

- [ ] **Step 1: Write fake-timer tests for the state machine**

Test these transitions with an injected `fetcher` and clock:

```ts
idle -> loading -> ready
ready + 304 -> ready with unchanged snapshot
ready + network error -> disconnected with last snapshot retained
disconnected + success -> ready and delay reset to 2000ms
consecutive failures -> delays 2000, 4000, 8000, 16000, 30000ms
fresh_until passed -> stale even before the next request succeeds
404 player_not_found -> needs-player with no automatic name match
unsupported schema -> incompatible
stop() -> no outstanding timer or AbortController
```

- [ ] **Step 2: Run and verify RED**

Run: `cd overlay && npm test -- poller.test.ts`

Expected: FAIL because `SnapshotPoller` is undefined.

- [ ] **Step 3: Implement `SnapshotPoller`**

Use this narrow bridge contract:

```ts
export type FetchSnapshotRequest = { baseUrl: string; gameId: string; userId: string; etag?: string };
export type FetchSnapshotResult =
  | { status: 200; etag?: string; body: unknown }
  | { status: 304 }
  | { status: 404; code: 'player_not_found' | 'game_not_supported' }
  | { status: 503; code: 'snapshot_unavailable' };

export interface OverlayBridge {
  fetchSnapshot(request: FetchSnapshotRequest, signal: AbortSignal): Promise<FetchSnapshotResult>;
}
```

Keep only the last valid snapshot in memory. Use one scheduled timeout, not `setInterval`, so requests never overlap. Apply a 5-second request timeout and the capped backoff sequence above.

- [ ] **Step 4: Run tests and commit**

Run: `cd overlay && npm test -- poller.test.ts && npm run build`

Expected: PASS.

```bash
git add overlay/src/core/poller.ts overlay/src/core/poller.test.ts overlay/src/core/bridge.ts
git commit -m "feat(overlay-ui): add resilient snapshot polling"
```

### Task 6: Build the approved overlay bar and state visuals

**Files:**
- Create: `overlay/src/components/OverlayBar.tsx`
- Create: `overlay/src/components/OverlayBar.test.tsx`
- Create: `overlay/src/games/types.ts`
- Create: `overlay/src/games/palworld/adapter.ts`
- Create: `overlay/src/games/palworld/adapter.test.ts`
- Create: `overlay/src/styles.css`

- [ ] **Step 1: Write component tests from the approved mockup**

Assert:

- Default rendered size class is `overlay--compact` and exposes four timer labels.
- Identity line shows `名称 · Lv.N`, online/offline status, ping, and freshness.
- Warning tone adds `overlay--warning` without a dialog or animation class.
- Disconnected and stale states retain values and show last-update copy.
- Missing latency/map/timers remove only that capability region.
- Adjust mode adds a visible drag hint and `data-tauri-drag-region`.
- Timers render in the order supplied by the provider.

- [ ] **Step 2: Run and verify RED**

Run: `cd overlay && npm test -- OverlayBar.test.tsx adapter.test.ts`

Expected: FAIL because the adapter and component do not exist.

- [ ] **Step 3: Implement the game adapter and component**

Use this adapter interface:

```ts
export interface GameAdapter {
  id: string;
  title: string;
  processHints: { windows: string[]; macos: string[] };
  formatDuration(ms: number): string;
  overallTone(snapshot: Snapshot): 'normal' | 'warning' | 'danger' | 'muted';
}
```

The Palworld adapter uses Chinese labels from the provider, rounds display durations to whole minutes, and selects the strongest timer tone in order `danger > warning > normal > muted`.

Implement the approved `480×76px` layout with CSS variables for scale, an opaque-enough blurred dark panel, 44px minimum interactive targets only in adjustment/settings mode, no blinking, and `prefers-reduced-motion` disabling all transitions.

- [ ] **Step 4: Run component tests and commit**

Run: `cd overlay && npm test -- OverlayBar.test.tsx adapter.test.ts && npm run build`

Expected: tests PASS and the production bundle builds. Real-window visual inspection remains in Task 13 after Tauri routing exists.

```bash
git add overlay/src/components/OverlayBar.tsx overlay/src/components/OverlayBar.test.tsx overlay/src/games overlay/src/styles.css
git commit -m "feat(overlay-ui): render compact player bar"
```

### Task 7: Add the private-network Palworld mini-map without a public fallback

**Files:**
- Create: `overlay/src/games/palworld/map.ts`
- Create: `overlay/src/games/palworld/map.test.ts`
- Create: `overlay/src/components/PalworldMiniMap.tsx`
- Create: `overlay/src/components/PalworldMiniMap.test.tsx`
- Modify: `overlay/src/components/OverlayBar.tsx`

- [ ] **Step 1: Write projection and URL safety tests**

Reuse the established landscape constants:

```ts
export const PALWORLD_LANDSCAPE = [349400, 724400, -1099400, -724400] as const;
```

Tests must prove world origin and known edge coordinates project into `L.CRS.Simple`, relative tile URLs resolve against the configured private service base, and absolute tile URLs are rejected unless their host equals the configured service host. Assert that no source contains `palworld.gg`.

- [ ] **Step 2: Run and verify RED**

Run: `cd overlay && npm test -- map.test.ts PalworldMiniMap.test.tsx`

Expected: FAIL because projection and mini-map modules do not exist.

- [ ] **Step 3: Implement the north-up centered mini-map**

Create a non-interactive Leaflet map using `L.CRS.Simple`, no controls, no attribution, no drag/zoom input, fixed zoom, and a single centered player marker. Recenter only when coordinates change. On tile error, render the map region's muted unavailable state; do not affect the rest of the overlay.

- [ ] **Step 4: Run tests and commit**

Run: `cd overlay && npm test -- map.test.ts PalworldMiniMap.test.tsx && npm run build`

Expected: PASS.

```bash
git add overlay/src/games/palworld/map.ts overlay/src/games/palworld/map.test.ts overlay/src/components/PalworldMiniMap.tsx overlay/src/components/PalworldMiniMap.test.tsx overlay/src/components/OverlayBar.tsx
git commit -m "feat(overlay-ui): add private Palworld mini-map"
```

### Task 8: Add settings, player selection, and window routing

**Files:**
- Create: `overlay/src/core/config.ts`
- Create: `overlay/src/core/config.test.ts`
- Create: `overlay/src/settings/SettingsView.tsx`
- Create: `overlay/src/settings/SettingsView.test.tsx`
- Modify: `overlay/src/App.tsx`
- Create: `overlay/src/App.test.tsx`
- Modify: `overlay/src/main.tsx`

- [ ] **Step 1: Write configuration and settings tests**

Use this schema:

```ts
export type OverlayConfigV1 = {
  schema: 1;
  baseUrl: string;
  gameId: 'palworld';
  userId: string;
  scale: 0.8 | 1 | 1.25;
  displayId?: string;
  x?: number;
  y?: number;
  locked: boolean;
};
```

Tests cover URL trimming, rejecting credentials/query/fragment in `baseUrl`, only allowing `http`/`https`, preserving URL ports and Tailscale hostnames, player selection by stable UID, and no automatic same-name selection.

- [ ] **Step 2: Run and verify RED**

Run: `cd overlay && npm test -- config.test.ts SettingsView.test.tsx App.test.tsx`

Expected: FAIL because the settings modules do not exist.

- [ ] **Step 3: Implement settings and two-window routing**

The bridge must expose:

```ts
loadConfig(): Promise<OverlayConfigV1 | null>
saveConfig(config: OverlayConfigV1): Promise<void>
listPlayers(baseUrl: string, signal: AbortSignal): Promise<Array<{ user_id: string; name: string; account_name: string }>>
currentWindowLabel(): Promise<'overlay' | 'settings'>
setAdjustmentMode(enabled: boolean): Promise<void>
```

`App` renders `SettingsView` for the settings window label and `OverlayBar` for the overlay label. First run opens settings. Windows may preselect only an exact detected UID returned by the Rust bridge; macOS shows the list without an automatic candidate.

- [ ] **Step 4: Run tests and commit**

Run: `cd overlay && npm test && npm run build`

Expected: all frontend tests PASS.

```bash
git add overlay/src/core/config.ts overlay/src/core/config.test.ts overlay/src/settings overlay/src/App.tsx overlay/src/App.test.tsx overlay/src/main.tsx
git commit -m "feat(overlay-ui): add player and service settings"
```

## Phase 3 — Tauri and platform delivery

### Task 9: Scaffold Tauri, persist config atomically, and bridge HTTP

**Files:**
- Create: `overlay/src-tauri/Cargo.toml`
- Create: `overlay/src-tauri/Cargo.lock`
- Create: `overlay/src-tauri/build.rs`
- Create: `overlay/src-tauri/tauri.conf.json`
- Create: `overlay/src-tauri/capabilities/default.json`
- Create: `overlay/src-tauri/src/main.rs`
- Create: `overlay/src-tauri/src/lib.rs`
- Create: `overlay/src-tauri/src/config.rs`
- Create: `overlay/src-tauri/src/http.rs`
- Create: `overlay/icon-source.png`
- Create: `overlay/src-tauri/icons/`
- Modify: `overlay/src/core/bridge.ts`

- [ ] **Step 1: Create the Tauri manifest and generate the lockfile**

Use Rust edition 2024 and compatible major requirements:

```toml
[dependencies]
tauri = { version = "2", features = ["tray-icon", "image-png"] }
serde = { version = "1", features = ["derive"] }
serde_json = "1"
reqwest = { version = "0.12", default-features = false, features = ["json", "rustls-tls"] }
url = "2"
sysinfo = "0.37"

[target.'cfg(windows)'.dependencies]
winreg = "0.55"

[build-dependencies]
tauri-build = { version = "2", features = [] }
```

Run: `cd overlay/src-tauri && cargo generate-lockfile`

Expected: `Cargo.lock` is created from non-yanked compatible releases.

- [ ] **Step 2: Write failing Rust tests for config and HTTP response mapping**

Test config round-trip, migration of an unversioned pre-release config into schema 1, unknown future schema rejection, atomic replacement leaving no temp file, `200/304/404/503` mapping, a 5-second request timeout, and response body limit of 1 MiB.

Run: `cd overlay/src-tauri && cargo test config && cargo test http`

Expected: FAIL because the modules do not exist.

- [ ] **Step 3: Implement commands and least-privilege configuration**

Register commands:

```rust
load_config
save_config
fetch_snapshot
list_players
current_platform
detected_palworld_user_id
set_adjustment_mode
```

Use `app.path().app_config_dir()` and write `config.json.tmp`, `sync_all`, then rename to `config.json`. `fetch_snapshot` and `list_players` use one `reqwest::Client` with connect timeout 3 seconds, total timeout 5 seconds, redirect limit 3, and 1 MiB body cap. Never log a response body or full URL query.

Configure two windows:

- `overlay`: 480×76, transparent, decorations false, resizable false, always-on-top, skip-taskbar, initially hidden.
- `settings`: 560×520, decorated, initially hidden.

Set CSP to local scripts/styles plus images from `http:`/`https:`/`data:`; do not enable shell execution, filesystem plugins, global shortcuts, or autostart.

Create one square source icon at `overlay/icon-source.png`, then run `npm --prefix overlay run tauri -- icon icon-source.png`. Commit the generated `.ico`, `.icns`, and PNG icon set so platform builds do not depend on an untracked design tool.

- [ ] **Step 4: Wire the real Tauri bridge and run both suites**

`bridge.ts` selects Tauri `invoke` when `isTauri()` is true and retains an injected browser bridge for Vitest/story fixtures.

Run: `cd overlay/src-tauri && cargo fmt --check && cargo test`

Run: `cd overlay && npm test && npm run build`

Expected: PASS.

- [ ] **Step 5: Commit the shell foundation**

```bash
git add overlay/src-tauri overlay/src/core/bridge.ts
git commit -m "feat(overlay): add Tauri shell and HTTP bridge"
```

### Task 10: Add cross-platform process monitoring, tray actions, and click-through lifecycle

**Files:**
- Create: `overlay/src-tauri/src/process.rs`
- Create: `overlay/src-tauri/src/platform/mod.rs`
- Create: `overlay/src-tauri/src/lifecycle.rs`
- Create: `overlay/src-tauri/src/tray.rs`
- Modify: `overlay/src-tauri/src/lib.rs`

- [ ] **Step 1: Write failing pure lifecycle tests**

Test process matching from `(name, executable path)` records:

- Windows matches case-insensitive `Palworld-Win64-Shipping.exe`.
- macOS matches an executable path containing the exact `Palworld.app` component.
- Names containing only `pal` or unrelated paths do not match.
- Two consecutive one-second scans meet the 2-second show/hide bound.
- Adjustment mode disables ignore-cursor-events; locking reenables it.
- No game process keeps the overlay hidden while settings remains independently usable.

- [ ] **Step 2: Run and verify RED**

Run: `cd overlay/src-tauri && cargo test process && cargo test lifecycle`

Expected: FAIL because matcher and reducer are undefined.

- [ ] **Step 3: Implement a reducer-driven monitor**

Keep OS calls outside the reducer:

```rust
enum LifecycleEvent { GameDetected(bool), EnterAdjustment, Lock, Quit }
struct LifecycleState { game_running: bool, adjustment: bool, overlay_visible: bool, click_through: bool }
fn reduce(state: LifecycleState, event: LifecycleEvent) -> LifecycleState
```

Use `sysinfo` once per second. Apply state changes to the overlay window only when values differ. Showing the overlay must not call `set_focus`. Entering adjustment mode shows and focuses it, enables pointer input, and emits an event to React. Locking saves geometry, clears focus, and reenables pointer passthrough.

- [ ] **Step 4: Implement the tray/menu contract**

Menu items:

- Status: disabled text.
- Adjust position.
- Lock overlay.
- Settings.
- Reselect player.
- Quit.

Do not add autostart or hotkey entries. Closing the settings window hides it; Quit is the only normal full-process exit.

- [ ] **Step 5: Run tests and commit**

Run: `cd overlay/src-tauri && cargo fmt --check && cargo clippy --all-targets -- -D warnings && cargo test`

Expected: PASS.

```bash
git add overlay/src-tauri/src/process.rs overlay/src-tauri/src/platform overlay/src-tauri/src/lifecycle.rs overlay/src-tauri/src/tray.rs overlay/src-tauri/src/lib.rs
git commit -m "feat(overlay): add game-driven window lifecycle"
```

### Task 11: Add Windows Steam UID discovery with safe manual fallback

**Files:**
- Create: `overlay/src-tauri/src/platform/windows.rs`
- Modify: `overlay/src-tauri/src/platform/mod.rs`
- Create tests inside: `overlay/src-tauri/src/platform/windows.rs`

- [ ] **Step 1: Write failing conversion and registry-value tests**

Use the individual-account SteamID64 base `76561197960265728` and test:

```rust
assert_eq!(steam_user_id_from_account_id(39734273), Some("steam_76561198000000001".to_string()));
assert_eq!(steam_user_id_from_account_id(0), None);
```

Calculate the exact expected decimal value in the test from `BASE + account_id`; do not hard-code an ellipsis. Test wrong registry types and unavailable keys return `None`, never a guessed name.

- [ ] **Step 2: Run the target test and verify RED**

Run on Windows: `cd overlay/src-tauri && cargo test platform::windows`

Expected: FAIL because the Windows adapter is undefined.

- [ ] **Step 3: Implement low-cost discovery**

Read `HKCU\Software\Valve\Steam\ActiveProcess\ActiveUser` as a 32-bit account ID. Convert only nonzero values to `steam_<SteamID64>`. Expose it as an optional candidate; the settings flow must still verify that exact UID exists in `/api/v1/players` before preselecting it.

No Steamworks SDK, Web API key, game memory access, profile-name matching, or ownership verification is introduced.

- [ ] **Step 4: Run Windows tests and commit**

Run on Windows: `cd overlay/src-tauri && cargo fmt --check && cargo test && cargo build --release`

Expected: PASS and release binary builds.

```bash
git add overlay/src-tauri/src/platform/windows.rs overlay/src-tauri/src/platform/mod.rs
git commit -m "feat(overlay): detect Windows Steam player UID"
```

### Task 12: Complete macOS manual identity, platform builds, and signing-aware CI

**Files:**
- Create: `overlay/src-tauri/src/platform/macos.rs`
- Modify: `overlay/src-tauri/src/platform/mod.rs`
- Create: `.github/workflows/overlay.yml`
- Modify: `.gitignore`

- [ ] **Step 1: Write the macOS adapter contract test**

Assert macOS returns no automatic `userId`, detects `Palworld.app` only by exact path component, and retains a manually saved UID across config reload.

Run on macOS: `cd overlay/src-tauri && cargo test platform::macos`

Expected: FAIL because the macOS adapter is undefined.

- [ ] **Step 2: Implement the macOS adapter**

Return `None` from automatic identity discovery. Use the existing player list and saved `userId`; never map Apple display names or account names heuristically. Confirm the Tauri window is above ordinary app windows, transparent, hidden from Dock/task switcher when only the overlay is visible, and accepts pointer input only in adjustment mode.

- [ ] **Step 3: Add the CI matrix**

Workflow jobs:

```yaml
service-test:
  runs-on: ubuntu-latest
  steps: go test ./internal/... -count=1

overlay-test:
  strategy:
    matrix:
      os: [windows-latest, macos-14]
  steps:
    - npm --prefix overlay ci
    - npm --prefix overlay test
    - npm --prefix overlay run build
    - cargo test --manifest-path overlay/src-tauri/Cargo.toml
    - npm --prefix overlay run tauri -- build
```

Upload Windows NSIS/MSI and macOS DMG/app artifacts. macOS signing/notarization runs only when all six repository secrets are available: `APPLE_CERTIFICATE`, `APPLE_CERTIFICATE_PASSWORD`, `APPLE_SIGNING_IDENTITY`, `APPLE_ID`, `APPLE_PASSWORD`, and `APPLE_TEAM_ID`. An unsigned artifact must be labeled `development-unsigned` and must not satisfy the formal distribution acceptance item.

- [ ] **Step 4: Run platform builds and commit**

Run on Windows: `npm --prefix overlay run tauri -- build`

Run on Apple Silicon macOS: `npm --prefix overlay run tauri -- build`

Expected: both produce installable development artifacts; signed macOS environments also pass notarization.

```bash
git add overlay/src-tauri/src/platform/macos.rs overlay/src-tauri/src/platform/mod.rs .github/workflows/overlay.yml .gitignore
git commit -m "build(overlay): add Windows and macOS delivery"
```

### Task 13: End-to-end integration, documentation, and real-platform acceptance

**Files:**
- Create: `overlay/README.md`
- Modify: `README.md`
- Modify: `internal/api/overlay_test.go`

- [ ] **Step 1: Bind the handler output to the shared fixture**

Add `TestOverlayHandlerMatchesCanonicalFixture` to `internal/api/overlay_test.go`. Construct the fake provider's `overlay.Snapshot` from fixed values, serve it through the real API handler, decode both the response and `testdata/overlay/palworld_snapshot_v1.json` into `any`, and compare them with `reflect.DeepEqual`. The TypeScript parser test from Task 4 reads the same fixture, while the Rust HTTP tests from Task 9 validate status/body transport. This makes the fixture the executable contract across all three layers without launching language toolchains from one test process.

- [ ] **Step 2: Run the complete automated suite**

Run:

```bash
go test ./... -count=1
npm --prefix webui ci
npm --prefix webui test
npm --prefix webui run build
npm --prefix overlay ci
npm --prefix overlay test
npm --prefix overlay run build
cargo fmt --manifest-path overlay/src-tauri/Cargo.toml --check
cargo clippy --manifest-path overlay/src-tauri/Cargo.toml --all-targets -- -D warnings
cargo test --manifest-path overlay/src-tauri/Cargo.toml
```

Expected: every command exits 0.

- [ ] **Step 3: Document setup and the trust boundary**

`overlay/README.md` must contain:

- Manual launch behavior and explicit absence of login autostart.
- Tailscale/ZeroTier service URL example.
- The configured base URL points at the WebUI/Caddy origin so the same origin serves `/api/v1/overlay/snapshot`, `/api/v1/players`, and `/map/tiles`; a raw sidecar-only URL cannot serve map tiles.
- Windows automatic Steam UID with manual fallback.
- macOS manual UID selection.
- Borderless/windowed-fullscreen requirement.
- No game injection or memory reading.
- Private tile loading and no public fallback.
- Build commands for Windows/macOS.
- Apple signing/notarization secret names.
- Exact real-platform smoke checklist below.

Update the root README with the snapshot endpoint, public UID semantics, and a link to the overlay README.

- [ ] **Step 4: Execute the real Windows and macOS smoke checklist**

On each platform record pass/fail for:

1. Manual launch creates only tray/menu-bar presence; no login item is registered.
2. Palworld start shows overlay within 2 seconds; exit hides it within 2 seconds.
3. Normal overlay does not focus and pointer clicks reach the game.
4. Adjustment mode drags; lock restores passthrough; restart restores display/position/scale.
5. Windows exact Steam UID preselect works or safely falls back; macOS manual UID persists.
6. Tailscale disconnect retains the in-process last snapshot and shows its age without zeroing values.
7. Offline player and disconnected provider remain visually distinct.
8. Mini-map requests only the configured private host.
9. Windows installer installs/uninstalls cleanly.
10. Signed/notarized macOS build passes Gatekeeper; if credentials are absent, mark formal macOS distribution incomplete.

- [ ] **Step 5: Commit documentation and verified integration fixes**

```bash
git add overlay/README.md README.md internal/api/overlay_test.go
git commit -m "docs(overlay): document setup and platform verification"
```

## Final completion gate

Before claiming completion:

1. Run the complete automated suite in Task 13.
2. Confirm `git diff --check` is clean.
3. Confirm no unrelated pre-existing worktree files are staged.
4. Report Windows and macOS smoke results separately.
5. If Apple credentials are unavailable, report the exact remaining Gatekeeper/notarization item; do not describe the macOS release as formally distributable.
