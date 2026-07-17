# macOS Save Live Update Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep the configured macOS overlay visible after saving and make a successful settings save update the overlay's poller without a WebView reload.

**Architecture:** The native save command will persist, reload, and emit the canonical config only to the overlay window. The overlay bridge validates that event through the existing config parser and replaces `LiveOverlay` configuration, which disposes the old poller. Native lifecycle policy remains process-gated on Windows, while macOS treats a valid saved config as the visibility signal because native process discovery is not reliable for Whisky/CrossOver.

**Tech Stack:** Rust, Tauri 2, React 19, TypeScript, Vitest, Testing Library

---

### Task 1: Hot-update the overlay configuration

**Files:**
- Modify: `overlay/src/core/bridge.ts`
- Modify: `overlay/src/core/bridge.test.ts`
- Modify: `overlay/src/App.tsx`
- Modify: `overlay/src/App.test.tsx`
- Modify: `overlay/src-tauri/src/lib.rs`
- Modify: `overlay/src-tauri/src/config.rs`

- [ ] **Step 1: Write failing bridge and App tests**

Add `onConfigChanged` to the wished-for bridge contract. In `bridge.test.ts`, assert that it subscribes to `overlay-config-changed`, forwards the payload, and returns the native unlisten handle. In `App.test.tsx`, capture the handler, begin an old-UID request, publish a valid new config, and assert that the old request is aborted and the next request uses the new UID.

```tsx
let publishConfig!: (value: unknown) => void
const oldRequest = deferred<FetchSnapshotResult>()
const api = bridge({
  onConfigChanged: vi.fn(async (handler) => {
    publishConfig = handler
    return () => {}
  }),
  fetchSnapshot: vi.fn((request) => request.userId === 'uid' ? oldRequest.promise : Promise.resolve(newSnapshot)),
})
render(<App bridge={api} />)
await waitFor(() => expect(api.fetchSnapshot).toHaveBeenCalledWith(expect.objectContaining({ userId: 'uid' }), expect.any(AbortSignal)))
publishConfig({ ...config, userId: 'uid-2' })
await waitFor(() => expect(api.fetchSnapshot).toHaveBeenCalledWith(expect.objectContaining({ userId: 'uid-2' }), expect.any(AbortSignal)))
```

- [ ] **Step 2: Run the focused tests and verify RED**

Run: `npm --prefix overlay test -- --run src/core/bridge.test.ts src/App.test.tsx`

Expected: FAIL because `DesktopBridge.onConfigChanged` and the App subscription do not exist.

- [ ] **Step 3: Implement the frontend event path**

Add the optional bridge method and Tauri listener:

```ts
onConfigChanged?(handler: (config: unknown) => void): Promise<() => void>
// native bridge
onConfigChanged: (handler) => listen<unknown>('overlay-config-changed', (event) => handler(event.payload)),
```

Subscribe in `App`, parse with `parseOverlayConfig`, ignore invalid payloads, and functionally replace only a ready overlay bootstrap config. Updating the config object must recreate `SnapshotPoller`, allowing its effect cleanup to abort the old request.

- [ ] **Step 4: Make native save return and emit canonical persisted config**

Extract a config helper that saves editable fields and reloads the merged configuration, preserving native geometry. After successful persistence, `save_config` uses `Emitter::emit_to("overlay", "overlay-config-changed", &saved)`; it emits nothing on save failure.

- [ ] **Step 5: Run focused tests and verify GREEN**

Run: `npm --prefix overlay test -- --run src/core/bridge.test.ts src/App.test.tsx && CARGO_TARGET_DIR=overlay/src-tauri/target /root/.cargo/bin/cargo test --manifest-path overlay/src-tauri/Cargo.toml --no-default-features config::tests`

Expected: all focused frontend and Rust config tests pass.

- [ ] **Step 6: Commit**

```bash
git add overlay/src/core/bridge.ts overlay/src/core/bridge.test.ts overlay/src/App.tsx overlay/src/App.test.tsx overlay/src-tauri/src/lib.rs overlay/src-tauri/src/config.rs
git commit -m "fix(overlay): hot reload saved configuration"
```

### Task 2: Make configured macOS visibility independent of process discovery

**Files:**
- Modify: `overlay/src-tauri/src/lifecycle.rs`
- Modify: `overlay/src-tauri/src/lib.rs`

- [ ] **Step 1: Write failing lifecycle tests**

Add tests proving that a valid configured macOS launch starts visible and locked, a macOS configuration save produces a visibility event, and Windows remains process-gated.

```rust
#[test]
fn configured_macos_starts_visible_without_process_discovery() {
    let state = initial_lifecycle_state(true, true, "macos");
    assert!(state.game_running);
    assert!(state.overlay_visible);
    assert!(state.click_through);
}

#[test]
fn windows_still_waits_for_game_detection() {
    let state = initial_lifecycle_state(true, true, "windows");
    assert!(!state.game_running);
    assert!(!state.overlay_visible);
}
```

- [ ] **Step 2: Run the Rust lifecycle tests and verify RED**

Run: `CARGO_TARGET_DIR=overlay/src-tauri/target /root/.cargo/bin/cargo test --manifest-path overlay/src-tauri/Cargo.toml --no-default-features lifecycle::tests`

Expected: FAIL because initial state has no platform/config visibility policy.

- [ ] **Step 3: Implement the minimal platform policy**

Extend initial state construction with `has_valid_config` and platform. On macOS, a valid config initializes `game_running` and `overlay_visible` true; on Windows they remain false until detection. Initial window effects show the overlay when that state is visible. The macOS process monitor does not overwrite this state with an unreliable false result.

After a successful first save on macOS, transition the lifecycle controller with the same effective `GameDetected(true)` signal before the settings window applies `Lock`. This leaves the locked overlay visible and click-through; Windows save behavior is unchanged.

- [ ] **Step 4: Run lifecycle tests and verify GREEN**

Run: `CARGO_TARGET_DIR=overlay/src-tauri/target /root/.cargo/bin/cargo test --manifest-path overlay/src-tauri/Cargo.toml --no-default-features lifecycle::tests`

Expected: all lifecycle tests pass, including existing adjustment, geometry rollback, and Windows focus behavior.

- [ ] **Step 5: Commit**

```bash
git add overlay/src-tauri/src/lifecycle.rs overlay/src-tauri/src/lib.rs
git commit -m "fix(overlay): keep configured macOS overlay visible"
```

### Task 3: Full verification and release handoff

**Files:**
- Verify only; no unrelated source edits.

- [ ] **Step 1: Run all frontend verification**

Run: `npm --prefix overlay test && npm --prefix overlay run build`

Expected: all Vitest files pass and Vite production build exits 0.

- [ ] **Step 2: Run all native verification without system package changes**

Run: `CARGO_TARGET_DIR=overlay/src-tauri/target /root/.cargo/bin/cargo test --manifest-path overlay/src-tauri/Cargo.toml --no-default-features && CARGO_TARGET_DIR=overlay/src-tauri/target /root/.cargo/bin/cargo clippy --manifest-path overlay/src-tauri/Cargo.toml --no-default-features --all-targets -- -D warnings && /root/.cargo/bin/cargo fmt --manifest-path overlay/src-tauri/Cargo.toml -- --check`

Expected: Rust tests, Clippy, and formatting pass without apt or sudo.

- [ ] **Step 3: Run service and repository checks**

Run: `go test ./... && git diff --check`

Expected: all Go packages pass and no whitespace errors are reported.

- [ ] **Step 4: Review and publish**

Request independent review of the implementation range. Fix all Critical/Important findings, merge to `main`, preserve the existing Docker/ignore edits, and push `main` plus the release tag only when GitHub credentials are available. Record that native macOS save/lock/passthrough behavior needs a CI-built real-Mac smoke test.
