import { describe, expect, it } from 'vitest';
import {
  DEFAULT_TRAJECTORY_MAX_POINTS,
  DEFAULT_TRAJECTORY_WINDOW_MS,
  hybridTrajectoryWindow,
  pingBin,
  pingColor,
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

  it('returns empty or single without implying a line caller needs ≥2', () => {
    expect(hybridTrajectoryWindow([], '2026-07-14T10:00:00.000Z')).toEqual([]);
    expect(hybridTrajectoryWindow([base[0]!], base[0]!.observed_at)).toHaveLength(1);
  });

  it('uses design defaults of 10min window and 12 max points', () => {
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
    // default window ±10min → indices 5..25 (21 pts), then cap to 12 centered on 15
    expect(got).toHaveLength(DEFAULT_TRAJECTORY_MAX_POINTS);
    expect(got.map((p) => p.x)).toEqual([10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21]);
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
  it('maps traffic-light bins including boundaries', () => {
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
  });
});

describe('defaults', () => {
  it('exports design defaults', () => {
    expect(DEFAULT_TRAJECTORY_WINDOW_MS).toBe(10 * 60_000);
    expect(DEFAULT_TRAJECTORY_MAX_POINTS).toBe(12);
  });
});
