# Timeline Map Interaction Polish Design

**Date:** 2026-07-14  
**Approach (locked):** B — pure helpers under `webui/src/map/` + layered rendering in `TimelineMap.tsx`  
**Related:** `docs/superpowers/specs/2026-07-14-timeline-map-ux-polish-design.md` (v1 features shipped). This spec is the next polish pass on **cluster readability, trajectory contrast/direction, focus z-order, and playback motion**, not a new product surface.

## Goal

Make map playback feel alive and legible while scrubbing or autoplaying:

1. **Finer clustering** so nearby samples uncluster earlier and individual dots remain readable at mid-zoom.
2. **Directional, high-contrast trajectory** (past vs future split, dash flow, end arrow) so motion direction is obvious without inventing path across segments.
3. **Playback that reads as progress** — slightly longer hybrid window, past-segment emphasis, focus always painted above clusters.
4. **Correct focus exclusion** after every cluster marker rebuild so the active sample never re-enters the cluster group.
5. **Respect `prefers-reduced-motion`** by disabling dash animation and focus pulse while keeping static past/future contrast.

## Non-goals

- Server/guild replay modes, heatmaps, or coverage polygons on cluster hover (`showCoverageOnHover` stays `false`).
- True multi-stop SVG gradient polylines or WebGL path shaders.
- Configurable cluster/window/style constants via YAML or admin UI.
- Changing hybrid segment isolation, ping dual-mode, landmark overlay, transport controls, or API contracts.
- Rebuilding the entire marker set on every cursor step (incremental cursor lifecycle remains).
- New UI copy, presets, or transport buttons beyond existing labels.

## Decisions (locked)

| Item | Decision |
|------|----------|
| Architecture | Approach B: pure helpers + `TimelineMap` layered Leaflet rendering |
| Cluster | `maxClusterRadius=48`, `disableClusteringAtZoom=4`, `showCoverageOnHover=false`, neutral teal chrome (keep existing size tiers sm/md/lg) |
| Line style | Style C: past/future split gradient approximation; flowing dash on past only; direction arrow at focus end of past; dual-ring focus pulse |
| Playback | Keep hybrid window; raise max points to 16; progress = thicker/brighter past + dash + focus above clusters |
| Layer order | tiles → cluster → landmarks → trajectory lines → focus (+ arrow/pulse) |
| Focus bug | After any points rebuild, re-apply exclude of `activeSampleKey` from cluster |
| Reduced motion | Disable dash animation + pulse; keep static past/future colors and weights |

## In scope

1. MarkerCluster option tuning (`maxClusterRadius`, `disableClusteringAtZoom`).
2. Pure trajectory split helpers and style constants in `mapTrajectory.ts`.
3. Dual polylines (past / future) + gold tip + direction marker in `TimelineMap`.
4. CSS classes for dash flow and focus pulse; reduced-motion overrides.
5. Focus dual-ring (outer pulse ring + solid core) always on `focusLayer`.
6. Hybrid window default `maxPoints` raise to 16 (window stays 10 min); asymmetric past bias unchanged.
7. Post-rebuild focus re-exclusion fix.
8. Unit tests for split/window defaults/bearing; optional fake-cluster exclusion test.

## Out of scope

- Cross-segment path invention.
- Cluster icons colored by worst ping.
- Spiderfy UX redesign.
- Canvas/WebGL trajectory renderer.
- Changing play speeds, time-proportional mode math, or pan-follow policy (except visual layers).
- Backend pagination / downsampling changes.

## Architecture

```
webui/src/map/mapTrajectory.ts     # hybrid window + past/future split + style constants
webui/src/map/mapTrajectory.test.ts
webui/src/map/mapView.ts           # pan helper + bearingDeg
webui/src/map/mapView.test.ts     # bearing + existing pan tests
webui/src/components/TimelineMap.tsx
  layers (add order = paint bottom→top):
    tileLayer
    clusterGroup          # non-focus samples
    landmarkLayer
    lineLayer             # future polyline, past polyline, optional tip
    focusLayer            # dual-ring focus + direction arrow marker
webui/src/styles.css              # path dash, pulse, reduced-motion
```

| Concern | Owner | Side effects |
|---------|--------|--------------|
| Window ∩ N ∩ segment | `hybridTrajectoryWindow` | pure |
| Split window at anchor into past / future | `splitTrajectoryPastFuture` (new) | pure |
| Line/marker style tokens | exported constants in `mapTrajectory.ts` | pure data |
| Cluster group options | module constants in `TimelineMap.tsx` | Leaflet once |
| Marker rebuild on `points` / `colorMode` | points effect + **post-rebuild exclude** | Leaflet |
| Cursor-driven line + focus + arrow | cursor effect, extended | Leaflet clear/add on cursor only |
| Motion CSS | `styles.css` + path `className`s | none |

Pure helpers stay free of React/Leaflet so Vitest needs no map DOM. Paint order and exclusion bugs are fixed only in `TimelineMap.tsx`.

### Layer order (normative)

On map init, add layers in this order:

1. `tileLayer`
2. `clusterGroup`
3. `landmarkLayer`
4. `lineLayer`
5. `focusLayer`

Do not put focus circle, pulse ring, or direction arrow into `clusterGroup`. Do not put trajectory polylines into `clusterGroup`.

## Constants

| Constant | Value | Notes |
|----------|-------|--------|
| `MAX_CLUSTER_RADIUS` | `48` | Leaflet `maxClusterRadius` (px). Finer than library default 80. |
| `DISABLE_CLUSTERING_AT_ZOOM` | `4` | Full uncluster from zoom 4 through maxZoom 6. |
| `CLUSTER_SHOW_COVERAGE_ON_HOVER` | `false` | Unchanged. |
| `DEFAULT_TRAJECTORY_WINDOW_MS` | `10 * 60_000` | Keep ±10 min around cursor. |
| `DEFAULT_TRAJECTORY_MAX_POINTS` | **`16`** | Was 12; more responsive path during play. Past bias via existing `ceil((N-1)/2)` before center remains. |
| `TRAJ_PAST_WEIGHT` | `4.5` | Solid past path weight. |
| `TRAJ_FUTURE_WEIGHT` | `2.5` | Muted future path weight. |
| `TRAJ_PAST_COLOR` | `#0f8fa3` | Brighter teal than old single stroke `#0f7285`. |
| `TRAJ_FUTURE_COLOR` | `#0f7285` | Base teal. |
| `TRAJ_FUTURE_OPACITY` | `0.35` | Muted future. |
| `TRAJ_PAST_OPACITY` | `0.95` | Strong past. |
| `TRAJ_TIP_COLOR` | `#ca8519` | Last past edge gold tip toward focus. |
| `TRAJ_DASH_ARRAY` | `'10 14'` | Past path only. |
| `TRAJ_DASH_ANIM_MS` | `900` | CSS dash offset cycle; disabled under reduced motion. |
| `FOCUS_CORE_RADIUS` | `8` (ping mode: `max(8, ping.radius + 3)`) | Existing core behaviour retained. |
| `FOCUS_CORE_STROKE` | `#8d5a0f` | Existing gold brown. |
| `FOCUS_CORE_FILL` | `#ca8519` or ping fill | Unchanged dual-mode rule. |
| `FOCUS_PULSE_RADIUS` | `14` | Outer ring only; no fill. |
| `FOCUS_PULSE_COLOR` | `#ca8519` | Matches core emphasis. |
| `FOCUS_PULSE_WEIGHT` | `2` | Thin outer ring. |
| `ARROW_SIZE_PX` | `12` | Direction divIcon edge length. |
| `ARROW_COLOR` | `#ca8519` | Matches tip/focus. |

**Why maxPoints 16 (not keep 12):** with ±10 min and ~1 sample/min, the window often holds ~21 points then caps; 16 still prefers past (`ceil(15/2)=8` past + anchor + `floor(15/2)=7` future) and gives a longer readable snake during 2×/4× play without dense spaghetti. Window time is unchanged so distant history still does not connect.

**Why zoom 4 uncluster:** map `maxZoom` is 6; unclustering two full zoom levels before max keeps mid-zoom readable while zoom 0–3 still aggregates dense sessions.

**Constant placement:**

- Trajectory style + window defaults → `mapTrajectory.ts`
- `MAX_CLUSTER_RADIUS` / `DISABLE_CLUSTERING_AT_ZOOM` → module-level constants in `TimelineMap.tsx` (cluster is Leaflet wiring)

## Feature design

### 1. Cluster granularity

**File:** `TimelineMap.tsx` map init.

```ts
L.markerClusterGroup({
  maxClusterRadius: MAX_CLUSTER_RADIUS, // 48
  disableClusteringAtZoom: DISABLE_CLUSTERING_AT_ZOOM, // 4
  showCoverageOnHover: false,
  iconCreateFunction(cluster) { /* existing teal sm/md/lg divIcon */ },
});
```

Keep current `.timeline-marker-cluster` / `--sm|md|lg` CSS (neutral teal `#0f7285`, cream border). No ping-derived cluster color.

### 2. Trajectory past/future split

**Input:** ordered window from `hybridTrajectoryWindow(samples, activeAt)` (already same `segment_id`, time-filtered, N-capped, chronological).

**Anchor index:** prefer the sample equal to `anchorSample(samples, activeAt)` within the window; if missing, last window index with `observed_at <= activeAt`, else `0`.

**Split (normative):**

```ts
export type TrajectorySplit<T> = {
  past: T[];       // indices [0 .. anchorIdx] inclusive
  future: T[];     // indices [anchorIdx .. end] inclusive
  anchor: T | undefined;
  anchorIndex: number; // -1 if empty
};

export function splitTrajectoryPastFuture<T extends TrajectoryPointLike>(
  windowSamples: T[],
  activeAt: string | undefined,
): TrajectorySplit<T>
```

**Rendering rules:**

| Piece | When drawn | Geometry | Style |
|-------|------------|----------|--------|
| Future polyline | `future.length >= 2` | all `future` projected | `TRAJ_FUTURE_COLOR`, opacity `0.35`, weight `2.5`, **no** dash |
| Past polyline | `past.length >= 2` | all `past` projected | `TRAJ_PAST_COLOR`, opacity `0.95`, weight `4.5`, `dashArray: '10 14'`, class `timeline-traj-past` |
| Gold tip | `past.length >= 2` | last two points of `past` only | `TRAJ_TIP_COLOR`, weight `4.5`, opacity `1`, no dash |
| None | window length `< 2` | — | no polylines (dots only) |

Shared anchor vertex means past and future meet at the focus sample without a gap. Draw **future first, then past, then tip** inside `lineLayer` so past/tip paint over the join.

**Progress during playback:** as the cursor advances, `hybridTrajectoryWindow` and the split recompute each step; past grows along the segment within the sliding window — that is the progress cue (no separate progress layer).

### 3. Direction markers

- Small **arrow `L.marker` with `divIcon`** on `focusLayer` at the focus/anchor position (end of past).
- Bearing: if `past.length >= 2`, angle from second-to-last past point → last past point.
- Helper in `mapView.ts`:

```ts
/** Degrees CSS clockwise from up, for CRS.Simple LatLng tuples used as L.LatLngExpression. */
export function bearingDeg(from: L.LatLngExpression, to: L.LatLngExpression): number
```

Normalize via `L.latLng`, then `Math.atan2(to.lng - from.lng, to.lat - from.lat) * 180 / Math.PI`. Lock with a unit test on synthetic points; if the arrow is wrong by 90° against a known eastward move on the map, swap components and document the final formula in the helper comment.

- Icon: compact chevron/triangle, class `timeline-traj-arrow`, size `ARROW_SIZE_PX`, color `ARROW_COLOR`.
- `interactive: false`, `keyboard: false`, no tooltip.
- If `past.length < 2`, **omit** arrow.

### 4. CSS animation (flowing dash + pulse)

**Dash (past path only):** Leaflet path `className: 'timeline-traj-past'`.

```css
.timeline-traj-past {
  stroke-dasharray: 10 14;
  animation: timeline-traj-dash 900ms linear infinite;
}
@keyframes timeline-traj-dash {
  to { stroke-dashoffset: -24; } /* -(10+14) */
}
```

Future/tip: no dash animation. Set Leaflet `dashArray` for past so static fallback exists; CSS animation drives `stroke-dashoffset`.

**Focus pulse:** outer `circleMarker` with `className: 'timeline-focus-pulse'`, gold stroke, `fillOpacity: 0`, radius 14, weight 2. Core keeps existing solid marker with class `timeline-focus-core`.

Opacity pulse only — avoid scale transforms on Leaflet paths (they fight projection).

**`prefers-reduced-motion: reduce`:**

```css
@media (prefers-reduced-motion: reduce) {
  .timeline-traj-past { animation: none; }
  .timeline-focus-pulse { animation: none; stroke-opacity: 0.55; }
}
```

Under reduced motion: past remains thicker/brighter with **static** dash array; future still muted; outer ring visible at fixed opacity. Do **not** disable play stepping or change `panTo({ animate: false })`.

### 5. Focus dual-ring + cluster exclusion bugfix

**Dual-ring (always on `focusLayer` when `focusSample` exists):**

1. Pulse ring: radius 14, stroke `#ca8519`, fill transparent, weight 2, class `timeline-focus-pulse`.
2. Core: existing gold/ping fill marker (radius rules unchanged).
3. Direction arrow marker (if past ≥ 2).

**Add order in `focusLayer` each cursor update:** pulse → core → arrow (arrow on top).

**Exclude bug (root cause):**

1. Points effect: `clearLayers`, recreate all markers into cluster, **resets** `excludedClusterKeyRef` to `''`.
2. Cursor effect: excludes `activeSampleKey`.

When **only** `points`/`colorMode` change while `activeSampleKey` is unchanged, the cursor effect **does not re-run**, so focus stays in the cluster.

**Fix:**

- Extract `syncFocusClusterExclusion(activeSampleKey: string)` that:
  - re-adds previous excluded marker if key changed and marker exists;
  - removes current active marker from cluster if present;
  - sets `excludedClusterKeyRef`.
- Call it at end of **points rebuild effect** with current `activeSampleKey` (via `activeSampleKeyRef.current` updated every render).
- Also call it from the cursor effect when the key moves.

After rebuild, focus sample is always removed from cluster even when the cursor effect is skipped. Focus **visual** remains only on `focusLayer`.

### 6. Hybrid window tuning for playback

| Param | Before | After |
|-------|--------|-------|
| `DEFAULT_TRAJECTORY_WINDOW_MS` | 10 min | **10 min** (unchanged) |
| `DEFAULT_TRAJECTORY_MAX_POINTS` | 12 | **16** |

Past preference math unchanged. No UI control for T/N.

### 7. Cursor / playback effect structure

Replace single polyline block with:

1. `syncFocusClusterExclusion(...)`
2. `lineLayer.clearLayers()`
3. `window = hybridTrajectoryWindow(samples, activeAt)`
4. `split = splitTrajectoryPastFuture(window, activeAt)`
5. project past/future arrays
6. add future polyline → past polyline → optional tip
7. `focusLayer.clearLayers()`; add pulse, core, arrow
8. `shouldPanToFocus` → `panTo` unchanged

`colorMode` still only affects marker fills and focus core fill, not trajectory stroke colors (trajectory encodes **time direction**, ping mode encodes **latency** on dots).

## UI / copy

**No new user-visible copy.** Existing labels stay (位置 / 延迟 / 地标 / 播放 controls).

## Testing

### Unit (`mapTrajectory.test.ts`)

1. Defaults: `DEFAULT_TRAJECTORY_WINDOW_MS === 600_000`, `DEFAULT_TRAJECTORY_MAX_POINTS === 16`.
2. Cap behaviour with N=16: centered slice past-biased (update existing default-cap expectation).
3. `splitTrajectoryPastFuture`:
   - empty → empty split;
   - single point → past `[p]`, future `[p]`, shared anchor;
   - multi: past includes anchor as last, future includes anchor as first;
   - activeAt between samples uses `anchorSample` semantics (latest ≤ activeAt).
4. Style constants: past weight > future weight; opacities in documented ranges.

### Unit (`mapView.test.ts`)

5. `bearingDeg` documented fixture points; stable numeric expectation.

### Exclusion helper

6. Optional: unit-test `syncFocusClusterExclusion` with a tiny fake group (`hasLayer`/`addLayer`/`removeLayer`) for “rebuild then same key still excluded”.

### Manual smoke

1. Zoom 0–3: clusters visible, radius tighter than old default 80.
2. Zoom ≥4: all sample dots unclustered.
3. Play 1×/4×: past dash flows toward focus; future muted; arrow points along last past edge.
4. Toggle 位置/延迟: rebuild markers; focus still gold-emphasized and **not** inside a cluster count.
5. OS reduced-motion: no dash/pulse animation; past still thicker.
6. Landmark toggle: landmarks under lines, under focus.

### Regression checklist

- Click cluster child still seeks cursor.
- Empty samples: no throw, empty map message unchanged.
- Segment boundary: line never crosses `segment_id`.
- `prefers-reduced-motion` does not stop autoplay interval.

## Acceptance criteria

1. Cluster uses radius 48 and disables clustering at zoom ≥ 4; chrome remains neutral teal with counts.
2. Trajectory draws distinct past (thick/bright, dashed flow) and future (thin/muted); gold tip on last past edge; arrow at focus when direction exists.
3. During play/scrub, window updates with max 16 points / ±10 min; past segment length tracks progress visually.
4. Focus dual-ring (+ arrow) always paints above clusters and lines; after `points`/`colorMode` rebuild, active sample is re-excluded from the cluster group.
5. Layer order matches tiles → cluster → landmarks → lines → focus.
6. `prefers-reduced-motion: reduce` disables dash and pulse animations only.
7. `npm test` in `webui` passes; no API or copy changes required.

## Implementation order

1. Constants + `splitTrajectoryPastFuture` (+ tests); bump `DEFAULT_TRAJECTORY_MAX_POINTS` to 16 and fix default tests.
2. `bearingDeg` + arrow helper tests.
3. Cluster options in map init.
4. Cursor effect: dual polylines, tip, dual-ring, arrow; CSS classes.
5. Points-effect post-rebuild exclusion via ref + shared sync helper.
6. `styles.css` dash/pulse + reduced-motion.
7. Manual map smoke; green unit suite.

## Relationship to prior design

Extends shipped UX polish (hybrid window, MarkerCluster, dual ping mode, transport) by tightening cluster scale, making trajectory **direction-aware and playback-readable**, and closing the **focus re-cluster after rebuild** hole. Full Replay subsystem remains future work.
