import { describe, expect, it } from 'vitest';
import {
  analyzeTrajectoryBehavior,
  enrichBehaviorWithPOIs,
  nearestPOI,
  poiRadius,
  staticLandmarksToPOIs,
} from './behaviorPOIs';
import { summarizeBehavior } from './behaviorMetrics';
import {
  POI_RADIUS_FT,
  POI_RADIUS_GUILD,
  POI_RADIUS_TOWER,
  TELEPORT_MIN_DIST,
  type BehaviorPoint,
  type BehaviorPOI,
} from './behaviorTypes';
import { MAP_LANDMARKS } from '../map/mapLandmarks';

function pt(
  partial: Partial<BehaviorPoint> & Pick<BehaviorPoint, 'observed_at' | 'segment_id' | 'x' | 'y'>,
): BehaviorPoint {
  return { ping: 40, ...partial };
}

const fixturePOIs: BehaviorPOI[] = [
  { id: 'ft-a', nameZh: '传送点 A', kind: 'fast_travel', x: 0, y: 0 },
  { id: 'ft-b', nameZh: '传送点 B', kind: 'fast_travel', x: 100_000, y: 0 },
  { id: 'tw-1', nameZh: '首领塔 1', kind: 'boss_tower', x: 200_000, y: 0 },
  { id: 'gb-1', nameZh: '公会「狼」据点', kind: 'guild_base', x: 300_000, y: 0, guildName: '狼' },
];

const t0 = '2026-07-14T10:00:00.000Z';
const t1 = '2026-07-14T10:02:00.000Z';
const t2 = '2026-07-14T10:03:00.000Z';

describe('poiRadius', () => {
  it('returns kind-specific defaults', () => {
    expect(poiRadius('fast_travel')).toBe(POI_RADIUS_FT);
    expect(poiRadius('boss_tower')).toBe(POI_RADIUS_TOWER);
    expect(poiRadius('guild_base')).toBe(POI_RADIUS_GUILD);
  });
});

describe('staticLandmarksToPOIs', () => {
  it('maps MAP_LANDMARKS to BehaviorPOI', () => {
    const pois = staticLandmarksToPOIs(MAP_LANDMARKS);
    expect(pois.length).toBe(MAP_LANDMARKS.length);
    expect(pois[0]).toMatchObject({
      id: MAP_LANDMARKS[0]!.id,
      nameZh: MAP_LANDMARKS[0]!.nameZh,
      kind: MAP_LANDMARKS[0]!.kind,
      x: MAP_LANDMARKS[0]!.x,
      y: MAP_LANDMARKS[0]!.y,
    });
  });
});

describe('nearestPOI', () => {
  it('uses kind-specific radius', () => {
    // 26k from FT (radius 25k) → miss FT; 26k from tower with tower radius 30k would hit if tower nearby
    expect(nearestPOI({ x: 26_000, y: 0 }, [fixturePOIs[0]!])).toBeUndefined();
    const tower: BehaviorPOI = { id: 'tw-near', nameZh: '塔', kind: 'boss_tower', x: 0, y: 0 };
    expect(nearestPOI({ x: 26_000, y: 0 }, [tower])?.id).toBe('tw-near');
  });
});

describe('enrichBehaviorWithPOIs', () => {
  it('records stationary dwell near same FT and sets activity anchor', () => {
    const samples = [
      pt({ observed_at: t0, segment_id: 's1', x: 100, y: 50 }),
      pt({ observed_at: t1, segment_id: 's1', x: 120, y: 40 }),
    ];
    const base = summarizeBehavior(samples);
    const s = enrichBehaviorWithPOIs(base, samples, fixturePOIs);
    expect(s.poiDwells.length).toBeGreaterThan(0);
    expect(s.poiDwells[0]!.poiId).toBe('ft-a');
    expect(s.poiDwells[0]!.kind).toBe('fast_travel');
    expect(s.poiDwells[0]!.dwellMs).toBe(2 * 60_000);
    expect(s.activityAnchor?.poiId).toBe('ft-a');
    expect(s.poiHitRate).toBe(1);
  });

  it('records boss tower dwell', () => {
    const samples = [
      pt({ observed_at: t0, segment_id: 's1', x: 200_100, y: 0 }),
      pt({ observed_at: t1, segment_id: 's1', x: 200_050, y: 10 }),
    ];
    const s = enrichBehaviorWithPOIs(summarizeBehavior(samples), samples, fixturePOIs);
    expect(s.activityAnchor?.kind).toBe('boss_tower');
    expect(s.activityAnchor?.poiId).toBe('tw-1');
    expect(s.poiDwells[0]!.dwellMs).toBe(2 * 60_000);
  });

  it('records guild_base dwell and guildPresence', () => {
    const samples = [
      pt({ observed_at: t0, segment_id: 's1', x: 300_200, y: 0 }),
      pt({ observed_at: t1, segment_id: 's1', x: 300_100, y: 20 }),
    ];
    const s = enrichBehaviorWithPOIs(summarizeBehavior(samples), samples, fixturePOIs);
    expect(s.activityAnchor?.kind).toBe('guild_base');
    expect(s.activityAnchor?.poiId).toBe('gb-1');
    expect(s.guildPresence).toEqual({
      guildName: '狼',
      baseCount: 1,
      dwellMs: 2 * 60_000,
    });
  });

  it('returns empty POI fields for far points', () => {
    const samples = [
      pt({ observed_at: t0, segment_id: 's1', x: 1_000_000, y: 1_000_000 }),
      pt({ observed_at: t1, segment_id: 's1', x: 1_000_010, y: 1_000_000 }),
    ];
    const s = enrichBehaviorWithPOIs(summarizeBehavior(samples), samples, fixturePOIs);
    expect(s.poiDwells).toEqual([]);
    expect(s.activityAnchor).toBeUndefined();
    expect(s.teleportSuspects).toEqual([]);
    expect(s.poiHitRate).toBe(0);
    expect(s.guildPresence).toBeUndefined();
  });

  it('flags FT→FT long jump as teleport', () => {
    const samples = [
      pt({ observed_at: t0, segment_id: 's1', x: 0, y: 0 }),
      pt({ observed_at: t2, segment_id: 's1', x: 100_000, y: 0 }), // 60s, dist 100k >= TELEPORT_MIN_DIST
    ];
    const s = enrichBehaviorWithPOIs(summarizeBehavior(samples), samples, fixturePOIs);
    expect(TELEPORT_MIN_DIST).toBe(50_000);
    expect(s.teleportSuspects.length).toBe(1);
    const hop = s.teleportSuspects[0]!;
    expect(hop.reason).toBe('long_jump');
    expect(hop.fromLandmarkId).toBe('ft-a');
    expect(hop.toLandmarkId).toBe('ft-b');
    expect(hop.dist).toBeCloseTo(100_000, 0);
  });

  it('does not flag boss-only long jump as teleport', () => {
    const towerA: BehaviorPOI = { id: 'tw-a', nameZh: '塔A', kind: 'boss_tower', x: 0, y: 0 };
    const towerB: BehaviorPOI = { id: 'tw-b', nameZh: '塔B', kind: 'boss_tower', x: 100_000, y: 0 };
    const samples = [
      pt({ observed_at: t0, segment_id: 's1', x: 0, y: 0 }),
      pt({ observed_at: t2, segment_id: 's1', x: 100_000, y: 0 }),
    ];
    const s = enrichBehaviorWithPOIs(summarizeBehavior(samples), samples, [towerA, towerB]);
    expect(s.teleportSuspects).toEqual([]);
    // still counts tower hits / no dwell (traveling edge)
    expect(s.poiHitRate).toBe(1);
  });

  it('flags FT gap hop across segments as gap_hop teleport', () => {
    const samples = [
      pt({ observed_at: t0, segment_id: 's1', x: 0, y: 0 }),
      pt({ observed_at: t2, segment_id: 's2', x: 100_000, y: 0 }),
    ];
    const s = enrichBehaviorWithPOIs(summarizeBehavior(samples), samples, fixturePOIs);
    expect(s.teleportSuspects.length).toBe(1);
    expect(s.teleportSuspects[0]!.reason).toBe('gap_hop');
    expect(s.teleportSuspects[0]!.fromLandmarkId).toBe('ft-a');
    expect(s.teleportSuspects[0]!.toLandmarkId).toBe('ft-b');
    // Map arcs use player sample positions, not FT landmark centers.
    expect(s.teleportSuspects[0]!.fromX).toBe(0);
    expect(s.teleportSuspects[0]!.toX).toBe(100_000);
  });

  it('does not flag small segment-change near two FTs without large jump', () => {
    // Nearby FTs within radius can both hit, but short move must not draw a yellow arc.
    const closeFTs: BehaviorPOI[] = [
      { id: 'ft-1', nameZh: '近A', kind: 'fast_travel', x: 0, y: 0 },
      { id: 'ft-2', nameZh: '近B', kind: 'fast_travel', x: 20_000, y: 0 },
    ];
    const samples = [
      pt({ observed_at: t0, segment_id: 's1', x: 0, y: 0 }),
      pt({ observed_at: t2, segment_id: 's2', x: 15_000, y: 0 }), // dist 15k < 50k min
    ];
    const s = enrichBehaviorWithPOIs(summarizeBehavior(samples), samples, closeFTs);
    expect(s.teleportSuspects).toEqual([]);
  });
});

describe('analyzeTrajectoryBehavior', () => {
  it('composes summarize + enrich with provided pois', () => {
    const samples = [
      pt({ observed_at: t0, segment_id: 's1', x: 50, y: 0 }),
      pt({ observed_at: t1, segment_id: 's1', x: 80, y: 0 }),
    ];
    const s = analyzeTrajectoryBehavior(samples, { pois: fixturePOIs });
    expect(s.dominantClass).toBe('stationary');
    expect(s.activityAnchor?.poiId).toBe('ft-a');
  });
});
