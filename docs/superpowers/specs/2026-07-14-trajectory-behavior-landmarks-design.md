# Trajectory Behavior × Landmarks Design (Phase A2)

**Date:** 2026-07-14  
**Parent:** `docs/superpowers/specs/2026-07-14-trajectory-behavior-analysis-design.md` (phase A shipped)  
**Priority (ROI):** A2 (this) → C map overlay → B Analytics rankings  

## Goal

Deepen phase-A behavior analysis by **binding trajectories to world landmarks** (fast travel + boss towers already in `MAP_LANDMARKS`). The timeline Behavior Summary gains: dwell-by-landmark, activity anchor, and suspected teleports—so “idle / travel” becomes “where” not only “how fast.”

## Decisions

| Item | Decision |
|------|----------|
| Scope | Extend pure client metrics + BehaviorSummaryPanel only |
| Landmarks | Reuse `MAP_LANDMARKS` + `nearestLandmark` / `DEFAULT_LANDMARK_RADIUS` (25_000) |
| Server / Analytics page | Out of scope (phase B later, using same fields) |
| Map overlay | Out of scope (phase C; consume A2 tags) |
| New POI types | Not in A2 (bases from save later) |

## In scope

1. Pure enrichment after (or inside) `summarizeBehavior`:
   - per-sample nearest landmark hit (optional on edges)
   - dwell aggregates by landmark
   - activity anchor (most dwell / most hits)
   - teleport-suspect events
2. Panel UI: Top dwell list, anchor chip, teleport list
3. Unit tests for enrichment; component tests for new blocks
4. Types extended on `BehaviorSummary` (backward-compatible fields)

## Out of scope

- Go APIs, daily rollups, rankings
- Drawing on Leaflet (C)
- Inferring base camps without save data
- Changing landmark radius via admin UI
- Guaranteeing true game-teleport detection (label as **疑似**)

## Architecture

```
trajectorySamples
  → summarizeBehavior(...)           # existing
  → enrichBehaviorWithLandmarks(...) # new pure, uses nearestLandmark
  → BehaviorSummaryPanel
```

Preferred split (testability):

| Module | Role |
|--------|------|
| `behavior/behaviorLandmarks.ts` | Enrichment: dwell, anchor, teleports |
| `behavior/behaviorLandmarks.test.ts` | Fixtures with tiny landmark tables |
| `behaviorTypes.ts` | New types + fields on summary |
| `behaviorFormat.ts` | Labels for kind / teleport line |
| `BehaviorSummaryPanel.tsx` | Render new sections |
| `PlayerTimeline.tsx` | Call enrich after summarize (or single wrapper) |

Keep `summarizeBehavior` free of landmark dependency so motion metrics stay isolated; compose in a thin `analyzeTrajectoryBehavior(samples, options)` if cleaner.

## Constants

| Constant | Default | Notes |
|----------|---------|--------|
| Landmark match radius | `DEFAULT_LANDMARK_RADIUS` (25_000) | Same as list/map labels |
| Dwell: edge classes | `stationary` only | Optionally include slow `local` if both ends same landmark—**MVP: stationary only** |
| Teleport: min jump dist | `50_000` | Large world hop |
| Teleport: max duration | `T_GAP_MS` (5 min) or gap edges | Instant-ish hop |
| Teleport: require FT ends | At least one end near `fast_travel` (prefer both) | Reduces false positives |
| Top dwell N | 5 | Panel list |
| Top teleports N | 5 | Panel list |

## Algorithms

### 1. Point → landmark

For each sorted sample used in the summary:

```
hit = nearestLandmark({x,y}, landmarks, radius)
```

Store parallel array `hits: (MapLandmark | undefined)[]` aligned with sorted indices used in metrics (document index space: same as `edges.fromIndex/toIndex` if edges refer to sorted samples).

### 2. Dwell by landmark

For each non-gap edge with `class === 'stationary'`:

- If both endpoints hit the **same** `landmark.id`, add `min(dtMs, T_active_cap)` to that landmark’s `dwellMs`, increment `visitEdges`.
- If only one end hits, **do not** count dwell (avoid path-through noise).

Also count `sampleHits` per landmark (how many samples bind to it) for anchor fallback.

Output:

```ts
type LandmarkDwell = {
  landmarkId: string;
  nameZh: string;
  kind: 'fast_travel' | 'boss_tower';
  dwellMs: number;
  sampleHits: number;
};
```

Sort by `dwellMs` desc, then `sampleHits`, then id; take top 5.

### 3. Activity anchor

1. Prefer landmark with max `dwellMs` if `dwellMs > 0`.
2. Else max `sampleHits` if any hits.
3. Else `undefined` (open field).

### 4. Suspected teleports

Scan consecutive sorted pairs `(a,b)`:

**Case A — gap edge already classified** (`segment` change or `dt > T_gap`):  
If `dist(a,b) >= TELEPORT_MIN_DIST` OR endpoints near different landmarks of kind fast_travel → record suspect with reason `gap_hop`.

**Case B — same segment, short time, huge dist:**  
`dtMs <= T_gap` and `dist >= TELEPORT_MIN_DIST` and (both ends near any FT, or ends near two different FTs) → reason `long_jump`.

Record:

```ts
type TeleportSuspect = {
  fromLandmarkId?: string;
  fromNameZh?: string;
  toLandmarkId?: string;
  toNameZh?: string;
  dist: number;
  dtMs: number;
  reason: 'gap_hop' | 'long_jump';
  at: string; // b.observed_at
};
```

Cap list at 5 (longest dist first).

### 5. Summary extension

```ts
// added to BehaviorSummary
landmarkDwells: LandmarkDwell[];
activityAnchor?: LandmarkDwell;
teleportSuspects: TeleportSuspect[];
landmarkHitRate: number; // samples with any hit / sampleCount
```

## UI (BehaviorSummaryPanel)

Below existing metrics grid:

1. **活动锚点** — chip with name + kind label（传送点 / 首领塔）+ dwell if any  
2. **驻留 Top** — ordered list: name, `formatDuration(dwellMs)`, kind  
3. **疑似传送** — list: `from → to` or `野外 → name`, duration, reason badge「跨段/大跳」  
4. Empty sublines when lists empty:「未匹配到传送点/塔附近的驻留」

Keep gap warning and motion mix as today.

## Testing

### Unit `behaviorLandmarks`

- Stationary edges on same FT accumulate dwell  
- Endpoints different landmarks → no dwell  
- Anchor from dwell vs sampleHits fallback  
- gap_hop and long_jump detection with fixture landmarks  
- Outside radius → no hit, empty dwells  
- Top-N ordering  

### Component

- Renders dwell names when fixture summary provided  
- Hides teleport block when empty (or shows empty copy)  

## Acceptance

1. Player with samples near known FT shows dwell and/or anchor in panel.  
2. Synthetic teleport hop appears under 疑似传送.  
3. Motion metrics unchanged when landmarks omitted/empty table.  
4. Tests + build green; no backend changes.  

## Follow-ons

- **C:** Color edges; pin dwell landmarks; draw teleport arcs.  
- **B:** Daily `landmarkDwells` / teleport counts for rankings.  

## Risks

| Risk | Mitigation |
|------|------------|
| FT names still generic | Existing region prefix on labels |
| False teleports from lag | Require large dist + FT proximity; label 疑似 |
| Radius too large merges FTs | Prefer nearest only; dual-end same id for dwell |
