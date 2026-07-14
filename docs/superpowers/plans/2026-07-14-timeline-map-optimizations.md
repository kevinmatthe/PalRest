# Timeline Map Optimizations Plan

> **For agentic workers:** Execute by priority/ROI. Update this file when a task finishes: check the box, append a row to **Progress Log**.

**Goal:** Improve playback performance, map readability, and timeline UX without reopening full Replay scope.

**Architecture:** Keep pure helpers in `webui/src/map/`; fix Leaflet layer lifecycle in `PlayerTimeline` (or extracted `TimelineMap`); API pagination is a later backend task.

**Tech Stack:** React 19, Leaflet, leaflet.markercluster, Vitest.

**Related:** Spec `docs/superpowers/specs/2026-07-14-timeline-map-ux-polish-design.md` (v1 shipped on `main`).

---

## Priority matrix (ROI)

| Priority | ID | Task | ROI | Effort | Status |
|----------|-----|------|-----|--------|--------|
| P0 | O1 | Incremental map layers on cursor (no full marker rebuild while scrubbing/playing) | High | M | ✅ |
| P0 | O2 | Neutral teal MarkerCluster chrome (not size traffic-light) | High | S | ✅ |
| P1 | O3 | Active list row `scrollIntoView` while cursor moves | High | S | ✅ |
| P1 | O4 | Step prev/next + Space/Arrow keyboard shortcuts | High | S | ✅ |
| P1 | O5 | Timeline tick track: canvas/SVG or pixel-merge (avoid 500 DOM spans) | Med | M | ☐ |
| P1 | O6 | Unified location fallback copy + real zh landmark names (data) | Med | M | ☐ |
| P1 | O7 | Map follow lock (only pan when focus leaves viewport) | Med | S | ✅ |
| P2 | O8 | Extract `TimelineMap.tsx` / filters / list from `PlayerTimeline.tsx` | Med | M | ☐ |
| P2 | O9 | Virtualize timeline list (`react-window` or equivalent) | Med | M | ☐ |
| P2 | O10 | Click sample on map → seek cursor | Med | S | ✅ |
| P2 | O11 | Optional landmark overlay layer toggle | Low | M | ☐ |
| P2 | O12 | README: root Docker context + `git lfs pull` + tile fallback | Low | S | ✅ |
| P3 | O13 | Backend: trajectory pagination / downsampling + `total_count` | High (needs API) | L | ☐ |
| P3 | O14 | Time-proportional playback mode (vs index step) | Med | M | ☐ |
| P3 | O15 | Colorblind-friendly ping encoding (shape/pattern) | Low | S | ☐ |

**Execution order for this sprint:** O1 → O2 → O3 → O4 → (O7 if time) → O12 → stop for review. Defer O13+ unless requested.

---

## Task details

### O1 — Incremental map layers

**Files:** `webui/src/components/PlayerTimeline.tsx`, tests if needed

**Behavior:**
- Rebuild cluster markers only when `points` or `colorMode` changes.
- On `activeSampleKey` / cursor time change: update polyline + focus marker only; move previous active sample back into cluster and pull new active out (or equivalent without recreating all markers).
- Playing at 4× with ~200 samples must not clear/rebuild the whole cluster each step.

**Done when:** Manual or test-backed logic shows cluster rebuild is independent of cursor index; existing PlayerTimeline tests still pass.

### O2 — Teal cluster icons

**Files:** `webui/src/styles.css` and/or `iconCreateFunction` in TimelineMap

**Behavior:** Single neutral teal style + count; no green/yellow/orange by size.

### O3 — List follows cursor

**Files:** `PlayerTimeline.tsx`

**Behavior:** When `activeIndex` changes, the active `TimelineEntry` scrolls into view (`block: 'nearest'`).

### O4 — Step controls + keyboard

**Files:** `PlayerTimeline.tsx`, `mapPlayback.ts` if helpers needed, tests

**Behavior:**
- Buttons 上一步 / 下一步 next to play.
- Space toggles play when timeline focused; ←/→ step (preventDefault when not in input).

### O5–O15

See matrix; detail when scheduled.

---

## Progress Log

| Date (UTC) | ID | Result | Commit / notes |
|------------|-----|--------|----------------|
| 2026-07-14 | — | Plan created | `71f7047` this file |
| 2026-07-14 | O1 | Done — cluster rebuild only on `points`/`colorMode`; cursor updates focus/line + exclude key | `6de0197` |
| 2026-07-14 | O2 | Done — `iconCreateFunction` + `.timeline-marker-cluster` teal CSS; dropped Default.css | `6de0197` |
| 2026-07-14 | O3 | Done — active `TimelineEntry` `scrollIntoView({ block: 'nearest' })` (jsdom-safe) | `6de0197` |
| 2026-07-14 | O4 | Done — 上一步/下一步、`prevCursorIndex`、Space/←/→ on section | `6de0197` |
| 2026-07-14 | O7 | Done — `shouldPanToFocus` inset viewport; pan only when outside | `47f0de1` |
| 2026-07-14 | O10 | Done — cluster marker click → `seekTrajectorySample` | `47f0de1` |
| 2026-07-14 | O12 | Done — README root context + `git lfs pull` + runtime fallback | `47f0de1` |

---

## Out of scope (this plan)

- Full server/guild Replay
- Path invention across segment gaps
- Heatmaps
