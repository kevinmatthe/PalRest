import { describe, expect, it } from 'vitest';
import {
  DEFAULT_TRAJECTORY_MAX_POINTS,
  DEFAULT_TRAJECTORY_WINDOW_MS,
  hybridTrajectoryWindow,
  pingBin,
  pingColor,
  splitTrajectoryPastFuture,
  TRAJ_DASH_ARRAY,
  TRAJ_FUTURE_OPACITY,
  TRAJ_FUTURE_WEIGHT,
  TRAJ_PAST_COLOR,
  TRAJ_PAST_OPACITY,
  TRAJ_PAST_WEIGHT,
  TRAJ_TIP_COLOR,
  type TrajectoryPointLike,
} from './mapTrajectory';

function pt(
  partial: Partial<TrajectoryPointLike> & Pick<TrajectoryPointLike, 'observed_at' | 'segment_id' | 'x' | 'y'>,
): TrajectoryPointLike {
  return { ping: 40, ...partial };
}

describe('hybridTrajectoryWindow', () => {
  const base = [
    pt({ observed_at: '2026-07-14T10:00:00.000Z', segment_id: 's1', x: 0, y: 0 }),
    pt({ observed_at: '2026-07-14T10:05:00.000Z', segment_id: 's1', x: 1, y: 0 }),
    pt({ observed_at: '2026-07-14T10:10:00.000Z', segment_id: 's1', x: 2, y: 0 }),
    pt({ observed_at: '2026-07-14T10:15:00.000Z', segment_id: 's2', x: 3, y: 0 }),
    pt({ observed_at: '2026-07-14T10:20:00.000Z', segment_id: 's2', x: 4, y: 0 }),
  ];

  it('keeps same segment only and applies time window around cursor', () => {
    const got = hybridTrajectoryWindow(base, '2026-07-14T10:10:00.000Z', {
      windowMs: 6 * 60_000,
      maxPoints: 12,
    });
    // cursor 10:10, window ±6min → 10:05 and 10:10 in s1; 10:00 is 10min away (out)
    expect(got.map((p) => p.x)).toEqual([1, 2]);
  });

  it('does not connect across segments', () => {
    const got = hybridTrajectoryWindow(base, '2026-07-14T10:20:00.000Z', {
      windowMs: 60 * 60_000,
      maxPoints: 12,
    });
    expect(got.every((p) => p.segment_id === 's2')).toBe(true);
    expect(got.map((p) => p.x)).toEqual([3, 4]);
  });

  it('caps to N centered on anchor (prefer past on odd overflow)', () => {
    const many = Array.from({ length: 20 }, (_, i) =>
      pt({
        observed_at: new Date(Date.parse('2026-07-14T10:00:00.000Z') + i * 60_000).toISOString(),
        segment_id: 's1',
        x: i,
        y: 0,
      }),
    );
    const got = hybridTrajectoryWindow(many, many[10]!.observed_at, {
      windowMs: 24 * 60 * 60_000,
      maxPoints: 5,
    });
    expect(got).toHaveLength(5);
    expect(got.map((p) => p.x)).toEqual([8, 9, 10, 11, 12]);
  });

  it('caps even N with past preference (fails under prefer-future)', () => {
    // N=12 at center 15 → indices 9..20 (6 past + anchor + 5 future)
    // prefer-future would yield 10..21 instead
    const many = Array.from({ length: 30 }, (_, i) =>
      pt({
        observed_at: new Date(Date.parse('2026-07-14T10:00:00.000Z') + i * 60_000).toISOString(),
        segment_id: 's1',
        x: i,
        y: 0,
      }),
    );
    const got = hybridTrajectoryWindow(many, many[15]!.observed_at, {
      windowMs: 24 * 60 * 60_000,
      maxPoints: 12,
    });
    expect(got).toHaveLength(12);
    expect(got.map((p) => p.x)).toEqual([9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20]);
  });

  it('returns empty or single without implying a line caller needs ≥2', () => {
    expect(hybridTrajectoryWindow([], '2026-07-14T10:00:00.000Z')).toEqual([]);
    expect(hybridTrajectoryWindow([base[0]!], base[0]!.observed_at)).toHaveLength(1);
  });

  it('uses design defaults of 10min window and 16 max points', () => {
    // 1 point/min for 30 minutes on one segment; cursor mid-window
    const many = Array.from({ length: 31 }, (_, i) =>
      pt({
        observed_at: new Date(Date.parse('2026-07-14T10:00:00.000Z') + i * 60_000).toISOString(),
        segment_id: 's1',
        x: i,
        y: 0,
      }),
    );
    const cursor = many[15]!.observed_at; // 10:15
    const got = hybridTrajectoryWindow(many, cursor);
    // ±10min → indices 5..25 (21 pts), cap 16 past-preferring around 15
    // ceil(15/2)=8 past + anchor + floor(15/2)=7 future → indices 7..22
    expect(got).toHaveLength(DEFAULT_TRAJECTORY_MAX_POINTS);
    expect(got.map((p) => p.x)).toEqual([7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22]);
    // points outside ±10min of cursor are excluded before cap
    const far = hybridTrajectoryWindow(
      [
        pt({ observed_at: '2026-07-14T09:00:00.000Z', segment_id: 's1', x: -1, y: 0 }),
        pt({ observed_at: '2026-07-14T10:15:00.000Z', segment_id: 's1', x: 0, y: 0 }),
      ],
      '2026-07-14T10:15:00.000Z',
    );
    expect(far.map((p) => p.x)).toEqual([0]);
  });
});

describe('pingColor', () => {
  it('maps colorblind-friendly bins with radius/glyph channels', () => {
    expect(pingColor(20).fill).toMatch(/#/);
    expect(pingColor(20).bin).toBe('lt50');
    expect(pingBin(49.9)).toBe('lt50');
    expect(pingBin(50)).toBe('50_80');
    expect(pingColor(60).bin).toBe('50_80');
    expect(pingBin(80)).toBe('80_120');
    expect(pingColor(100).bin).toBe('80_120');
    expect(pingBin(120)).toBe('120_200');
    expect(pingColor(150).bin).toBe('120_200');
    expect(pingBin(200)).toBe('gt200');
    expect(pingColor(250).bin).toBe('gt200');
    expect(pingColor(Number.NaN).bin).toBe('unknown');
    expect(pingBin(-1)).toBe('unknown');
    expect(pingColor(20).stroke).toMatch(/#/);
    // Higher latency → larger marker (redundant encoding).
    expect(pingColor(20).radius).toBeLessThan(pingColor(250).radius);
    expect(pingColor(20).glyph).toBeTruthy();
    expect(pingColor(250).glyph).not.toBe(pingColor(20).glyph);
  });
});

describe('defaults', () => {
  it('exports design defaults', () => {
    expect(DEFAULT_TRAJECTORY_WINDOW_MS).toBe(10 * 60_000);
    expect(DEFAULT_TRAJECTORY_MAX_POINTS).toBe(16);
  });
});

describe('trajectory style constants', () => {
  it('past is thicker and more opaque than future', () => {
    expect(TRAJ_PAST_WEIGHT).toBeGreaterThan(TRAJ_FUTURE_WEIGHT);
    expect(TRAJ_PAST_OPACITY).toBeGreaterThan(TRAJ_FUTURE_OPACITY);
    expect(TRAJ_DASH_ARRAY).toBe('10 14');
    expect(TRAJ_PAST_COLOR).toMatch(/^#/);
    expect(TRAJ_TIP_COLOR).toMatch(/^#/);
  });
});

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
