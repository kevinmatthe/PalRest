// webui/src/behavior/behaviorMetrics.test.ts
import { describe, expect, it } from 'vitest';
import { edgeClassBetween, pickDominantClass, summarizeBehavior } from './behaviorMetrics';
import {
  D_IDLE,
  T_ACTIVE_CAP_MS,
  T_GAP_MS,
  V_IDLE,
  V_TRAVEL,
  type BehaviorPoint,
} from './behaviorTypes';

describe('behavior thresholds', () => {
  it('exports design defaults', () => {
    expect(D_IDLE).toBe(500);
    expect(V_IDLE).toBe(50);
    expect(V_TRAVEL).toBe(800);
    expect(T_GAP_MS).toBe(5 * 60_000);
    expect(T_ACTIVE_CAP_MS).toBe(T_GAP_MS);
  });
});

function pt(
  partial: Partial<BehaviorPoint> & Pick<BehaviorPoint, 'observed_at' | 'segment_id' | 'x' | 'y'>,
): BehaviorPoint {
  return { ping: 40, ...partial };
}

const t0 = Date.parse('2026-07-14T10:00:00.000Z');

describe('summarizeBehavior', () => {
  it('returns safe zeros for empty or single point', () => {
    expect(summarizeBehavior([], { windowStartMs: t0, windowEndMs: t0 + 3600_000 }).dominantClass).toBe(
      'unknown',
    );
    const one = summarizeBehavior(
      [pt({ observed_at: '2026-07-14T10:00:00.000Z', segment_id: 's1', x: 0, y: 0 })],
      { windowStartMs: t0, windowEndMs: t0 + 3600_000 },
    );
    expect(one.sampleCount).toBe(1);
    expect(one.pathLength).toBe(0);
    expect(one.dominantClass).toBe('unknown');
  });

  it('classifies stationary pair with negligible movement', () => {
    const samples = [
      pt({ observed_at: '2026-07-14T10:00:00.000Z', segment_id: 's1', x: 0, y: 0 }),
      pt({ observed_at: '2026-07-14T10:02:00.000Z', segment_id: 's1', x: 10, y: 0 }),
    ];
    const s = summarizeBehavior(samples, {
      windowStartMs: t0,
      windowEndMs: t0 + 10 * 60_000,
    });
    expect(s.classShare.stationary).toBeGreaterThan(0.99);
    expect(s.dominantClass).toBe('stationary');
    expect(s.pathLength).toBeLessThan(20);
    expect(s.peakSpeed).toBeLessThan(50);
  });

  it('classifies high-speed travel', () => {
    // 100_000 world units in 60s → ~1667 u/s >= V_travel 800
    const samples = [
      pt({ observed_at: '2026-07-14T10:00:00.000Z', segment_id: 's1', x: 0, y: 0 }),
      pt({ observed_at: '2026-07-14T10:01:00.000Z', segment_id: 's1', x: 100_000, y: 0 }),
    ];
    const s = summarizeBehavior(samples, { windowStartMs: t0, windowEndMs: t0 + 5 * 60_000 });
    expect(s.dominantClass).toBe('traveling');
    expect(s.pathLength).toBeCloseTo(100_000, 0);
    expect(s.meanSpeed).toBeCloseTo(100_000 / 60, 0);
    expect(s.peakSpeed).toBeCloseTo(100_000 / 60, 0);
  });

  it('classifies local mid-speed movement', () => {
    // 10_000 in 60s → ~167 u/s: above V_idle 50, below V_travel 800
    const samples = [
      pt({ observed_at: '2026-07-14T10:00:00.000Z', segment_id: 's1', x: 0, y: 0 }),
      pt({ observed_at: '2026-07-14T10:01:00.000Z', segment_id: 's1', x: 10_000, y: 0 }),
    ];
    const s = summarizeBehavior(samples, { windowStartMs: t0, windowEndMs: t0 + 5 * 60_000 });
    expect(s.dominantClass).toBe('local');
  });

  it('does not connect path across segments (gap)', () => {
    const samples = [
      pt({ observed_at: '2026-07-14T10:00:00.000Z', segment_id: 's1', x: 0, y: 0 }),
      pt({ observed_at: '2026-07-14T10:01:00.000Z', segment_id: 's2', x: 100_000, y: 0 }),
    ];
    const s = summarizeBehavior(samples, { windowStartMs: t0, windowEndMs: t0 + 5 * 60_000 });
    expect(s.pathLength).toBe(0);
    expect(s.gapMs).toBe(60_000);
    expect(s.observedActiveMs).toBe(0);
  });

  it('treats Δt above T_gap as gap and caps active contribution', () => {
    const samples = [
      pt({ observed_at: '2026-07-14T10:00:00.000Z', segment_id: 's1', x: 0, y: 0 }),
      pt({ observed_at: '2026-07-14T10:10:00.000Z', segment_id: 's1', x: 100, y: 0 }), // 10 min > 5 min gap
    ];
    const s = summarizeBehavior(samples, { windowStartMs: t0, windowEndMs: t0 + 20 * 60_000 });
    expect(s.gapMs).toBe(10 * 60_000);
    expect(s.observedActiveMs).toBe(0);
    expect(s.pathLength).toBe(0);
  });

  it('computes radius from centroid and density when active', () => {
    const samples = [
      pt({ observed_at: '2026-07-14T10:00:00.000Z', segment_id: 's1', x: 0, y: 0 }),
      pt({ observed_at: '2026-07-14T10:01:00.000Z', segment_id: 's1', x: 3000, y: 0 }),
      pt({ observed_at: '2026-07-14T10:02:00.000Z', segment_id: 's1', x: 0, y: 0 }),
    ];
    const s = summarizeBehavior(samples, { windowStartMs: t0, windowEndMs: t0 + 10 * 60_000 });
    expect(s.sampleCount).toBe(3);
    expect(s.radius).toBeGreaterThan(0);
    expect(s.sampleDensityPerHour).toBeGreaterThan(0);
    expect(s.segmentCount).toBe(1);
  });

  it('breaks dominant-class ties traveling > local > stationary', () => {
    expect(pickDominantClass({ stationary: 10, local: 10, traveling: 10 })).toBe('traveling');
    expect(pickDominantClass({ stationary: 10, local: 10, traveling: 0 })).toBe('local');
    expect(pickDominantClass({ stationary: 0, local: 0, traveling: 0 })).toBe('unknown');
  });

  it('classifies single edges for map overlay', () => {
    const a = pt({ observed_at: '2026-07-14T10:00:00.000Z', segment_id: 's1', x: 0, y: 0 });
    const travel = pt({ observed_at: '2026-07-14T10:01:00.000Z', segment_id: 's1', x: 100_000, y: 0 });
    const idle = pt({ observed_at: '2026-07-14T10:01:00.000Z', segment_id: 's1', x: 5, y: 0 });
    expect(edgeClassBetween(a, travel)).toBe('traveling');
    expect(edgeClassBetween(a, idle)).toBe('stationary');
  });
});
