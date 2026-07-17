# Configurable Overlay Fields Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the fixed four-timer Palworld HUD with a server-described field catalog and a locally persisted per-game layout while preserving the exact 480×76 visual structure.

**Architecture:** Add a backward-compatible `overlay.presentation/v1` endpoint beside the legacy snapshot endpoint. The Tauri client strictly parses presentation fields, resolves each configured primary/fallback pair through pure helpers, stores schema-2 per-game layouts locally, and renders the same field resolver in both the overlay and settings preview.

**Tech Stack:** Go 1.24, Rust/Tauri 2, React 19, TypeScript, Vitest, Testing Library

**Spec:** `docs/superpowers/specs/2026-07-17-overlay-configurable-fields-design.md`

---

## File map

| Responsibility | Files |
| --- | --- |
| Go presentation contract and Palworld field catalog | `internal/overlay/presentation.go`, `internal/overlay/presentation_test.go`, `internal/overlay/palworld.go`, `internal/overlay/palworld_test.go` |
| Public endpoint, validation, ETag, legacy compatibility | `internal/api/overlay.go`, `internal/api/overlay_test.go`, `internal/api/server.go` |
| Strict client contract | `overlay/src/contracts/presentation.ts`, `overlay/src/contracts/presentation.test.ts` |
| Layout defaults, primary/fallback resolution and generic formatting | `overlay/src/core/layout.ts`, `overlay/src/core/layout.test.ts` |
| Schema-2 config and migration | `overlay/src/core/config.ts`, `overlay/src/core/config.test.ts`, `overlay/src-tauri/src/config.rs` |
| Restricted HTTP bridge and presentation poller | `overlay/src/core/bridge.ts`, `overlay/src/core/bridge.test.ts`, `overlay/src/core/presentationPoller.ts`, `overlay/src/core/presentationPoller.test.ts`, `overlay/src-tauri/src/http.rs`, `overlay/src-tauri/src/lib.rs` |
| Fixed HUD with configurable content | `overlay/src/components/OverlayBar.tsx`, `overlay/src/components/OverlayBar.test.tsx`, `overlay/src/components/PlayerBadge.tsx`, `overlay/src/components/PlayerBadge.test.tsx`, `overlay/src/App.tsx`, `overlay/src/App.test.tsx`, `overlay/src/styles.css` |
| Settings editor and live preview | `overlay/src/settings/HudLayoutEditor.tsx`, `overlay/src/settings/HudLayoutEditor.test.tsx`, `overlay/src/settings/SettingsView.tsx`, `overlay/src/settings/SettingsView.test.tsx`, `overlay/src/styles.css` |

### Task 1: Define the presentation contract and Palworld fields

**Files:**
- Create: `internal/overlay/presentation.go`
- Create: `internal/overlay/presentation_test.go`
- Modify: `internal/overlay/palworld.go`
- Modify: `internal/overlay/palworld_test.go`

- [ ] **Step 1: Write failing contract tests**

Create table-driven tests for available/unavailable field invariants and stable Palworld IDs. The core fixture must assert all fourteen IDs in provider order:

```go
wantIDs := []string{
    "identity.account", "identity.uid", "identity.level",
    "presence.status", "presence.last_online",
    "network.latency", "location.coordinates",
    "activity.today", "activity.week",
    "policy.strategy", "policy.cycle_used", "policy.remaining",
    "policy.period_end", "policy.enforcement",
}
```

Tests must cover online, offline, unknown freshness, disabled policy, exempt policy, warning/danger tone, cooldown rest labels, and enforcement states. Unavailable fields must remain present without `value` or `progress`.

- [ ] **Step 2: Run tests and verify RED**

Run: `go test ./internal/overlay -run 'Presentation|PalworldProviderBuildsPresentation' -count=1`

Expected: FAIL because `Presentation`, `DisplayField`, and `PalworldProvider.Presentation` do not exist.

- [ ] **Step 3: Add the typed presentation model**

Implement the following public shapes and validation helpers in `presentation.go`:

```go
const PresentationSchemaV1 = "overlay.presentation/v1"

type Presentation struct {
    Schema       string         `json:"schema"`
    GameID       string         `json:"game_id"`
    UserID       string         `json:"user_id"`
    ObservedAt   time.Time      `json:"observed_at"`
    FreshUntil   time.Time      `json:"fresh_until"`
    SourceStatus string         `json:"source_status"`
    Identity     Identity       `json:"identity"`
    Map          *MapPosition   `json:"map,omitempty"`
    Fields       []DisplayField `json:"fields"`
}

type DisplayField struct {
    ID        string          `json:"id"`
    Label     string          `json:"label"`
    Kind      string          `json:"kind"`
    Available bool            `json:"available"`
    Value     json.RawMessage `json:"value,omitempty"`
    Tone      string          `json:"tone"`
    Progress  *float64        `json:"progress,omitempty"`
}
```

Provide constructors for string, number, timestamp, coordinates, and unavailable fields so Provider code cannot accidentally emit an invalid kind/value pair. `ValidatePresentation` must reject duplicate/unsafe IDs, unsupported kinds or tones, invalid kind-specific JSON, progress outside `[0,1]`, missing value for available fields, and value/progress on unavailable fields.

- [ ] **Step 4: Build Palworld presentation fields**

Extract the existing shared snapshot inputs (Guard snapshot, daily activity, freshness) into a private `palworldView` helper used by both legacy `Snapshot` and new `Presentation`. Keep legacy timer IDs and JSON unchanged. Map strategy/enforcement values to compact public labels in the Provider; never expose warnings, raw policy documents, IP, or private samples.

- [ ] **Step 5: Verify GREEN and legacy compatibility**

Run: `go test ./internal/overlay -count=1`

Expected: all overlay tests pass, including unchanged legacy snapshot fixtures.

- [ ] **Step 6: Commit**

```bash
git add internal/overlay/presentation.go internal/overlay/presentation_test.go internal/overlay/palworld.go internal/overlay/palworld_test.go
git commit -m "feat(overlay): describe Palworld presentation fields"
```

### Task 2: Add the backward-compatible presentation endpoint

**Files:**
- Modify: `internal/api/overlay.go`
- Modify: `internal/api/overlay_test.go`
- Modify: `internal/api/server.go`

- [ ] **Step 1: Write failing API tests**

Add tests for `GET /api/v1/overlay/presentation` covering valid response, exact query validation, provider errors, ETag/304, one-player scope, and forbidden-key scanning. Assert the legacy `/api/v1/overlay/snapshot` fixture remains byte-semantically unchanged.

```go
for _, forbidden := range []string{"ip", "password", "credential", "private_samples", "warnings"} {
    if bytes.Contains(bytes.ToLower(body), []byte(forbidden)) {
        t.Fatalf("presentation leaked forbidden key %q", forbidden)
    }
}
```

- [ ] **Step 2: Run API tests and verify RED**

Run: `go test ./internal/api -run 'OverlayPresentation|OverlaySnapshot' -count=1`

Expected: presentation requests return `404 not_found` because the route is absent.

- [ ] **Step 3: Extend the provider interface and handler**

Extend `OverlayProvider` with:

```go
Presentation(context.Context, string, string) (overlay.Presentation, error)
```

Register `GET /api/v1/overlay/presentation`. Reuse `overlayQueryValue`, stable error mapping, canonical JSON marshaling, SHA-256 ETag, `Cache-Control: no-cache`, and conditional `304`. Call `ValidatePresentation` before writing a 200 response.

- [ ] **Step 4: Verify endpoint and full Go suite**

Run: `go test ./internal/api -count=1 && go test ./...`

Expected: all tests pass; both old and new overlay endpoints are available.

- [ ] **Step 5: Commit**

```bash
git add internal/api/overlay.go internal/api/overlay_test.go internal/api/server.go
git commit -m "feat(api): expose overlay presentation catalog"
```

### Task 3: Strictly parse and resolve configurable fields

**Files:**
- Create: `overlay/src/contracts/presentation.ts`
- Create: `overlay/src/contracts/presentation.test.ts`
- Create: `overlay/src/core/layout.ts`
- Create: `overlay/src/core/layout.test.ts`
- Modify: `overlay/src/games/types.ts`
- Modify: `overlay/src/games/palworld/adapter.ts`
- Modify: `overlay/src/games/palworld/adapter.test.ts`

- [ ] **Step 1: Write failing parser tests**

Cover every field kind, unavailable fields, duplicate/unsafe IDs, invalid RFC3339, non-finite numbers, wrong value type, unsupported tone/kind, invalid progress, and `available=false` carrying value/progress. The parser must return a discriminated union, not `any`.

- [ ] **Step 2: Write failing layout-resolution tests**

Define the wished-for API and prove primary, fallback, double-missing, unknown ID, four-slot stability, progress auto/field/hidden, and compact formatting:

```ts
const resolved = resolveSlot(fieldMap, {
  primary: 'network.latency',
  fallback: 'presence.last_online',
})
expect(resolved.field.id).toBe('presence.last_online')
expect(resolved.usedFallback).toBe(true)

expect(formatDisplayField(durationField(7_200_000))).toBe('2小时')
expect(formatDisplayField(latencyField(38.5))).toBe('39 ms')
```

- [ ] **Step 3: Run focused tests and verify RED**

Run: `npm --prefix overlay test -- --run src/contracts/presentation.test.ts src/core/layout.test.ts`

Expected: FAIL because the new modules do not exist.

- [ ] **Step 4: Implement parser, layout types and defaults**

Create these stable client types:

```ts
export type SlotSelection = { primary: string; fallback: string }
export type LeftSelection = {
  primary: 'map' | 'player_badge'
  fallback: 'map' | 'player_badge'
}
export type ProgressSelection = {
  mode: 'auto' | 'field' | 'hidden'
  field?: string
}
export type LayoutProfile = {
  left: LeftSelection
  slots: [SlotSelection, SlotSelection, SlotSelection, SlotSelection]
  progress: ProgressSelection
}
```

Export `PALWORLD_DEFAULT_LAYOUT` exactly as approved. Add it to `palworldAdapter.defaultLayout`; keep process hints and title. `resolveSlot` must preserve the primary label with `--` when both values are missing. `resolveProgress` in auto mode tries the preferred field first, then Provider order.

- [ ] **Step 5: Implement generic formatting**

Format duration, timestamp, integer, latency, coordinates, text and status without game-specific rules. Timestamp slots use local `HH:mm` for the current day and compact `M月D日 HH:mm` otherwise; missing values render `--`. Clamp display text with CSS later, not by silently changing raw values.

- [ ] **Step 6: Verify GREEN and commit**

Run: `npm --prefix overlay test -- --run src/contracts/presentation.test.ts src/core/layout.test.ts src/games/palworld/adapter.test.ts`

```bash
git add overlay/src/contracts/presentation.ts overlay/src/contracts/presentation.test.ts overlay/src/core/layout.ts overlay/src/core/layout.test.ts overlay/src/games/types.ts overlay/src/games/palworld/adapter.ts overlay/src/games/palworld/adapter.test.ts
git commit -m "feat(overlay): resolve server-described display fields"
```

### Task 4: Migrate local configuration to schema 2

**Files:**
- Modify: `overlay/src/core/config.ts`
- Modify: `overlay/src/core/config.test.ts`
- Modify: `overlay/src-tauri/src/config.rs`

- [ ] **Step 1: Write failing TypeScript migration tests**

Assert schema-1 input returns schema 2 with the Palworld default profile, schema-2 round-trips custom layout, exactly-four slots and distinct primary/fallback are enforced, unknown but safely formatted field IDs survive, and future schemas are rejected.

- [ ] **Step 2: Write failing Rust migration and merge tests**

Add fixtures proving unversioned and schema-1 files migrate to schema 2; geometry-only save retains `layouts`; editable settings save replaces `layouts` but retains newer native geometry; future schema bytes are never overwritten.

- [ ] **Step 3: Run focused tests and verify RED**

Run:

```bash
npm --prefix overlay test -- --run src/core/config.test.ts
CARGO_TARGET_DIR=overlay/src-tauri/target /root/.cargo/bin/cargo test --manifest-path overlay/src-tauri/Cargo.toml --no-default-features config::tests
```

Expected: schema-2 fixtures are rejected by current schema-1 validators.

- [ ] **Step 4: Implement TypeScript schema 2**

Make `parseOverlayConfig` return `OverlayConfigV2` for both schema 1 and 2. Schema 2 uses `gameId: string` and `layouts: Record<string, LayoutProfile>`. `buildOverlayConfig` always emits schema 2 and accepts the edited current-game profile.

- [ ] **Step 5: Implement Rust schema 2 and migration**

Add serializable `SlotSelection`, `LeftSelection`, `ProgressSelection`, and `LayoutProfile`. Decode missing/schema-1 layouts by injecting the exact Palworld default. Validate safe IDs with a bounded ASCII rule such as `^[a-z0-9][a-z0-9._-]{0,95}$` without adding a regex dependency. Update editable merge to copy layouts; keep native geometry ownership unchanged.

- [ ] **Step 6: Verify GREEN and commit**

Run both focused commands again, then:

```bash
git add overlay/src/core/config.ts overlay/src/core/config.test.ts overlay/src-tauri/src/config.rs
git commit -m "feat(overlay): persist per-game HUD layouts"
```

### Task 5: Fetch and poll presentation data through Tauri

**Files:**
- Modify: `overlay/src-tauri/src/http.rs`
- Modify: `overlay/src-tauri/src/lib.rs`
- Modify: `overlay/src/core/bridge.ts`
- Modify: `overlay/src/core/bridge.test.ts`
- Create: `overlay/src/core/presentationPoller.ts`
- Create: `overlay/src/core/presentationPoller.test.ts`
- Delete after migration: `overlay/src/core/poller.ts`
- Delete after migration: `overlay/src/core/poller.test.ts`

- [ ] **Step 1: Write failing Rust HTTP tests**

Require `/api/v1/overlay/presentation`, exact encoded game/user query, ETag forwarding, 200/304/404/503 mapping, one-MiB limit, timeout, redirect policy, raw-dot-segment rejection, and legacy-service `404 not_found` mapping to `presentation_unsupported`.

- [ ] **Step 2: Write failing bridge and poller tests**

Copy the proven retry/freshness/abort cases from `poller.test.ts`, changing the success schema and parser. Add terminal `presentation_unsupported`, identity mismatch, game mismatch, malformed body retry, and old request abortion when config changes.

- [ ] **Step 3: Verify RED**

Run:

```bash
CARGO_TARGET_DIR=overlay/src-tauri/target /root/.cargo/bin/cargo test --manifest-path overlay/src-tauri/Cargo.toml --no-default-features http::tests
npm --prefix overlay test -- --run src/core/bridge.test.ts src/core/presentationPoller.test.ts
```

- [ ] **Step 4: Implement restricted fetch command**

Add `PresentationRequest`, `PresentationResult`, `HttpBridge::fetch_presentation`, and Tauri command `fetch_presentation`. Reuse the existing serialized HTTP invoke gate; keep `list_players` behavior unchanged. Do not allow arbitrary paths or hosts.

- [ ] **Step 5: Implement PresentationPoller**

Preserve the existing 2-second poll, 5-second request timeout, exponential retry, ETag, freshness timers, stale/disconnected last-good data, terminal player/game/version errors, listener isolation, and abort semantics. Parse only `overlay.presentation/v1`.

- [ ] **Step 6: Migrate all bridge consumers and remove dead poller**

Expose `fetchPresentation` on `OverlayBridge`; keep no unused snapshot command in the new frontend. Remove `poller.ts` only after `App` migration in Task 6 compiles; if commits are kept buildable, defer the actual delete to Task 6.

- [ ] **Step 7: Verify GREEN and commit**

```bash
git add overlay/src-tauri/src/http.rs overlay/src-tauri/src/lib.rs overlay/src/core/bridge.ts overlay/src/core/bridge.test.ts overlay/src/core/presentationPoller.ts overlay/src/core/presentationPoller.test.ts
git commit -m "feat(overlay): poll presentation data through Tauri"
```

### Task 6: Render the fixed HUD from the configured layout

**Files:**
- Create: `overlay/src/components/PlayerBadge.tsx`
- Create: `overlay/src/components/PlayerBadge.test.tsx`
- Modify: `overlay/src/components/OverlayBar.tsx`
- Modify: `overlay/src/components/OverlayBar.test.tsx`
- Modify: `overlay/src/App.tsx`
- Modify: `overlay/src/App.test.tsx`
- Modify: `overlay/src/styles.css`
- Delete: `overlay/src/core/poller.ts`
- Delete: `overlay/src/core/poller.test.ts`
- Delete after production references reach zero: `overlay/src/contracts/snapshot.ts`
- Delete after production references reach zero: `overlay/src/contracts/snapshot.test.ts`

- [ ] **Step 1: Write failing component tests**

Cover fixed identity header; map→badge fallback; badge initial/level/status; approved four default slots; custom ordering; primary/fallback/double-missing `--`; field tone; overall highest risk; progress auto/field/hidden; offline/stale/disconnected desaturation; deep drag region; exact 480×76 and four equal columns.

- [ ] **Step 2: Write failing App integration tests**

Assert `LiveOverlay` creates `PresentationPoller` with current config, uses `config.layouts[gameId]` or adapter default, and hot config events replace both data request and layout without WebView reload. Preserve listener-registration-before-bootstrap and old-request abort tests.

- [ ] **Step 3: Run focused tests and verify RED**

Run: `npm --prefix overlay test -- --run src/components/PlayerBadge.test.tsx src/components/OverlayBar.test.tsx src/App.test.tsx`

- [ ] **Step 4: Implement PlayerBadge and configurable OverlayBar**

`OverlayBar` receives `{ presentation, layout, status, adjustMode, scale, mapBaseUrl }`. Resolve fields only through `layout.ts`; render four stable cells regardless availability. Keep header fixed. The left module checks configured primary then fallback. Use the existing map only when presentation map data and trusted base URL exist.

- [ ] **Step 5: Preserve the approved visual geometry**

Keep `--overlay-width: 30rem`, `--overlay-height: 4.75rem`, `--overlay-map-size: 3.875rem`, frame padding `0.375rem`, fixed content rows, quiet-glass colors, and 2px rail. Add only selectors required for badge and field kinds. No settings-window selectors may accidentally inherit overlay-only geometry.

- [ ] **Step 6: Migrate App and remove legacy client modules**

Replace `SnapshotPoller` with `PresentationPoller`. Keep the same loading/error compact states and config-event race protections. Remove legacy client snapshot/poller modules only when `rg 'SnapshotPoller|parseSnapshot' overlay/src` returns no production references.

- [ ] **Step 7: Verify GREEN, build, and commit**

Run:

```bash
npm --prefix overlay test -- --run src/components/PlayerBadge.test.tsx src/components/OverlayBar.test.tsx src/App.test.tsx
npm --prefix overlay run build
```

```bash
git add overlay/src/components overlay/src/App.tsx overlay/src/App.test.tsx overlay/src/styles.css overlay/src/core/poller.ts overlay/src/core/poller.test.ts overlay/src/contracts/snapshot.ts overlay/src/contracts/snapshot.test.ts
git commit -m "feat(overlay): render configurable HUD slots"
```

### Task 7: Add the HUD layout editor and live preview

**Files:**
- Create: `overlay/src/settings/HudLayoutEditor.tsx`
- Create: `overlay/src/settings/HudLayoutEditor.test.tsx`
- Modify: `overlay/src/settings/SettingsView.tsx`
- Modify: `overlay/src/settings/SettingsView.test.tsx`
- Modify: `overlay/src/styles.css`

- [ ] **Step 1: Write failing editor tests**

Cover grouped field options, unavailable-but-selectable fields, left primary/fallback, exactly four slot rows, duplicate primary/fallback prevention, progress modes, live preview using the same resolver, reset-current-game-only, and accessible labels.

- [ ] **Step 2: Write failing SettingsView integration tests**

After exact player selection, fetch presentation with abort support. Test player/base URL changes abort old preview requests. Saving must persist schema 2 layout, call existing adjustment synchronization, emit the existing hot-config flow, and keep the “saved but sync failed” message. Reset must not change URL, UID, scale, lock, or geometry.

- [ ] **Step 3: Write CSS contract tests for 560×520**

Assert settings shell and form retain `height:100%`, independently scrolling content, fixed/reachable footer, stacked preview above controls at 560px, touch-sized controls, and no increase to Tauri settings dimensions.

- [ ] **Step 4: Run focused tests and verify RED**

Run: `npm --prefix overlay test -- --run src/settings/HudLayoutEditor.test.tsx src/settings/SettingsView.test.tsx`

- [ ] **Step 5: Implement editor and preview loading**

`HudLayoutEditor` is controlled: `{ presentation, layout, defaultLayout, onChange }`. `SettingsView` owns preview loading and abort generation. When presentation endpoint is unsupported, show `服务版本不支持可配置字段` and disable layout save rather than silently discarding selections.

- [ ] **Step 6: Implement compact responsive settings CSS**

At the real 560px client width, use a single column: preview first, controls below. Keep the action footer outside `.settings-form__content`. Use the approved quiet-glass preview without changing the 480×76 overlay itself.

- [ ] **Step 7: Verify GREEN, full frontend, and commit**

Run:

```bash
npm --prefix overlay test -- --run src/settings/HudLayoutEditor.test.tsx src/settings/SettingsView.test.tsx
npm --prefix overlay test
npm --prefix overlay run build
```

```bash
git add overlay/src/settings/HudLayoutEditor.tsx overlay/src/settings/HudLayoutEditor.test.tsx overlay/src/settings/SettingsView.tsx overlay/src/settings/SettingsView.test.tsx overlay/src/styles.css
git commit -m "feat(overlay): configure per-game HUD layouts"
```

### Task 8: Full verification, review, and release handoff

**Files:**
- Modify only if verification exposes a scoped defect.

- [ ] **Step 1: Verify all frontend behavior**

Run: `npm --prefix overlay test && npm --prefix overlay run build`

Expected: all Vitest files and the production Vite build pass.

- [ ] **Step 2: Verify all Rust behavior without system package changes**

Run:

```bash
CARGO_TARGET_DIR=overlay/src-tauri/target /root/.cargo/bin/cargo test --manifest-path overlay/src-tauri/Cargo.toml --no-default-features
CARGO_TARGET_DIR=overlay/src-tauri/target /root/.cargo/bin/cargo clippy --manifest-path overlay/src-tauri/Cargo.toml --no-default-features --all-targets -- -D warnings
/root/.cargo/bin/cargo fmt --manifest-path overlay/src-tauri/Cargo.toml -- --check
```

Expected: tests, Clippy, and formatting pass. Do not use apt, sudo, or install system packages. Record native Linux build as unavailable if GLib remains absent.

- [ ] **Step 3: Verify Go and repository state**

Run: `go test ./... && git diff --check && git status --short`

Expected: all Go packages pass; only explicitly preserved user Docker/ignore edits may remain outside the implementation commits.

- [ ] **Step 4: Perform independent review**

Request spec-compliance and code-quality review for every task, then a final integrated review. Resolve all Critical and Important findings before merging.

- [ ] **Step 5: Merge and platform smoke handoff**

Use `superpowers:finishing-a-development-branch`. After the selected integration path, rerun frontend, Rust no-default, and Go tests on the merged result. CI-built macOS and Windows packages must smoke-test: save layout → overlay hot update, online→offline map/badge fallback, lock/click-through, restart persistence, restore defaults, and unchanged 480×76 geometry.
