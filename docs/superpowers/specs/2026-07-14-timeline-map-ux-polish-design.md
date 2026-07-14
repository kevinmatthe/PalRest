# Timeline Map UX Polish Design

## Goal

Ship the seven map/timeline improvements that were specified before the tile-fallback pivot and never landed. Scope stays inside the existing player timeline UI; this is not the full Phase 2 Replay subsystem (server/guild scopes, metric panels, or server-side downsampling).

## Decisions (locked in brainstorming)

| Item | Decision |
|------|----------|
| Trajectory polyline | Hybrid: same `segment_id` only; time window ∩ max N samples around the cursor; weight 2; other samples as unconnected dots |
| Low-zoom aggregation | `leaflet.markercluster` |
| Ping coloring | Dual mode: default uniform teal; toggle to traffic-light bins |
| Place names | Nearest named landmark within radius; else existing coordinate fallback copy |
| Structure | Focused modules under `webui/src` rather than a new Replay package or a single mega-file |

## In scope

1. **Map autoplay** — play/pause on the shared timeline cursor; optional speed; stop at end.
2. **Friendly time range** — presets for last 1h / 24h / 7d; keep `datetime-local` and the 31-day cap.
3. **Thinner, windowed trajectory** — hybrid polyline; no cross-segment inventing.
4. **Zoom clustering** — MarkerCluster for non-focus samples.
5. **Ping-colored points** — dual mode + legend when in ping mode.
6. **Dockerfile Git LFS** — ensure map tile LFS objects are materialised at image build when the build context allows it.
7. **Landmark Chinese labels** — coordinate → nearest zh name via a static table.

## Out of scope

- Server-wide / guild replay modes
- Server-side resolution-aware downsampling APIs
- Heatmaps, coverage polygons on cluster hover
- Default map overlay of all fast-travel/tower icons
- Configurable T/N/radius via YAML or admin UI

## Architecture

```
webui/src/components/PlayerTimeline.tsx   # shell: filters, list, map, presets, transport
webui/src/map/mapLandmarks.ts             # static table + nearest match + knownMapLocation
webui/src/map/mapTrajectory.ts            # hybrid window polyline, ping bins/colors
webui/src/map/mapPlayback.ts              # interval stepping, speeds, stop/reset helpers
webui/Dockerfile                          # LFS pull stage or documented equivalent
package.json                              # leaflet.markercluster (+ types if needed)
```

Pure helpers stay free of React/Leaflet side effects so unit tests do not need a real map. Leaflet wiring (cluster group, polyline layer, focus marker) remains in `TimelineMap` inside `PlayerTimeline.tsx` (or a sibling component file if the shell grows past a comfortable size).

### Default constants

| Constant | Default | Notes |
|----------|---------|--------|
| Time window ±T | 10 minutes | Around cursor time |
| Max N samples | 12 | Cap after time filter, centered on anchor |
| Landmark radius R | 25_000 world units | Tune against real FT spacing if needed |
| Base play step | 800 ms | Per index step at 1× |
| Speeds | 1× / 2× / 4× | Interval = 800 / speed |
| Ping bins (ms) | &lt;50, 50–80, 80–120, 120–200, &gt;200 | Traffic-light fills |

## Feature design

### 1. Autoplay

- Controls sit next to the existing range cursor: previous step, play/pause, speed select, then the range input.
- Play advances `cursorIndex` by one on an interval derived from speed; at the last item, playback pauses and stays on the last item.
- Pause, dragging the range input, or selecting a list row (crosshair) stops playback.
- Changing player, start/end range, or `refreshKey` resets the cursor (existing behaviour) and stops playback.
- `prefers-reduced-motion` does not disable stepping; map pan stays non-animated (`animate: false` as today).

### 2. Time range presets

- Above the two `datetime-local` fields: buttons **最近 1 小时**, **最近 24 小时**, **最近 7 天**.
- Each sets local wall-clock `start`/`end` via existing `localInputValue` helpers and reuses the current fetch effect.
- Manual datetime edits remain valid; presets do not require two-way active-state binding.
- Keep the note: local time · max 31 days · 500 rows per category.

### 3. Hybrid trajectory polyline

Algorithm (`mapTrajectory`):

1. Resolve anchor sample with the same semantics as `latestPointAt` (latest sample at or before cursor time).
2. Restrict to samples with the same `segment_id` as the anchor.
3. Keep those with `|observed_at - activeAt| ≤ T`.
4. If more than N remain, trim to N centered on the anchor (prefer past side on odd overflow).
5. If ≥ 2 points, draw one polyline: `weight: 2`, teal stroke, round caps/joins.
6. Never connect across segment boundaries.

All trajectory samples still render as point markers (subject to clustering); only the windowed subset is connected.

### 4. MarkerCluster

- Dependency: `leaflet.markercluster` and its CSS.
- Non-focus samples go into a `MarkerClusterGroup` (`showCoverageOnHover: false`).
- Focus (cursor) marker and the polyline live on separate layers and never enter the cluster group.
- Existing `panTo` on focus remains.
- Cluster chrome: neutral teal + count; do **not** color clusters by worst ping (avoids misleading aggregates). Ping color applies to individual markers after spiderfy/uncluster.

### 5. Dual-mode ping coloring

| Mode | Marker style |
|------|----------------|
| **位置** (default) | Current teal ring + light fill |
| **延迟** | Fill from traffic-light bins; darker stroke |

- Toggle: segmented control **位置 | 延迟** near the map header/legend (`aria-pressed` / radiogroup pattern).
- Legend row only visible in 延迟 mode.
- Focus marker keeps gold outer emphasis; in 延迟 mode the inner fill may follow ping.
- Invalid or missing ping uses a distinct grey in 延迟 mode.

### 6. Dockerfile Git LFS

- Goal: image builds materialise `webui/public/map/tiles/**` LFS objects instead of pointer files when possible.
- Preferred: install `git-lfs` in the Node build stage and `git lfs pull` with include filter for map tiles **when `.git` is available in the build context**.
- If compose/build context is `webui/` only (no git metadata), document and implement one of:
  - raise build context to the repo root and adjust `COPY` paths; or
  - require CI to run `git lfs pull` before `docker build`.
- Runtime **keeps** the existing per-tile palworld.gg fallback; LFS is a local-hit optimisation, not a hard dependency for map display.

### 7. Landmark labels (`mapLandmarks.ts`)

- Static table: `{ id, nameZh, x, y, kind: 'fast_travel' | 'boss_tower' }[]` in the same world axes as REST `location_x` / `location_y`.
- Coordinates aligned with the reference `points.json` (zaigie/palworld-server-tool). Chinese names from a documented redistributable source; any unnamed entry may use a temporary `传送点 #i` / `塔 #i` only if a zh name is unavailable.
- `knownMapLocation(sample)`: nearest landmark with Euclidean distance ≤ R → `靠近 · ${nameZh}`; else `''` so UI keeps **地图坐标位置** / list fallback copy.
- Do not render the full landmark icon set on the map in this work (labels only).

Asset licensing: static names/coords are data, not map tiles; provenance of the name table must be noted in code comment or README snippet next to the table.

## UI copy (zh)

| Control | Label |
|---------|--------|
| Preset 1h | 最近 1 小时 |
| Preset 24h | 最近 24 小时 |
| Preset 7d | 最近 7 天 |
| Play | 播放 |
| Pause | 暂停 |
| Color mode position | 位置 |
| Color mode ping | 延迟 |
| Near landmark | 靠近 · {name} |
| Legend title | 延迟 |

## Testing

### Unit (no DOM map)

- `mapTrajectory`: window ∩ N; segment isolation; empty/single-point no polyline; odd N bias; color bin boundaries.
- `mapLandmarks`: hit inside R; miss outside R; empty table; stable nearest when equidistant (document tie-break: smaller id or lower distance then name).
- `mapPlayback`: interval for each speed; stop at end; stop on external reset.
- Existing datetime / `tileErrorTransition` tests remain green.

### Component (Testing Library)

- Preset buttons set range and trigger timeline fetch with expected ISO bounds (approx within test clock).
- Play control advances cursor under fake timers; reaches end and shows paused state.
- Color mode toggle updates legend visibility / accessible state.
- Location label shows `靠近 · …` when fixture sample sits on a known landmark coordinate.
- Regression: empty player, abort-on-replace, 31-day validation, private samples.

### Build / deploy smoke

- Dockerfile change reviewed against actual compose build context.
- Optional: build with LFS present proves tiles are PNGs not pointer text; without LFS, app still builds and runtime fallback still works.

## Acceptance criteria

1. With ≥2 timeline items, user can play/pause and optionally change speed; cursor drives map focus and list active row as today.
2. One click applies last 1h / 24h / 7d without typing datetimes; manual range and 31-day rule still work.
3. Map draws a thin polyline only for the hybrid window on the active segment; other samples are dots only.
4. At low zoom, dense samples cluster; focus marker is never swallowed by a cluster.
5. Default markers match current teal look; 延迟 mode colors by ping bins with a visible legend.
6. WebUI image build path either pulls LFS tiles or documents the pre-build pull; pointer-only trees still degrade via palworld.gg fallback.
7. Samples near a table landmark show Chinese proximity labels in list and map meta; far samples keep coordinate fallback copy.
8. `npm test` (webui) and existing Go tests remain passing; no API contract change required.

## Implementation order (for planning)

1. Pure modules + unit tests (`mapTrajectory`, `mapLandmarks`, `mapPlayback`).
2. Wire polyline + dual color + cluster into `TimelineMap`.
3. Presets + transport controls in the shell.
4. Landmark table data + label wiring.
5. Dockerfile / compose LFS stage.
6. Component tests and manual map smoke.

## Relationship to prior design

Extends the “Real Map and Replay” direction in `2026-07-13-palworld-trace-world-analytics-design.md` (cursor play/pause/speed, versioned static locations) for the **current player timeline only**. Full Replay scopes remain future work.
