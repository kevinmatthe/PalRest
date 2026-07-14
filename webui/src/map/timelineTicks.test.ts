import { describe, expect, it } from 'vitest';
import { mergeTimelineTicks, timelinePercent } from './timelineTicks';

describe('timelinePercent', () => {
  it('clamps to 0–100', () => {
    expect(timelinePercent('2026-07-14T10:00:00.000Z', Date.parse('2026-07-14T10:00:00.000Z'), Date.parse('2026-07-14T11:00:00.000Z'))).toBe(0);
    expect(timelinePercent('2026-07-14T11:00:00.000Z', Date.parse('2026-07-14T10:00:00.000Z'), Date.parse('2026-07-14T11:00:00.000Z'))).toBe(100);
    expect(timelinePercent('2026-07-14T12:00:00.000Z', Date.parse('2026-07-14T10:00:00.000Z'), Date.parse('2026-07-14T11:00:00.000Z'))).toBe(100);
  });
});

describe('mergeTimelineTicks', () => {
  const start = Date.parse('2026-07-14T10:00:00.000Z');
  const end = Date.parse('2026-07-14T11:00:00.000Z');

  it('merges many samples into at most trackWidth columns', () => {
    const items = Array.from({ length: 200 }, (_, i) => ({
      at: new Date(start + i * 1000).toISOString(),
      kind: 'trajectory' as const,
      key: `t-${i}`,
    }));
    const merged = mergeTimelineTicks(items, start, end, 100, 0);
    expect(merged.length).toBeLessThanOrEqual(100);
    expect(merged.length).toBeGreaterThan(0);
    expect(merged.reduce((n, t) => n + t.count, 0)).toBe(200);
  });

  it('prefers event kind when mixed in one column', () => {
    const at = '2026-07-14T10:30:00.000Z';
    const merged = mergeTimelineTicks(
      [
        { at, kind: 'trajectory', key: 'a' },
        { at, kind: 'event', key: 'b' },
      ],
      start,
      end,
      50,
      -1,
    );
    expect(merged).toHaveLength(1);
    expect(merged[0]!.kind).toBe('event');
    expect(merged[0]!.count).toBe(2);
  });

  it('marks a column active when the active index falls in it', () => {
    const items = [
      { at: '2026-07-14T10:00:00.000Z', kind: 'event' as const, key: 'e0' },
      { at: '2026-07-14T10:30:00.000Z', kind: 'trajectory' as const, key: 't1' },
      { at: '2026-07-14T11:00:00.000Z', kind: 'private' as const, key: 'p2' },
    ];
    const merged = mergeTimelineTicks(items, start, end, 100, 1);
    expect(merged.some((t) => t.active)).toBe(true);
    const active = merged.find((t) => t.active)!;
    expect(active.kind).toBe('trajectory');
  });
});
