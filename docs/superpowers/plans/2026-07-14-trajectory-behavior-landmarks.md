# Trajectory Behavior × World POIs (A2) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans.

**Goal:** Bind trajectories to fast travel, boss towers, and (when save data exists) guild base camps; show dwell/anchor/teleports in the behavior panel.

**Architecture:** Unified `BehaviorPOI` enrichment pure module; static POIs from `MAP_LANDMARKS`; optional `GET .../world-pois` for guild bases; compose in PlayerTimeline.

**Spec:** `docs/superpowers/specs/2026-07-14-trajectory-behavior-landmarks-design.md`

---

## File Structure

| File | Role |
|------|------|
| `webui/src/behavior/behaviorTypes.ts` | POI kinds, dwell, teleport, summary fields |
| `webui/src/behavior/behaviorPOIs.ts` | nearestPOI, static convert, enrich, analyzeTrajectoryBehavior |
| `webui/src/behavior/behaviorPOIs.test.ts` | Pure tests with fixture POIs |
| `webui/src/behavior/behaviorFormat.ts` | Kind / teleport labels |
| `webui/src/components/BehaviorSummaryPanel.tsx` | POI sections |
| `webui/src/api.ts` | `getPlayerWorldPOIs` |
| `internal/store/save_imports.go` (or new query file) | ListPlayerWorldPOIs |
| `internal/api/` | Handler + tests |
| `PlayerTimeline.tsx` | Fetch POIs + analyze |

---

### Task 1: Types

Extend `behaviorTypes.ts`:

```ts
export type BehaviorPOIKind = 'fast_travel' | 'boss_tower' | 'guild_base';
export type BehaviorPOI = {
  id: string;
  nameZh: string;
  kind: BehaviorPOIKind;
  x: number;
  y: number;
  guildName?: string;
};
export type POIDwell = {
  poiId: string;
  nameZh: string;
  kind: BehaviorPOIKind;
  dwellMs: number;
  sampleHits: number;
};
export type TeleportSuspect = { /* as design */ };
// BehaviorSummary fields:
poiDwells: POIDwell[];
activityAnchor?: POIDwell;
teleportSuspects: TeleportSuspect[];
poiHitRate: number;
guildPresence?: { guildName?: string; baseCount: number; dwellMs: number };
```

Constants: `TELEPORT_MIN_DIST=50000`, radii per kind, top N=5.

Update `summarizeBehavior` empty fields + panel test fixtures.

Commit: `feat(webui): add behavior POI types for FT, towers, and guild bases`

---

### Task 2: Pure enrichment (A2a)

`behaviorPOIs.ts`:

- `staticLandmarksToPOIs(MAP_LANDMARKS)`
- `nearestPOI(point, pois, radii?)`
- `enrichBehaviorWithPOIs(summary, samples, pois, options)`
- `analyzeTrajectoryBehavior(samples, options & { pois? })` → summarize + enrich (default pois = static)

Dwell: stationary + same poi id both ends.  
Teleport: FT-oriented only.  
Guild presence when any guild_base hits.

Tests with fixture FT + tower + guild_base.

Commit: `feat(webui): enrich behavior with multi-kind world POIs`

---

### Task 3: Format + panel

Kind labels: 传送点 / 首领塔 / 公会据点.  
Sections: anchor, dwell top (kind badge), guild line, teleports.

Commit: `feat(webui): show POI dwell and teleports in behavior panel`

---

### Task 4: Store + API world-pois (A2b)

`ListPlayerWorldPOIs(ctx, userID)`:

1. Latest save_imports by imported_at  
2. Identity mapping user_id → save_player_hex  
3. Guild membership for that hex  
4. Base camps for guild with location_x/y  

Handler `GET /api/v1/players/{userID}/world-pois`  
Tests with import fixture.

Commit: `feat: API for player world POIs from save guild bases`

---

### Task 5: Wire timeline

- Fetch world-pois when selectedID changes (soft fail)  
- `pois = static ∪ api`  
- `analyzeTrajectoryBehavior(samples, { window, pois })`  
- Panel already bound to summary  

Commit: `feat(webui): merge guild base POIs into trajectory behavior analysis`

---

### Task 6: Verify

`go test ./...` + `cd webui && npm test && npm run build`  
Acceptance checklist from design.
