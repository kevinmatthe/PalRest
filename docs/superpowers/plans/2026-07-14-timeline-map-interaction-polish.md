# Timeline Map Interaction Polish Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make timeline map playback legible: finer clusters, past/future trajectory with direction and motion, correct focus above clusters, and reduced-motion-safe styling.

**Architecture:** Pure helpers in `mapTrajectory.ts` / `mapView.ts` for window split and bearing; Leaflet wiring only in `TimelineMap.tsx` (cluster options, dual polylines, dual-ring focus, post-rebuild cluster exclusion); motion via CSS on path classNames.

**Tech Stack:** React 19, TypeScript, Leaflet 1.9, leaflet.markercluster, Vitest.

**Spec:** `docs/superpowers/specs/2026-07-14-timeline-map-interaction-polish-design.md`

---

## File Structure

| File | Role |
|------|------|
| Modify `webui/src/map/mapTrajectory.ts` | `DEFAULT_TRAJECTORY_MAX_POINTS=16`; style constants; `splitTrajectoryPastFuture` |
| Modify `webui/src/map/mapTrajectory.test.ts` | Defaults, split, cap expectations |
| Modify `webui/src/map/mapView.ts` | `bearingDeg` helper |
| Modify `webui/src/map/mapView.test.ts` | Bearing fixtures |
| Create `webui/src/map/mapClusterExclusion.ts` | Pure-ish `syncFocusClusterExclusion` for testability |
| Create `webui/src/map/mapClusterExclusion.test.ts` | Rebuild / same-key exclusion cases |
| Modify `webui/src/components/TimelineMap.tsx` | Cluster options; dual lines; focus rings/arrow; exclusion wiring |
| Modify `webui/src/styles.css` | Dash flow, pulse, reduced-motion |

---

### Task 1: Bump maxPoints + trajectory style constants

**Files:**
- Modify: `webui/src/map/mapTrajectory.ts`
- Modify: `webui/src/map/mapTrajectory.test.ts`

- [ ] **Step 1: Update failing default tests**

In `mapTrajectory.test.ts`, change the defaults assertion and the default-cap expectation for N=16:

```ts
// defaults describe
expect(DEFAULT_TRAJECTORY_MAX_POINTS).toBe(16);

// in 'uses design defaults of 10min window and 12 max points' → rename and update:
it('uses design defaults of 10min window and 16 max points', () => {
  const many = Array.from({ length: 31 }, (_, i) =>
    pt({
      observed_at: new Date(Date.parse('2026-07-14T10:00:00.000Z') + i * 60_000).toISOString(),
      segment_id: 's1',
      x: i,
      y: 0,
    }),
  );
  const cursor = many[15]!.observed_at;
  const got = hybridTrajectoryWindow(many, cursor);
  // ±10min → indices 5..25 (21 pts), cap 16 past-preferring around 15
  // ceil(15/2)=8 past + anchor + floor(15/2)=7 future → indices 7..22
  expect(got).toHaveLength(DEFAULT_TRAJECTORY_MAX_POINTS);
  expect(got.map((p) => p.x)).toEqual([7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22]);
});
```

Also add style constant smoke tests at the end of the file:

```ts
describe('trajectory style constants', () => {
  it('past is thicker and more opaque than future', () => {
    expect(TRAJ_PAST_WEIGHT).toBeGreaterThan(TRAJ_FUTURE_WEIGHT);
    expect(TRAJ_PAST_OPACITY).toBeGreaterThan(TRAJ_FUTURE_OPACITY);
    expect(TRAJ_DASH_ARRAY).toBe('10 14');
    expect(TRAJ_PAST_COLOR).toMatch(/^#/);
    expect(TRAJ_TIP_COLOR).toMatch(/^#/);
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd webui && npm test -- src/map/mapTrajectory.test.ts`

Expected: FAIL on `DEFAULT_TRAJECTORY_MAX_POINTS` still 12 and/or missing `TRAJ_*` exports.

- [ ] **Step 3: Implement constants**

In `mapTrajectory.ts`, change:

```ts
export const DEFAULT_TRAJECTORY_MAX_POINTS = 16;
```

Add after `pingColor` (or near other exports):

```ts
export const TRAJ_PAST_WEIGHT = 4.5;
export const TRAJ_FUTURE_WEIGHT = 2.5;
export const TRAJ_PAST_COLOR = '#0f8fa3';
export const TRAJ_FUTURE_COLOR = '#0f7285';
export const TRAJ_FUTURE_OPACITY = 0.35;
export const TRAJ_PAST_OPACITY = 0.95;
export const TRAJ_TIP_COLOR = '#ca8519';
export const TRAJ_DASH_ARRAY = '10 14';
export const TRAJ_DASH_ANIM_MS = 900;
export const FOCUS_PULSE_RADIUS = 14;
export const FOCUS_PULSE_COLOR = '#ca8519';
export const FOCUS_PULSE_WEIGHT = 2;
export const ARROW_SIZE_PX = 12;
export const ARROW_COLOR = '#ca8519';
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd webui && npm test -- src/map/mapTrajectory.test.ts`

Expected: PASS (except if split tests not yet added — only this task's tests).

- [ ] **Step 5: Commit**

```bash
git add webui/src/map/mapTrajectory.ts webui/src/map/mapTrajectory.test.ts
git commit -m "feat(webui): raise trajectory max points and export line style constants"
```

---

### Task 2: `splitTrajectoryPastFuture`

**Files:**
- Modify: `webui/src/map/mapTrajectory.ts`
- Modify: `webui/src/map/mapTrajectory.test.ts`

- [ ] **Step 1: Write failing tests**

Append to `mapTrajectory.test.ts`:

```ts
import { splitTrajectoryPastFuture } from './mapTrajectory';

describe('splitTrajectoryPastFuture', () => {
  const windowPts = [
    pt({ observed_at: '2026-07-14T10:00:00.000Z', segment_id: 's1', x: 0, y: 0 }),
    pt({ observed_at: '2026-07-14T10:05:00.000Z', segment_id: 's1', x: 1, y: 0 }),
    pt({ observed_at: '2026-07-14T10:10:00.000Z', segment_id: 's1', x: 2, y: 0 }),
    pt({ observed_at: '2026-07-14T10:15:00.000Z', segment_id: 's1', x: 3, y: 0 }),
  ];

  it('returns empty split for empty window', () => {
    const s = splitTrajectoryPastFuture([], '2026-07-14T10:10:00.000Z');
    expect(s.past).toEqual([]);
    expect(s.future).toEqual([]);
    expect(s.anchor).toBeUndefined();
    expect(s.anchorIndex).toBe(-1);
  });

  it('shares single point on both past and future', () => {
    const s = splitTrajectoryPastFuture([windowPts[0]!], windowPts[0]!.observed_at);
    expect(s.past).toHaveLength(1);
    expect(s.future).toHaveLength(1);
    expect(s.past[0]).toBe(s.future[0]);
    expect(s.anchorIndex).toBe(0);
  });

  it('splits at anchor: past ends with anchor, future starts with anchor', () => {
    const s = splitTrajectoryPastFuture(windowPts, '2026-07-14T10:10:00.000Z');
    expect(s.anchorIndex).toBe(2);
    expect(s.past.map((p) => p.x)).toEqual([0, 1, 2]);
    expect(s.future.map((p) => p.x)).toEqual([2, 3]);
  });

  it('uses latest sample at or before activeAt as anchor', () => {
    const s = splitTrajectoryPastFuture(windowPts, '2026-07-14T10:12:00.000Z');
    expect(s.anchorIndex).toBe(2);
    expect(s.anchor?.x).toBe(2);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd webui && npm test -- src/map/mapTrajectory.test.ts`

Expected: FAIL — `splitTrajectoryPastFuture` is not exported / not a function.

- [ ] **Step 3: Implement split helper**

In `mapTrajectory.ts`:

```ts
export type TrajectorySplit<T> = {
  past: T[];
  future: T[];
  anchor: T | undefined;
  anchorIndex: number;
};

export function splitTrajectoryPastFuture<T extends TrajectoryPointLike>(
  windowSamples: T[],
  activeAt: string | undefined,
): TrajectorySplit<T> {
  if (!windowSamples.length) {
    return { past: [], future: [], anchor: undefined, anchorIndex: -1 };
  }
  const anchor = anchorSample(windowSamples, activeAt);
  let anchorIndex = anchor
    ? windowSamples.findIndex((s) => s === anchor || s.observed_at === anchor.observed_at)
    : -1;
  if (anchorIndex < 0) {
    const activeMS = activeAt ? Date.parse(activeAt) : Number.NaN;
    if (Number.isFinite(activeMS)) {
      for (let i = windowSamples.length - 1; i >= 0; i -= 1) {
        const t = Date.parse(windowSamples[i]!.observed_at);
        if (Number.isFinite(t) && t <= activeMS) {
          anchorIndex = i;
          break;
        }
      }
    }
    if (anchorIndex < 0) anchorIndex = 0;
  }
  const resolved = windowSamples[anchorIndex]!;
  return {
    past: windowSamples.slice(0, anchorIndex + 1),
    future: windowSamples.slice(anchorIndex),
    anchor: resolved,
    anchorIndex,
  };
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd webui && npm test -- src/map/mapTrajectory.test.ts`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add webui/src/map/mapTrajectory.ts webui/src/map/mapTrajectory.test.ts
git commit -m "feat(webui): split hybrid trajectory window into past and future"
```

---

### Task 3: `bearingDeg`

**Files:**
- Modify: `webui/src/map/mapView.ts`
- Modify: `webui/src/map/mapView.test.ts`

- [ ] **Step 1: Write failing tests**

Append to `mapView.test.ts`:

```ts
import { bearingDeg, shouldPanToFocus } from './mapView';

describe('bearingDeg', () => {
  it('points east when lng increases at fixed lat (CRS.Simple tuple)', () => {
    // L.latLng(lat, lng); projectWorldSample returns [mapX, mapY] used as LatLngExpression
    // Documented formula: atan2(Δlng, Δlat) * 180/π, CSS degrees from up.
    const deg = bearingDeg([0, 0], [0, 1]);
    expect(deg).toBeCloseTo(90, 5);
  });

  it('points north when lat increases at fixed lng', () => {
    const deg = bearingDeg([0, 0], [1, 0]);
    expect(deg).toBeCloseTo(0, 5);
  });

  it('points south when lat decreases', () => {
    const deg = bearingDeg([1, 0], [0, 0]);
    expect(deg).toBeCloseTo(180, 5);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd webui && npm test -- src/map/mapView.test.ts`

Expected: FAIL — `bearingDeg` not exported.

- [ ] **Step 3: Implement**

In `mapView.ts`:

```ts
/**
 * CSS rotation degrees clockwise from up for CRS.Simple LatLngExpression.
 * Uses atan2(Δlng, Δlat) so north (+lat) → 0°, east (+lng) → 90°.
 */
export function bearingDeg(from: L.LatLngExpression, to: L.LatLngExpression): number {
  const a = L.latLng(from as L.LatLngTuple);
  const b = L.latLng(to as L.LatLngTuple);
  return (Math.atan2(b.lng - a.lng, b.lat - a.lat) * 180) / Math.PI;
}
```

- [ ] **Step 4: Run tests**

Run: `cd webui && npm test -- src/map/mapView.test.ts`

Expected: PASS. If east is not ~90°, adjust formula and update tests + comment to match (spec allows one verification pass).

- [ ] **Step 5: Commit**

```bash
git add webui/src/map/mapView.ts webui/src/map/mapView.test.ts
git commit -m "feat(webui): add bearingDeg for trajectory direction arrows"
```

---

### Task 4: Cluster focus exclusion helper

**Files:**
- Create: `webui/src/map/mapClusterExclusion.ts`
- Create: `webui/src/map/mapClusterExclusion.test.ts`

- [ ] **Step 1: Write failing tests**

```ts
// webui/src/map/mapClusterExclusion.test.ts
import { describe, expect, it, vi } from 'vitest';
import { syncFocusClusterExclusion, type ClusterLike, type ExclusionState } from './mapClusterExclusion';

function fakeCluster(initialKeys: string[]): {
  group: ClusterLike;
  layers: Map<string, object>;
} {
  const layers = new Map<string, object>();
  for (const k of initialKeys) layers.set(k, { key: k });
  const group: ClusterLike = {
    hasLayer: (layer) => [...layers.values()].includes(layer),
    addLayer: (layer) => {
      const key = (layer as { key?: string }).key;
      if (key) layers.set(key, layer);
    },
    removeLayer: (layer) => {
      for (const [k, v] of layers) {
        if (v === layer) layers.delete(k);
      }
    },
  };
  return { group, layers };
}

describe('syncFocusClusterExclusion', () => {
  it('removes active key from cluster and records exclusion', () => {
    const { group, layers } = fakeCluster(['a', 'b']);
    const markers = new Map(
      [...layers.entries()].map(([k, v]) => [k, v as L.Layer]),
    );
    // use object layers from fake
    const markersByKey = new Map<string, object>();
    for (const [k, v] of layers) markersByKey.set(k, v);
    const state: ExclusionState = { excludedKey: '' };
    syncFocusClusterExclusion({
      clusterGroup: group,
      markersByKey: markersByKey as Map<string, { /* leaflet layer */ }>,
      activeSampleKey: 'b',
      state,
    });
    expect(state.excludedKey).toBe('b');
    expect(layers.has('b')).toBe(false);
    expect(layers.has('a')).toBe(true);
  });

  it('re-adds previous excluded when key changes', () => {
    const { group, layers } = fakeCluster(['a']);
    const markersByKey = new Map<string, object>();
    const markerA = { key: 'a' };
    const markerB = { key: 'b' };
    markersByKey.set('a', markerA);
    markersByKey.set('b', markerB);
    layers.set('a', markerA);
    // b not in cluster yet (simulating previous exclude of b)
    const state: ExclusionState = { excludedKey: 'b' };
    syncFocusClusterExclusion({
      clusterGroup: group,
      markersByKey: markersByKey as never,
      activeSampleKey: 'a',
      state,
    });
    expect(state.excludedKey).toBe('a');
    expect(layers.has('b')).toBe(true); // previous re-added
    expect(layers.has('a')).toBe(false);
  });

  it('re-excludes same key after rebuild (all markers back in cluster)', () => {
    const { group, layers } = fakeCluster(['focus', 'other']);
    const markersByKey = new Map(
      [...layers.entries()].map(([k, v]) => [k, v]),
    );
    const state: ExclusionState = { excludedKey: '' }; // rebuild cleared ref
    syncFocusClusterExclusion({
      clusterGroup: group,
      markersByKey: markersByKey as never,
      activeSampleKey: 'focus',
      state,
    });
    expect(layers.has('focus')).toBe(false);
    expect(state.excludedKey).toBe('focus');
  });
});
```

Fix types in the real file to avoid `L.Layer` import confusion — use a minimal interface.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd webui && npm test -- src/map/mapClusterExclusion.test.ts`

Expected: FAIL — module missing.

- [ ] **Step 3: Implement helper**

```ts
// webui/src/map/mapClusterExclusion.ts
export type ClusterLike = {
  hasLayer: (layer: unknown) => boolean;
  addLayer: (layer: unknown) => void;
  removeLayer: (layer: unknown) => void;
};

export type ExclusionState = {
  excludedKey: string;
};

export function syncFocusClusterExclusion(args: {
  clusterGroup: ClusterLike;
  markersByKey: Map<string, unknown>;
  activeSampleKey: string;
  state: ExclusionState;
}): void {
  const { clusterGroup, markersByKey, activeSampleKey, state } = args;
  const previousKey = state.excludedKey;
  if (previousKey && previousKey !== activeSampleKey) {
    const previous = markersByKey.get(previousKey);
    if (previous && !clusterGroup.hasLayer(previous)) {
      clusterGroup.addLayer(previous);
    }
  }
  if (activeSampleKey) {
    const active = markersByKey.get(activeSampleKey);
    if (active && clusterGroup.hasLayer(active)) {
      clusterGroup.removeLayer(active);
    }
  }
  state.excludedKey = activeSampleKey;
}
```

Adjust the test file to match these exact types (no Leaflet types required).

- [ ] **Step 4: Run tests**

Run: `cd webui && npm test -- src/map/mapClusterExclusion.test.ts`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add webui/src/map/mapClusterExclusion.ts webui/src/map/mapClusterExclusion.test.ts
git commit -m "feat(webui): extract focus cluster exclusion helper"
```

---

### Task 5: Cluster options + exclusion wiring + trajectory layers in TimelineMap

**Files:**
- Modify: `webui/src/components/TimelineMap.tsx`
- Modify: `webui/src/styles.css`

- [ ] **Step 1: Add cluster constants and options**

At top of `TimelineMap.tsx` (module scope):

```ts
const MAX_CLUSTER_RADIUS = 48;
const DISABLE_CLUSTERING_AT_ZOOM = 4;
```

In `L.markerClusterGroup({...})` add:

```ts
maxClusterRadius: MAX_CLUSTER_RADIUS,
disableClusteringAtZoom: DISABLE_CLUSTERING_AT_ZOOM,
```

Keep `showCoverageOnHover: false` and existing `iconCreateFunction`.

- [ ] **Step 2: Wire exclusion helper + activeSampleKeyRef**

Imports:

```ts
import {
  hybridTrajectoryWindow,
  pingColor,
  splitTrajectoryPastFuture,
  TRAJ_PAST_WEIGHT,
  TRAJ_FUTURE_WEIGHT,
  TRAJ_PAST_COLOR,
  TRAJ_FUTURE_COLOR,
  TRAJ_FUTURE_OPACITY,
  TRAJ_PAST_OPACITY,
  TRAJ_TIP_COLOR,
  TRAJ_DASH_ARRAY,
  FOCUS_PULSE_RADIUS,
  FOCUS_PULSE_COLOR,
  FOCUS_PULSE_WEIGHT,
  ARROW_SIZE_PX,
  ARROW_COLOR,
} from '../map/mapTrajectory';
import { bearingDeg, shouldPanToFocus } from '../map/mapView';
import { syncFocusClusterExclusion, type ExclusionState } from '../map/mapClusterExclusion';
```

Replace `excludedClusterKeyRef` string with:

```ts
const exclusionStateRef = useRef<ExclusionState>({ excludedKey: '' });
const activeSampleKeyRef = useRef('');
activeSampleKeyRef.current = activeSampleKey;
```

In points rebuild effect, after creating all markers:

```ts
// do NOT reset exclusion by leaving key '' without re-sync
exclusionStateRef.current.excludedKey = '';
syncFocusClusterExclusion({
  clusterGroup,
  markersByKey: markersByKeyRef.current,
  activeSampleKey: activeSampleKeyRef.current,
  state: exclusionStateRef.current,
});
```

Note: `markersByKey` values are `L.CircleMarker`; the helper accepts `unknown`.

- [ ] **Step 3: Replace single polyline + focus with layered rendering**

In the cursor effect (deps stay `[activeItem?.at, activeSampleKey, colorMode, samples]`):

```ts
syncFocusClusterExclusion({
  clusterGroup,
  markersByKey: markersByKeyRef.current,
  activeSampleKey,
  state: exclusionStateRef.current,
});

lineLayer.clearLayers();
const activeAt = activeItem?.at;
const lineSamples = hybridTrajectoryWindow(samples, activeAt);
const split = splitTrajectoryPastFuture(lineSamples, activeAt);
const projectAll = (list: typeof lineSamples) => list.map((s) => projectWorldSample(s));

if (split.future.length >= 2) {
  L.polyline(projectAll(split.future), {
    color: TRAJ_FUTURE_COLOR,
    opacity: TRAJ_FUTURE_OPACITY,
    weight: TRAJ_FUTURE_WEIGHT,
    lineCap: 'round',
    lineJoin: 'round',
    className: 'timeline-traj-future',
  }).addTo(lineLayer);
}
if (split.past.length >= 2) {
  L.polyline(projectAll(split.past), {
    color: TRAJ_PAST_COLOR,
    opacity: TRAJ_PAST_OPACITY,
    weight: TRAJ_PAST_WEIGHT,
    lineCap: 'round',
    lineJoin: 'round',
    dashArray: TRAJ_DASH_ARRAY,
    className: 'timeline-traj-past',
  }).addTo(lineLayer);
  const tip = split.past.slice(-2);
  L.polyline(projectAll(tip), {
    color: TRAJ_TIP_COLOR,
    opacity: 1,
    weight: TRAJ_PAST_WEIGHT,
    lineCap: 'round',
    lineJoin: 'round',
    className: 'timeline-traj-tip',
  }).addTo(lineLayer);
}

focusLayer.clearLayers();
const focusSample = latestPointAt(samples, activeAt);
if (focusSample) {
  const activePoint = projectWorldSample(focusSample);
  const ping = colorMode === 'ping' ? pingColor(focusSample.ping) : null;

  L.circleMarker(activePoint, {
    radius: FOCUS_PULSE_RADIUS,
    color: FOCUS_PULSE_COLOR,
    fillOpacity: 0,
    weight: FOCUS_PULSE_WEIGHT,
    className: 'timeline-focus-pulse',
    interactive: false,
  }).addTo(focusLayer);

  L.circleMarker(activePoint, {
    radius: ping ? Math.max(8, ping.radius + 3) : 8,
    color: '#8d5a0f',
    fillColor: ping?.fill ?? '#ca8519',
    fillOpacity: 0.92,
    weight: 3,
    className: 'timeline-focus-core',
  }).addTo(focusLayer);

  if (split.past.length >= 2) {
    const from = projectWorldSample(split.past[split.past.length - 2]!);
    const to = projectWorldSample(split.past[split.past.length - 1]!);
    const deg = bearingDeg(from, to);
    const icon = L.divIcon({
      className: 'timeline-traj-arrow',
      html: `<div class="timeline-traj-arrow-inner" style="transform:rotate(${deg}deg)"></div>`,
      iconSize: [ARROW_SIZE_PX, ARROW_SIZE_PX],
      iconAnchor: [ARROW_SIZE_PX / 2, ARROW_SIZE_PX / 2],
    });
    L.marker(activePoint, { icon, interactive: false, keyboard: false }).addTo(focusLayer);
  }

  if (shouldPanToFocus(map.getBounds(), activePoint)) {
    map.panTo(activePoint, { animate: false });
  }
}
```

Clean up map-destroy effect: reset `exclusionStateRef.current = { excludedKey: '' }` instead of `excludedClusterKeyRef`.

- [ ] **Step 4: Add CSS**

In `styles.css` after `.timeline-marker-cluster--lg` (or nearby timeline map rules):

```css
.timeline-traj-past {
  stroke-dasharray: 10 14;
  animation: timeline-traj-dash 900ms linear infinite;
}
@keyframes timeline-traj-dash {
  to { stroke-dashoffset: -24; }
}
.timeline-focus-pulse {
  animation: timeline-focus-pulse 1.2s ease-out infinite;
}
@keyframes timeline-focus-pulse {
  0% { stroke-opacity: 0.9; }
  70% { stroke-opacity: 0.15; }
  100% { stroke-opacity: 0.9; }
}
.timeline-traj-arrow {
  background: transparent;
  border: none;
}
.timeline-traj-arrow-inner {
  width: 0;
  height: 0;
  margin: 0 auto;
  border-left: 5px solid transparent;
  border-right: 5px solid transparent;
  border-bottom: 10px solid #ca8519;
  transform-origin: 50% 60%;
  filter: drop-shadow(0 0 1px #8d5a0f);
}
@media (prefers-reduced-motion: reduce) {
  .timeline-traj-past { animation: none; }
  .timeline-focus-pulse { animation: none; stroke-opacity: 0.55; }
}
```

If a global reduced-motion rule already zeros all animations, the explicit rules still document intent; keep them for specificity when needed.

- [ ] **Step 5: Run full webui tests + typecheck**

Run:

```bash
cd webui && npm test && npm run build
```

Expected: all tests PASS; `tsc -b` + vite build succeed.

- [ ] **Step 6: Manual smoke (if map available)**

1. Zoom 0–3: clusters form with tighter radius.  
2. Zoom ≥4: all dots unclustered.  
3. Play: past dash flows; future muted; arrow at focus.  
4. Toggle 位置/延迟: focus still excluded from cluster count.  
5. Reduced motion OS setting: no dash/pulse animation.

- [ ] **Step 7: Commit**

```bash
git add webui/src/components/TimelineMap.tsx webui/src/styles.css
git commit -m "feat(webui): polish map clusters, trajectory direction, and focus rings"
```

---

### Task 6: Final verification

**Files:** none (verification only)

- [ ] **Step 1: Full suite**

```bash
cd webui && npm test && npm run build
```

Expected: PASS / build OK.

- [ ] **Step 2: Spec acceptance checklist**

Confirm against design acceptance criteria:

1. Cluster radius 48 + disable at zoom ≥ 4  
2. Past/future/tip/arrow present when window ≥ 2  
3. maxPoints 16 / ±10 min  
4. Focus dual-ring + rebuild exclusion  
5. Layer order tiles → cluster → landmarks → lines → focus  
6. reduced-motion disables dash + pulse only  
7. Tests green  

- [ ] **Step 3: Optional empty commit message note**

If everything already committed in Tasks 1–5, no further commit. Otherwise:

```bash
git status
# ensure no stray uncommitted intentional changes
```

---

## Spec coverage (self-review)

| Spec item | Task |
|-----------|------|
| maxClusterRadius 48, disableClusteringAtZoom 4 | Task 5 |
| past/future split algorithm | Task 2 |
| Dual polylines + tip + dash | Task 5 |
| Direction arrow + bearingDeg | Tasks 3, 5 |
| Dual-ring focus pulse | Task 5 |
| maxPoints 16 | Task 1 |
| Post-rebuild exclusion | Tasks 4, 5 |
| CSS + reduced-motion | Task 5 |
| Unit tests split/defaults/bearing/exclusion | Tasks 1–4 |
| No new copy / API | N/A (unchanged) |

## Placeholder scan

No TBD / “implement later” / “similar to Task N” without code.

## Type consistency

- `TrajectorySplit`, `splitTrajectoryPastFuture`, style constants from `mapTrajectory.ts`
- `bearingDeg` from `mapView.ts`
- `syncFocusClusterExclusion` + `ExclusionState` + `ClusterLike` from `mapClusterExclusion.ts`
- TimelineMap uses `exclusionStateRef` not `excludedClusterKeyRef`
