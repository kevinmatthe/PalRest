import { describe, expect, it } from 'vitest';
import {
  DAY_MS,
  DEFAULT_HORIZON_MS,
  endFromScrubber,
  HOUR_MS,
  isPinnedToNow,
  matchPreset,
  scrubberPosition,
  windowFromEnd,
  windowTrackLeft,
  windowTrackWidth,
} from './timeWindow';

describe('timeWindow', () => {
  const now = Date.parse('2026-07-14T12:00:00Z');

  it('builds a fixed window ending at now', () => {
    const w = windowFromEnd(now, HOUR_MS, now);
    expect(w.endMs).toBe(now);
    expect(w.startMs).toBe(now - HOUR_MS);
    expect(w.windowMs).toBe(HOUR_MS);
  });

  it('clamps end so the window stays inside the horizon', () => {
    const tooOld = now - DEFAULT_HORIZON_MS - DAY_MS;
    const w = windowFromEnd(tooOld, DAY_MS, now);
    expect(w.endMs).toBe(now - DEFAULT_HORIZON_MS + DAY_MS);
    expect(w.startMs).toBe(now - DEFAULT_HORIZON_MS);
  });

  it('maps scrubber 0/1 to oldest / now', () => {
    expect(endFromScrubber(1, HOUR_MS, now)).toBe(now);
    const oldestEnd = endFromScrubber(0, HOUR_MS, now);
    expect(oldestEnd).toBe(now - DEFAULT_HORIZON_MS + HOUR_MS);
    expect(scrubberPosition(now, HOUR_MS, now)).toBeCloseTo(1);
    expect(scrubberPosition(oldestEnd, HOUR_MS, now)).toBeCloseTo(0);
  });

  it('keeps window width proportional on the track', () => {
    expect(windowTrackWidth(DAY_MS)).toBeCloseTo(DAY_MS / DEFAULT_HORIZON_MS);
    const start = now - DAY_MS;
    expect(windowTrackLeft(start, now)).toBeCloseTo((start - (now - DEFAULT_HORIZON_MS)) / DEFAULT_HORIZON_MS);
  });

  it('matches common presets with minute slack', () => {
    expect(matchPreset(HOUR_MS)?.id).toBe('1h');
    expect(matchPreset(HOUR_MS + 45_000)?.id).toBe('1h');
    expect(matchPreset(3 * HOUR_MS)).toBeNull();
  });

  it('detects pin-to-now', () => {
    expect(isPinnedToNow(now, now)).toBe(true);
    expect(isPinnedToNow(now - 30_000, now)).toBe(true);
    expect(isPinnedToNow(now - HOUR_MS, now)).toBe(false);
  });
});
