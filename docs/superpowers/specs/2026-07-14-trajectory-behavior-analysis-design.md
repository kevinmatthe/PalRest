# Trajectory Behavior Analysis Design

**Date:** 2026-07-14  
**Approach (locked):** Shared pure metrics library (isomorphic semantics); hybrid compute (window detail on client first; daily/rank later on server)  
**Pipeline:** A (sidebar + library) → C (map overlay) → B (Analytics rankings)

## Goal

Derive explainable player **behavior metrics** from existing trajectory samples—active time, movement class mix (traveling / local / idle), activity radius, path length, mean/peak speed, and sampling density—and surface them first as a **Behavior Summary** panel on the player timeline. The same definitions later power map coloring (C) and multi-player rankings (B).

## Decisions (locked)

| Item | Decision |
|------|----------|
| Delivery order | A → C → B |
| MVP surface | Player timeline sidebar (below map, full-width card) |
| Compute | Hybrid: window-level pure function in webui for A; server daily aggregates for B later |
| Metrics pack | Full pack (not minimal three) |
| Architecture | Pure `webui/src/behavior/*` library + `BehaviorSummaryPanel`; no React/Leaflet in metrics core |
| Cross-segment | Never invent movement across `segment_id` or long gaps |

## Scope

### In scope (MVP = phase A)

1. Pure TypeScript behavior metrics + unit tests.
2. Chinese formatting helpers for duration/share/speed labels.
3. `BehaviorSummaryPanel` wired into `PlayerTimeline` from currently loaded trajectory samples.
4. Empty/loading/insufficient-sample states; footnote that stats use **loaded** samples only.
5. Optional precomputation of per-edge labels in the summary type for phase C (may compute in MVP, not drawn on map yet).

### Out of scope (MVP)

- Map heat / speed ribbons / idle pins (phase C).
- Daily rollup tables, ranking APIs, Analytics page (phase B).
- Save-worker or landmark-based classification.
- ML / LLM classification.
- New polling path or changes to trajectory sampling policy.
- Claiming physical m/s accuracy (UI labels use world units / second).

## Relationship to existing systems

| Existing | Role |
|----------|------|
| `player_sessions` / analytics daily stats | Online presence time from poll success—**not** replaced by trajectory active time |
| Trajectory samples in SQLite + timeline API | Sole input for behavior metrics |
| Timeline map polish | Independent; behavior panel sits under map |
| Guard `max_observation_gap` / `trajectory_max_interval` | Inform gap/cap defaults (5 minutes) |

Trajectory **observed active ms** and session **online ms** may differ; copy must not equate them.

## Architecture

```
getPlayerTimeline(range)
  → mergeTimeline / trajectorySamples   (existing)
  → summarizeBehavior(samples, options) (new pure)
  → BehaviorSummaryPanel                (new UI)
```

```
webui/src/behavior/behaviorTypes.ts
webui/src/behavior/behaviorMetrics.ts
webui/src/behavior/behaviorMetrics.test.ts
webui/src/behavior/behaviorFormat.ts
webui/src/behavior/behaviorFormat.test.ts
webui/src/components/BehaviorSummaryPanel.tsx
webui/src/components/BehaviorSummaryPanel.test.tsx
webui/src/components/PlayerTimeline.tsx   # wire useMemo + panel
webui/src/styles.css
```

Phase C later consumes edge/point labels inside `TimelineMap`.  
Phase B later ports the same formulas to Go and persists daily summaries; JSON field names stay aligned with `BehaviorSummary`.

## Constants (defaults)

| Constant | Default | Notes |
|----------|---------|--------|
| `D_idle` | `500` | World units; micro-movement → stationary |
| `V_idle` | `50` | World units / second |
| `V_travel` | `800` | World units / second; long-distance travel |
| `T_gap` | `5 * 60_000` ms | Align with trajectory max interval |
| `T_active_cap` | `T_gap` | Cap per-edge contribution to observed active time |

Thresholds are code constants (not admin UI) in MVP; document calibration note next to exports.

## Algorithm

### Input

Ordered trajectory points:

```ts
{ observed_at: string; segment_id: string; x: number; y: number; ping?: number }
```

Window bounds: user-selected timeline `start` / `end` (ms), used for `windowMs` and gap-of-window share.

### Edges

Scan consecutive pairs after sorting by `observed_at` ascending:

1. If `segment_id` differs **or** `Δt > T_gap` → class `gap` (accumulate `gapMs` only).
2. Else `dist = hypot(Δx, Δy)`, `speed = dist / (Δt_s)` (`Δt_s = Δt/1000`; skip non-positive Δt).
3. Else if `dist < D_idle` **or** `speed < V_idle` → `stationary`.
4. Else if `speed >= V_travel` → `traveling`.
5. Else → `local`.

### Aggregates

- `observedActiveMs`: sum of `min(Δt, T_active_cap)` for non-gap edges.
- `pathLength`: sum of `dist` on non-gap edges.
- `radius`: for all finite points in the window, centroid `(mean x, mean y)`, then max Euclidean distance to centroid.
- `meanSpeed`: `pathLength / movingSeconds` where movingSeconds is duration of `local` + `traveling` edges (capped Δt); 0 if none.
- `peakSpeed`: max edge speed among non-gap edges with positive Δt.
- `sampleDensityPerHour`: `sampleCount / (observedActiveMs / 3_600_000)` when active &gt; 0; else 0.
- `classMs` / `classShare`: shares relative to `observedActiveMs` (not window).
- `gapShareOfWindow`: `gapMs / windowMs` when window &gt; 0.
- `dominantClass`: argmax of `classMs` among stationary/local/traveling; ties break `traveling > local > stationary`; if all zero → `unknown`.
- `segmentCount`: distinct `segment_id` in samples.

### Output type

```ts
type BehaviorEdgeClass = 'stationary' | 'local' | 'traveling' | 'gap';

type BehaviorSummary = {
  sampleCount: number;
  segmentCount: number;
  windowMs: number;
  observedActiveMs: number;
  pathLength: number;
  radius: number;
  meanSpeed: number;
  peakSpeed: number;
  sampleDensityPerHour: number;
  classMs: { stationary: number; local: number; traveling: number };
  classShare: { stationary: number; local: number; traveling: number };
  gapMs: number;
  gapShareOfWindow: number;
  dominantClass: 'stationary' | 'local' | 'traveling' | 'unknown';
  /** Optional for phase C; safe to populate in MVP */
  edges?: Array<{
    fromIndex: number;
    toIndex: number;
    class: BehaviorEdgeClass;
    dist: number;
    dtMs: number;
    speed: number;
  }>;
};
```

Empty or single-point input: zeros, `dominantClass: 'unknown'`, no NaN/Infinity.

## UI (phase A)

### Placement

Full-width card **below `TimelineMap`**, above the virtualized event list inside `PlayerTimeline`.

### Content

| Block | Content |
|-------|---------|
| Header | 「行为摘要」; subline with range or「基于已加载的位置样本」 |
| Mix bar | 跑图 (traveling) / 局部 (local) / 挂机 (stationary) shares |
| Grid | 观测活跃、活动半径、路径长度、均速、峰值、采样密度、点数 |
| Alert | If `gapShareOfWindow > 0.15`, soft notice「存在观测断档，活跃时长未覆盖全部日历时间」 |
| Empty | No player / no samples / still loading |

### Copy (zh)

| Key | Label |
|-----|--------|
| traveling | 跑图 |
| local | 局部 |
| stationary | 挂机 |
| observed active | 观测活跃 |
| radius | 活动半径 |
| path | 路径长度 |
| mean speed | 均速 |
| peak speed | 峰值速度 |
| density | 采样密度 |
| units distance | 世界坐标 |
| units speed | 坐标/秒 |

Use existing `formatDuration` for ms fields. Distance/speed as rounded numbers + unit suffix.

### Interaction

- Recompute via `useMemo` on `samples` + window bounds only (not playhead).
- No new admin config controls in MVP.

## Phase C / B hooks (not implemented in A)

### C — Map overlay

- Color trajectory edges or points by `BehaviorEdgeClass`.
- Idle clusters as pins; optional speed styling for `traveling`.
- Reuse `summarizeBehavior` edge list; no second classifier.

### B — Rankings

- Server job or on-read aggregation: per-user per-day `BehaviorSummary`-like row.
- Analytics page: rank by traveling share, idle share, radius, path length.
- Same thresholds documented in one place (shared constant table in design / code comments).

## Testing

### Unit (`behaviorMetrics`)

- All-stationary pair.
- Uniform high-speed travel.
- Local wander (mid speed, bounded radius).
- Cross-segment gap (no path across).
- Δt &gt; T_gap → gap; active cap applied.
- Empty / one point → safe zeros.
- Dominant class tie-break.

### Unit (`behaviorFormat`)

- Share percentages sum display; duration strings; speed/distance labels.

### Component

- Renders mix + grid when summary has samples.
- Empty copy without samples.
- Gap notice when share high (inject summary fixture).

## Acceptance criteria (MVP)

1. Selecting a player with trajectory samples shows Behavior Summary under the map.
2. Metrics match the formulas above on fixture data (tests green).
3. Cross-segment and long gaps do not create path length or speed.
4. UI states: loading timeline, no samples, gap warning.
5. No new backend endpoints required for A.
6. `npm test` / `npm run build` in `webui` pass.

## Implementation order

1. `behaviorTypes` + `behaviorMetrics` + tests (TDD).
2. `behaviorFormat` + tests.
3. `BehaviorSummaryPanel` + styles + component tests.
4. Wire `PlayerTimeline` `useMemo(summarizeBehavior)`.
5. Manual smoke on real trajectory ranges.
6. Stop before map overlay / rankings unless a follow-up plan is opened.

## Risks

| Risk | Mitigation |
|------|------------|
| Change-based sampling biases speed | Caps, gap class, document “observed” not continuous GPS |
| World units ≠ meters | Explicit UI units |
| Partial timeline pages (500 cap) | Footnote; load-older improves estimate |
| Thresholds wrong for server scale | Central constants + tests; tune later without API break |
