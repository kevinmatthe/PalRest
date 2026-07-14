import { MAP_LANDMARKS, type MapLandmark } from '../map/mapLandmarks';
import { summarizeBehavior } from './behaviorMetrics';
import {
  D_IDLE,
  POI_DWELL_TOP_N,
  POI_RADIUS_FT,
  POI_RADIUS_GUILD,
  POI_RADIUS_TOWER,
  T_ACTIVE_CAP_MS,
  T_GAP_MS,
  TELEPORT_MIN_DIST,
  TELEPORT_TOP_N,
  V_IDLE,
  V_TRAVEL,
  type BehaviorEdgeClass,
  type BehaviorPoint,
  type BehaviorPOI,
  type BehaviorPOIKind,
  type BehaviorSummary,
  type POIDwell,
  type SummarizeBehaviorOptions,
  type TeleportSuspect,
} from './behaviorTypes';

export function poiRadius(kind: BehaviorPOIKind): number {
  switch (kind) {
    case 'fast_travel':
      return POI_RADIUS_FT;
    case 'boss_tower':
      return POI_RADIUS_TOWER;
    case 'guild_base':
      return POI_RADIUS_GUILD;
    default: {
      const _exhaustive: never = kind;
      return _exhaustive;
    }
  }
}

export function staticLandmarksToPOIs(landmarks: MapLandmark[] = MAP_LANDMARKS): BehaviorPOI[] {
  return landmarks.map((lm) => ({
    id: lm.id,
    nameZh: lm.nameZh,
    kind: lm.kind,
    x: lm.x,
    y: lm.y,
  }));
}

export function nearestPOI(
  point: { x: number; y: number },
  pois: BehaviorPOI[],
): BehaviorPOI | undefined {
  let best: BehaviorPOI | undefined;
  let bestDist = Infinity;
  for (const poi of pois) {
    const radius = poiRadius(poi.kind);
    const d = Math.hypot(poi.x - point.x, poi.y - point.y);
    if (d > radius) continue;
    if (d < bestDist || (d === bestDist && (!best || poi.id < best.id))) {
      best = poi;
      bestDist = d;
    }
  }
  return best;
}

function finitePoint(p: BehaviorPoint): boolean {
  return Number.isFinite(p.x) && Number.isFinite(p.y) && Number.isFinite(Date.parse(p.observed_at));
}

function classifyEdge(
  a: BehaviorPoint,
  b: BehaviorPoint,
  dist: number,
  dtMs: number,
  tGapMs: number,
  dIdle: number,
  vIdle: number,
  vTravel: number,
): BehaviorEdgeClass {
  if (a.segment_id !== b.segment_id || dtMs > tGapMs) return 'gap';
  const speed = dist / (dtMs / 1000);
  if (dist < dIdle || speed < vIdle) return 'stationary';
  if (speed >= vTravel) return 'traveling';
  return 'local';
}

type DwellAcc = {
  poi: BehaviorPOI;
  dwellMs: number;
  sampleHits: number;
};

export function enrichBehaviorWithPOIs(
  summary: BehaviorSummary,
  samples: BehaviorPoint[],
  pois: BehaviorPOI[],
  options?: { tGapMs?: number; tActiveCapMs?: number; teleportMinDist?: number },
): BehaviorSummary {
  const tGapMs = options?.tGapMs ?? T_GAP_MS;
  const tActiveCapMs = options?.tActiveCapMs ?? T_ACTIVE_CAP_MS;
  const teleportMinDist = options?.teleportMinDist ?? TELEPORT_MIN_DIST;

  const sorted = [...samples]
    .filter(finitePoint)
    .sort((a, b) => Date.parse(a.observed_at) - Date.parse(b.observed_at));

  const hits = sorted.map((p) => nearestPOI(p, pois));
  const dwellById = new Map<string, DwellAcc>();

  const touch = (poi: BehaviorPOI): DwellAcc => {
    let acc = dwellById.get(poi.id);
    if (!acc) {
      acc = { poi, dwellMs: 0, sampleHits: 0 };
      dwellById.set(poi.id, acc);
    }
    return acc;
  };

  for (const hit of hits) {
    if (hit) touch(hit).sampleHits += 1;
  }

  const edgeByFrom = new Map(summary.edges.map((e) => [e.fromIndex, e]));
  const teleports: TeleportSuspect[] = [];

  for (let i = 0; i < sorted.length - 1; i += 1) {
    const a = sorted[i]!;
    const b = sorted[i + 1]!;
    const ta = Date.parse(a.observed_at);
    const tb = Date.parse(b.observed_at);
    const dtMs = tb - ta;
    if (!(dtMs > 0)) continue;

    const dist = Math.hypot(b.x - a.x, b.y - a.y);
    const edge = edgeByFrom.get(i);
    const edgeClass =
      edge && edge.toIndex === i + 1
        ? edge.class
        : classifyEdge(a, b, dist, dtMs, tGapMs, D_IDLE, V_IDLE, V_TRAVEL);

    const ha = hits[i];
    const hb = hits[i + 1];

    if (edgeClass === 'stationary' && ha && hb && ha.id === hb.id) {
      touch(ha).dwellMs += Math.min(dtMs, tActiveCapMs);
    }

    const isGap = a.segment_id !== b.segment_id || dtMs > tGapMs;
    const ftA = ha?.kind === 'fast_travel' ? ha : undefined;
    const ftB = hb?.kind === 'fast_travel' ? hb : undefined;
    const ftInvolved = Boolean(ftA || ftB);
    if (!ftInvolved) continue;
    if (ha && hb && ha.id === hb.id) continue;

    // Prefer labeling with FT ends when present; otherwise fall back to any hit.
    const fromPoi = ftA ?? ha;
    const toPoi = ftB ?? hb;

    if (isGap) {
      const differentFtHits = Boolean(ftA && ftB && ftA.id !== ftB.id);
      if (dist >= teleportMinDist || differentFtHits) {
        teleports.push({
          fromLandmarkId: fromPoi?.id,
          fromNameZh: fromPoi?.nameZh,
          toLandmarkId: toPoi?.id,
          toNameZh: toPoi?.nameZh,
          fromX: fromPoi?.x ?? a.x,
          fromY: fromPoi?.y ?? a.y,
          toX: toPoi?.x ?? b.x,
          toY: toPoi?.y ?? b.y,
          dist,
          dtMs,
          reason: 'gap_hop',
          at: b.observed_at,
        });
      }
    } else if (dist >= teleportMinDist) {
      teleports.push({
        fromLandmarkId: fromPoi?.id,
        fromNameZh: fromPoi?.nameZh,
        toLandmarkId: toPoi?.id,
        toNameZh: toPoi?.nameZh,
        fromX: fromPoi?.x ?? a.x,
        fromY: fromPoi?.y ?? a.y,
        toX: toPoi?.x ?? b.x,
        toY: toPoi?.y ?? b.y,
        dist,
        dtMs,
        reason: 'long_jump',
        at: b.observed_at,
      });
    }
  }

  const dwellList: POIDwell[] = [...dwellById.values()]
    .filter((d) => d.dwellMs > 0 || d.sampleHits > 0)
    .map((d) => ({
      poiId: d.poi.id,
      nameZh: d.poi.nameZh,
      kind: d.poi.kind,
      dwellMs: d.dwellMs,
      sampleHits: d.sampleHits,
      x: d.poi.x,
      y: d.poi.y,
    }))
    .sort((a, b) => b.dwellMs - a.dwellMs || b.sampleHits - a.sampleHits || a.poiId.localeCompare(b.poiId));

  const poiDwells = dwellList.filter((d) => d.dwellMs > 0).slice(0, POI_DWELL_TOP_N);

  let activityAnchor: POIDwell | undefined;
  const withDwell = dwellList.filter((d) => d.dwellMs > 0);
  if (withDwell.length) {
    activityAnchor = withDwell[0];
  } else {
    const byHits = [...dwellList].sort(
      (a, b) => b.sampleHits - a.sampleHits || a.poiId.localeCompare(b.poiId),
    );
    if (byHits[0] && byHits[0].sampleHits > 0) activityAnchor = byHits[0];
  }

  teleports.sort((a, b) => b.dist - a.dist || a.at.localeCompare(b.at));
  const teleportSuspects = teleports.slice(0, TELEPORT_TOP_N);

  const hitCount = hits.filter(Boolean).length;
  const poiHitRate = sorted.length > 0 ? hitCount / sorted.length : 0;

  const guildAccs = [...dwellById.values()].filter(
    (d) => d.poi.kind === 'guild_base' && (d.dwellMs > 0 || d.sampleHits > 0),
  );
  let guildPresence: BehaviorSummary['guildPresence'];
  if (guildAccs.length) {
    guildPresence = {
      guildName: guildAccs.find((g) => g.poi.guildName)?.poi.guildName,
      baseCount: guildAccs.length,
      dwellMs: guildAccs.reduce((sum, g) => sum + g.dwellMs, 0),
    };
  }

  return {
    ...summary,
    poiDwells,
    activityAnchor,
    teleportSuspects,
    poiHitRate,
    guildPresence,
  };
}

export function analyzeTrajectoryBehavior(
  samples: BehaviorPoint[],
  options?: SummarizeBehaviorOptions & { pois?: BehaviorPOI[] },
): BehaviorSummary {
  const base = summarizeBehavior(samples, options);
  const pois = options?.pois ?? staticLandmarksToPOIs();
  return enrichBehaviorWithPOIs(base, samples, pois, {
    tGapMs: options?.tGapMs,
    tActiveCapMs: options?.tActiveCapMs,
  });
}
