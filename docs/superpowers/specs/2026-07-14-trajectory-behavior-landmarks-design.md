# Trajectory Behavior × World POIs Design (Phase A2)

**Date:** 2026-07-14  
**Parent:** `docs/superpowers/specs/2026-07-14-trajectory-behavior-analysis-design.md` (phase A shipped)  
**Priority (ROI):** A2 (this) → C map overlay → B Analytics rankings  

## Goal

Deepen behavior analysis by binding trajectories to **world points of interest (POIs)**—not only fast travel:

| POI kind | Source today | Available offline? |
|----------|--------------|--------------------|
| `fast_travel` | Static `MAP_LANDMARKS` | Yes |
| `boss_tower` | Static `MAP_LANDMARKS` (9 towers) | Yes |
| `guild_base` | Latest save import: `save_base_camps` + guild membership via `save_identity_mappings` | Only when save worker has imported a snapshot |

The timeline Behavior Summary gains: dwell-by-POI (with kind), activity anchor, suspected teleports between FTs, and **guild-base presence** when save data exists—so analysis answers **where**, not only how fast.

## Decisions

| Item | Decision |
|------|----------|
| Abstraction | Unified `BehaviorPOI` (`id`, `nameZh`, `kind`, `x`, `y`, optional `meta`) |
| Static POIs | Always include **all** `MAP_LANDMARKS` (FT + boss towers)—not FT-only |
| Guild bases | Merge dynamic POIs from API when available; degrade gracefully if none |
| Matching | `nearestPOI` (same algorithm as `nearestLandmark`) with kind-aware radius defaults |
| Dwell | Stationary edges with **both ends** hitting the same POI id |
| Teleports | Prefer FT↔FT (or gap hops); do **not** label boss/base hops as teleports unless both ends are FT |
| Panel | Group dwells by kind tabs or badges: 传送点 / 首领塔 / 公会据点 |
| Server for B/C | Out of scope beyond the small POI feed API for guild bases |

## In scope

### A2a — Static world POIs (always on)

1. Pure enrichment over `BehaviorSummary` + samples + POI list.
2. Dwell / anchor / teleport-suspect using FT **and** boss towers.
3. Panel sections with kind labels.
4. Unit + component tests.

### A2b — Guild base POIs (save-backed)

1. Store query: latest successful save import → base camps linked to the selected player’s guild (via identity mapping).
2. Read API (auth: same as private timeline if sensitive, else public aggregate of **coordinates + guild name** only—no IP):
   - `GET /api/v1/players/{userID}/world-pois`  
   - Returns `{ pois: BehaviorPOI[], as_of?, source: 'save_import' | 'none' }`
3. Timeline loads POIs for selected player (best-effort; failure → static only).
4. Enrichment merges `staticLandmarksToPOIs(MAP_LANDMARKS) ∪ guildBasePOIs`.

## Out of scope

- Daily rollups / Analytics rankings (B)
- Leaflet drawing (C)—will consume POI hits later
- Wild boss spawn tables beyond the 9 static towers
- Full guild/base World UI
- Claiming true game-teleport certainty (always **疑似**)

## Architecture

```
                    ┌─ MAP_LANDMARKS (FT + towers) ──────────────┐
trajectorySamples → │                                           ├→ enrichBehaviorWithPOIs → panel
                    └─ GET world-pois (guild bases, optional) ───┘
                           summarizeBehavior (motion) ───────────┘
```

```
webui/src/behavior/behaviorTypes.ts      # POI + dwell + teleport types
webui/src/behavior/behaviorPOIs.ts       # nearestPOI, static convert, enrich*
webui/src/behavior/behaviorPOIs.test.ts
webui/src/behavior/behaviorFormat.ts     # kind labels
webui/src/components/BehaviorSummaryPanel.tsx
webui/src/api.ts                         # getPlayerWorldPOIs
internal/store/...                       # ListPlayerWorldPOIs
internal/api/...                         # handler
```

Motion metrics stay in `summarizeBehavior` (no POI dependency). Composition:

```ts
analyzeTrajectoryBehavior(samples, { window..., pois: BehaviorPOI[] })
```

## POI model

```ts
type BehaviorPOIKind = 'fast_travel' | 'boss_tower' | 'guild_base';

type BehaviorPOI = {
  id: string;           // ft-*, tw-*, or gb-{guildId}-{baseUid}
  nameZh: string;       // e.g. 中央 · 传送点 3 / 初始之塔 / 公会「狼」据点
  kind: BehaviorPOIKind;
  x: number;
  y: number;
  guildName?: string;   // guild_base only
  area?: number;        // base camp area if useful later
};
```

### Radii

| Kind | Default radius (world) | Rationale |
|------|------------------------|-----------|
| `fast_travel` | 25_000 | Existing `DEFAULT_LANDMARK_RADIUS` |
| `boss_tower` | 30_000 | Slightly larger arena footprint |
| `guild_base` | `max(25_000, sqrt(area)*k)` or fixed 40_000 | Bases are larger; MVP fixed **40_000** |

`nearestPOI` walks all POIs with their kind radius (or max of defaults).

## Algorithms

### 1. Hit assignment

For each sorted sample: `nearestPOI(point, pois)` → hit or none.  
`landmarkHitRate` → rename conceptually to `poiHitRate` (keep field name `landmarkHitRate` for less churn **or** rename to `poiHitRate` in A2—**prefer `poiHitRate`** with `landmarkHitRate` alias not needed if A1 just shipped empty fields).

Phase A added empty `landmarkDwells` etc. in plan—if not yet on main metrics return, introduce `poiDwells` as the canonical name:

```ts
poiDwells: POIDwell[];
activityAnchor?: POIDwell;
teleportSuspects: TeleportSuspect[];
poiHitRate: number;
```

(If code already has `landmark*` names from an unstarted plan, use `poi*` as specified here.)

### 2. Dwell

Same as prior A2: stationary edge, both ends same `poi.id` → add capped `dtMs`.  
Sort top 5 by dwellMs; show kind badge.

### 3. Anchor

Max dwellMs, else max sampleHits, else undefined. Prefer showing kind in chip.

### 4. Teleport suspects

Only promote to teleport UI when:

- reason gap_hop / long_jump **and**
- at least one endpoint hit is `fast_travel`, **and**
- if both hits exist, not both the same id.

Boss-only or base-only large moves stay **out** of teleport list (still contribute to dwell/path). Optional debug later: `largeMoves`—not MVP.

### 5. Guild presence (derived)

If any `guild_base` dwell or sampleHits &gt; 0:

```ts
guildPresence?: {
  guildName?: string;
  baseCount: number;
  dwellMs: number; // sum over that guild's bases
}
```

Panel line:「公会据点停留 · {duration}」.

## API (A2b)

### `GET /api/v1/players/{userID}/world-pois`

**Auth:** Same as player timeline (public ok for coordinates of bases that are already on the shared map of the server; if product wants admin-only, mirror private timeline). **Default: public read** of name+xy only (no member list).

**Response:**

```json
{
  "user_id": "…",
  "source": "save_import",
  "as_of": "2026-07-14T08:00:00Z",
  "pois": [
    {
      "id": "gb-…",
      "name_zh": "公会「示例」据点",
      "kind": "guild_base",
      "x": 1.0,
      "y": 2.0,
      "guild_name": "示例"
    }
  ]
}
```

`source: "none"` + empty pois when no import or no mapping.

**Store:** Latest `save_imports` by `imported_at`; join identity → guild member → guild → base camps with locations.

## UI

| Block | Content |
|-------|---------|
| 活动锚点 | name + kind badge + dwell |
| 驻留 Top | list with 传送点/首领塔/公会据点 colors |
| 公会据点 | optional summary line if guildPresence |
| 疑似传送 | FT-oriented only |
| Empty |「未匹配到传送点、首领塔或公会据点附近的驻留」 |

## Testing

- Static: FT dwell, tower dwell, mixed top list kinds  
- Teleport: FT→FT only  
- Dynamic: enrich with injected guild_base POIs  
- API: store fixture with import + identity + base → handler returns pois  
- Panel: renders kind badges  

## Acceptance

1. Near boss tower stationary time appears under 首领塔.  
2. Near FT appears under 传送点; teleports still work.  
3. With save data + mapping, guild base dwell appears; without, panel still works (static only).  
4. Motion metrics unchanged.  
5. Tests + build green.

## Implementation order

1. Types + pure POI enrich + static FT/tower (A2a) + panel.  
2. Store + API world-pois (A2b).  
3. Timeline fetch merge + loading/error soft-fail.  
4. Stop before map overlay / rankings.

## Risks

| Risk | Mitigation |
|------|------------|
| No save import / identity | Soft-fail; static POIs only |
| Base radius wrong | Fixed 40k; tune later |
| Name quality | Guild name from save; FT region prefix already applied |
| Privacy of base coords | Server-local ops tool; coords already imply world state |
