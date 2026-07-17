# Overlay Visual and Drag Restoration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore the approved 480×76 quiet-glass overlay and make every non-interactive child area draggable on macOS while adjustment mode is active.

**Architecture:** Keep the snapshot contract, game adapter, mini-map, settings UI, and Rust lifecycle unchanged. Make the React overlay root expose one deep Tauri drag region only during adjustment, grant exactly the required window command, and express the approved visual system through shared CSS semantic variables so normal, warning, danger, muted, stale, and disconnected states retain one layout.

**Tech Stack:** React 19, TypeScript, CSS, Vitest/Testing Library, Tauri 2 capabilities, Rust unit tests.

---

## File structure

- Modify `overlay/src/components/OverlayBar.tsx` — deep drag-region ownership for live snapshots.
- Modify `overlay/src/components/OverlayBar.test.tsx` — drag contract and quiet-glass layout/style regression checks.
- Modify `overlay/src/App.tsx` — deep drag-region ownership for compact loading/error states.
- Modify `overlay/src/App.test.tsx` — compact-state drag contract.
- Modify `overlay/src/styles.css` — approved quiet-glass tokens, fixed two-row/four-column grid, semantic state colors, adjustment outline.
- Modify `overlay/src-tauri/capabilities/default.json` — grant only Tauri window start-dragging in addition to existing event listeners.
- Modify `overlay/src-tauri/src/config.rs` — exact capability regression test.

No Go, snapshot contract, settings, map projection, polling, lifecycle, or workflow files change.

### Task 1: Restore the native drag contract

**Files:**
- Modify: `overlay/src/components/OverlayBar.test.tsx`
- Modify: `overlay/src/App.test.tsx`
- Modify: `overlay/src-tauri/src/config.rs`
- Modify: `overlay/src/components/OverlayBar.tsx`
- Modify: `overlay/src/App.tsx`
- Modify: `overlay/src-tauri/capabilities/default.json`

- [ ] **Step 1: Write failing React tests for deep-only adjustment dragging**

In `OverlayBar.test.tsx`, replace the loose adjustment assertions with exact ownership:

```tsx
expect(overlay).toHaveAttribute('data-tauri-drag-region', 'deep')
expect(dragHint).not.toHaveAttribute('data-tauri-drag-region')
expect(screen.getByTestId('capability-identity')).toBeInstanceOf(HTMLElement)
expect(screen.getByTestId('capability-timers')).toBeInstanceOf(HTMLElement)
```

Keep the existing assertion that normal mode has no drag attribute. In `App.test.tsx`, require the compact state root to use the same value:

```tsx
expect(loading.closest('main')).toHaveAttribute('data-tauri-drag-region', 'deep')
```

- [ ] **Step 2: Write the failing exact capability test**

Rename the config test to `capability_allows_only_required_event_and_drag_operations` and expect:

```rust
assert_eq!(
    capability["permissions"],
    serde_json::json!([
        "core:event:allow-listen",
        "core:event:allow-unlisten",
        "core:window:allow-start-dragging"
    ])
);
```

- [ ] **Step 3: Run focused tests and verify RED**

Run:

```bash
cd overlay && npm test -- --run src/components/OverlayBar.test.tsx src/App.test.tsx
/root/.cargo/bin/cargo test --manifest-path overlay/src-tauri/Cargo.toml --no-default-features capability_allows_only_required_event_and_drag_operations
```

Expected: React fails because the attribute serializes as `true`; Rust fails because the permission list has only two event entries.

- [ ] **Step 4: Implement one deep drag owner per overlay root**

In `OverlayBar.tsx`:

```tsx
const dragProps = adjustMode ? { 'data-tauri-drag-region': 'deep' } : {}
```

Remove `data-tauri-drag-region` from `.overlay__drag-hint`; the deep root owns the entire non-interactive subtree.

In `App.tsx`, change `CompactState` to:

```tsx
function CompactState({ children, adjustMode = false }: { children: string; adjustMode?: boolean }) {
  const dragProps = adjustMode ? { 'data-tauri-drag-region': 'deep' } : {}
  return <main className={`overlay-state${adjustMode ? ' overlay-state--adjusting' : ''}`} role="status" {...dragProps}>
    <span>{children}</span>
    {adjustMode ? <span className="overlay__drag-hint">拖动调整位置</span> : null}
  </main>
}
```

In `default.json`, append only:

```json
"core:window:allow-start-dragging"
```

- [ ] **Step 5: Run focused tests and verify GREEN**

Run the two commands from Step 3.

Expected: all focused React tests and the capability test pass.

- [ ] **Step 6: Commit the drag fix**

```bash
git add overlay/src/components/OverlayBar.tsx overlay/src/components/OverlayBar.test.tsx overlay/src/App.tsx overlay/src/App.test.tsx overlay/src-tauri/capabilities/default.json overlay/src-tauri/src/config.rs
git -c commit.gpgsign=false commit -m "fix(overlay): enable deep native dragging"
```

### Task 2: Restore the approved quiet-glass overlay

**Files:**
- Modify: `overlay/src/components/OverlayBar.test.tsx`
- Modify: `overlay/src/styles.css`

- [ ] **Step 1: Write failing quiet-glass CSS contract assertions**

Extend the nominal overlay test with these structural and visual requirements:

```tsx
expect(css).toMatch(/\.overlay\s*\{[^}]*--overlay-panel:\s*rgba\(8,\s*18,\s*22,\s*0\.84\)/s)
expect(css).toMatch(/\.overlay\s*\{[^}]*border-radius:\s*0\.875rem/s)
expect(css).toMatch(/\.overlay\s*\{[^}]*backdrop-filter:\s*blur\(1rem\)\s+saturate\(115%\)/s)
expect(css).toMatch(/\.overlay__content\s*\{[^}]*grid-template-rows:\s*1\.5625rem\s+1\.9375rem/s)
expect(css).toMatch(/\.overlay__telemetry\s*\{[^}]*border-bottom:\s*1px solid rgba\(255,\s*255,\s*255,\s*0\.06\)/s)
expect(css).toMatch(/\.overlay__timers\s*\{[^}]*grid-template-columns:\s*repeat\(4,\s*minmax\(0,\s*1fr\)\)/s)
expect(css).not.toMatch(/\.overlay\s*\{[^}]*repeating-linear-gradient/s)
```

Extend the warning/danger test to assert that both classes continue to exist and use semantic accent variables without adding alert roles or animation.

- [ ] **Step 2: Run the component test and verify RED**

Run:

```bash
cd overlay && npm test -- --run src/components/OverlayBar.test.tsx
```

Expected: failures for panel alpha, radius, blur, fixed row heights, fixed four-column grid, and industrial repeating texture.

- [ ] **Step 3: Implement the shared quiet-glass tokens**

Update the `.overlay` variables and container to the approved values:

```css
.overlay {
  --overlay-panel: rgba(8, 18, 22, 0.84);
  --overlay-edge: rgba(160, 230, 229, 0.3);
  --overlay-accent: var(--overlay-cyan);
  border-radius: 0.875rem;
  background:
    linear-gradient(115deg, rgba(116, 230, 226, 0.08), transparent 34%),
    var(--overlay-panel);
  box-shadow: inset 0 1px rgba(255, 255, 255, 0.12), 0 0.75rem 1.75rem rgba(0, 0, 0, 0.5);
  backdrop-filter: blur(1rem) saturate(115%);
}
```

Remove the industrial repeating diagonal texture and decorative top/bottom scan-line pseudo-element. Keep `box-sizing`, fixed nominal dimensions, isolation, typography, and transitions.

- [ ] **Step 4: Lock the approved pixel grid**

Use these layout rules:

```css
.overlay__frame {
  grid-template-columns: var(--overlay-map-size) minmax(0, 1fr);
  gap: 0.5rem;
  padding: 0.375rem;
  padding-bottom: 0.5rem;
}

.overlay__content {
  grid-template-rows: 1.5625rem 1.9375rem;
}

.overlay__telemetry {
  align-items: center;
  border-bottom: 1px solid rgba(255, 255, 255, 0.06);
}

.overlay__timers {
  grid-template-columns: repeat(4, minmax(0, 1fr));
  align-items: center;
}
```

Retain `min-width: 0`, overflow ellipsis, no-map expansion, and the two-pixel policy rail.

- [ ] **Step 5: Apply the approved quiet details and semantic states**

- Give the map/fallback locator a `0.625rem` radius, subtle accent edge, inner high-light, and no industrial texture in the information area.
- Keep timer separators at `rgba(255, 255, 255, 0.09)` and use a pale neutral value by default.
- Keep warning/danger/muted timer value overrides.
- Make `.overlay--warning` and `.overlay--danger` set `--overlay-accent`, `--overlay-edge`, and semantic rail color without duplicating the full layout.
- Keep `.overlay--muted`, `.overlay--stale`, and `.overlay--disconnected` at reduced saturation while preserving values.
- Make `.overlay--adjusting` use a one-pixel dashed inset outline with no positive outline offset, so the fixed transparent window does not clip it.
- Preserve `prefers-contrast: more` and `prefers-reduced-motion: reduce` blocks.

- [ ] **Step 6: Run focused tests and build**

```bash
cd overlay && npm test -- --run src/components/OverlayBar.test.tsx src/components/PalworldMiniMap.test.tsx && npm run build
```

Expected: focused tests pass and Vite production build exits 0.

- [ ] **Step 7: Commit the visual restoration**

```bash
git add overlay/src/components/OverlayBar.test.tsx overlay/src/styles.css
git -c commit.gpgsign=false commit -m "style(overlay): restore quiet glass HUD"
```

### Task 3: Verify the complete desktop overlay

**Files:**
- Verify only; no planned production changes.

- [ ] **Step 1: Run the full frontend suite and build**

```bash
cd overlay && npm test && npm run build
```

Expected: all Vitest files pass and the production bundle builds.

- [ ] **Step 2: Run Rust tests, lint, and formatting without system packages**

```bash
/root/.cargo/bin/cargo test --manifest-path overlay/src-tauri/Cargo.toml --no-default-features
/root/.cargo/bin/cargo clippy --manifest-path overlay/src-tauri/Cargo.toml --no-default-features --all-targets -- -D warnings
/root/.cargo/bin/cargo fmt --manifest-path overlay/src-tauri/Cargo.toml -- --check
```

Expected: every command exits 0. Do not run `apt`, `sudo`, or install GLib/GTK packages.

- [ ] **Step 3: Verify repository boundaries**

```bash
git diff --check
git status --short
git diff HEAD~2 --name-only
```

Expected: implementation commits contain only the seven planned overlay files. Existing user modifications to `.dockerignore`, `.gitignore`, `Dockerfile`, and `webui/Dockerfile` remain uncommitted and untouched.

- [ ] **Step 4: Request code review**

Review against `docs/superpowers/specs/2026-07-17-overlay-visual-drag-design.md`, specifically checking Tauri permission scope, `deep` drag semantics, fixed 480×76 geometry, semantic state styles, and settings isolation. Resolve every Critical or Important finding before completion.

- [ ] **Step 5: Record platform smoke-test boundary**

Local Linux verification cannot prove native macOS dragging. After authenticated push, require the macOS GitHub build and test dragging from map, identity, timer, and blank regions; then lock and verify pointer passthrough. Run the same smoke test on Windows to guard the shared deep-drag attribute.
