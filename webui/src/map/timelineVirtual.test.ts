import { describe, expect, it } from 'vitest';
import { scrollTopForIndex, virtualWindow } from './timelineVirtual';

describe('virtualWindow', () => {
  it('returns empty range for zero items', () => {
    expect(virtualWindow(0, 0, 400, 100, 2)).toEqual({ start: 0, end: 0, offsetTop: 0, totalHeight: 0 });
  });

  it('windows a long list around scrollTop', () => {
    const w = virtualWindow(500, 10_000, 400, 100, 2);
    // floor(10000/100)-2 = 98
    expect(w.start).toBe(98);
    expect(w.end).toBeLessThanOrEqual(500);
    expect(w.end - w.start).toBeLessThan(20);
    expect(w.totalHeight).toBe(50_000);
    expect(w.offsetTop).toBe(98 * 100);
  });

  it('clamps end to count near the bottom', () => {
    const w = virtualWindow(10, 900, 400, 100, 5);
    // floor(900/100)-5 = 4
    expect(w.start).toBe(4);
    expect(w.end).toBe(10);
  });
});

describe('scrollTopForIndex', () => {
  it('keeps early indices near zero', () => {
    expect(scrollTopForIndex(0, 400, 100)).toBe(0);
  });

  it('moves down for later indices', () => {
    expect(scrollTopForIndex(20, 400, 100)).toBeGreaterThan(scrollTopForIndex(5, 400, 100));
  });
});
