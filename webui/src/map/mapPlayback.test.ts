import { describe, expect, it } from 'vitest';
import {
  BASE_PLAY_INTERVAL_MS,
  PLAY_SPEEDS,
  TIME_PLAY_COMPRESS,
  TIME_PLAY_MAX_MS,
  TIME_PLAY_MIN_MS,
  nextCursorIndex,
  playIntervalMs,
  playStepDelayMs,
  prevCursorIndex,
  type PlaySpeed,
} from './mapPlayback';

describe('playIntervalMs', () => {
  it('maps speeds to 800/400/200', () => {
    expect(playIntervalMs(1)).toBe(800);
    expect(playIntervalMs(2)).toBe(400);
    expect(playIntervalMs(4)).toBe(200);
  });

  it('divides BASE_PLAY_INTERVAL_MS by speed', () => {
    for (const speed of PLAY_SPEEDS) {
      expect(playIntervalMs(speed)).toBe(BASE_PLAY_INTERVAL_MS / speed);
    }
  });
});

describe('PLAY_SPEEDS', () => {
  it('is 1, 2, 4', () => {
    expect(PLAY_SPEEDS).toEqual([1, 2, 4] satisfies PlaySpeed[]);
  });
});

describe('nextCursorIndex', () => {
  it('advances until end then done', () => {
    expect(nextCursorIndex(0, 4)).toEqual({ index: 1, done: false });
    expect(nextCursorIndex(1, 4)).toEqual({ index: 2, done: false });
    expect(nextCursorIndex(2, 4)).toEqual({ index: 3, done: true });
    expect(nextCursorIndex(3, 4)).toEqual({ index: 3, done: true });
  });

  it('length 0 stays at 0 and is done', () => {
    expect(nextCursorIndex(0, 0)).toEqual({ index: 0, done: true });
  });

  it('length 1 stays at 0 and is done', () => {
    expect(nextCursorIndex(0, 1)).toEqual({ index: 0, done: true });
  });
});

describe('prevCursorIndex', () => {
  it('steps backward until the first index', () => {
    expect(prevCursorIndex(3, 4)).toEqual({ index: 2, done: false });
    expect(prevCursorIndex(1, 4)).toEqual({ index: 0, done: false });
    expect(prevCursorIndex(0, 4)).toEqual({ index: 0, done: true });
  });

  it('length 0/1 stay done at 0', () => {
    expect(prevCursorIndex(0, 0)).toEqual({ index: 0, done: true });
    expect(prevCursorIndex(0, 1)).toEqual({ index: 0, done: true });
  });
});

describe('playStepDelayMs', () => {
  it('uses fixed intervals in index mode', () => {
    expect(playStepDelayMs('index', 1, '2026-07-14T10:00:00Z', '2026-07-14T11:00:00Z')).toBe(800);
    expect(playStepDelayMs('index', 2, 'a', 'b')).toBe(400);
  });

  it('compresses real deltas in time mode', () => {
    const hour = 60 * 60 * 1000;
    const delay = playStepDelayMs('time', 1, '2026-07-14T10:00:00.000Z', '2026-07-14T11:00:00.000Z');
    expect(delay).toBe(Math.min(TIME_PLAY_MAX_MS, Math.max(TIME_PLAY_MIN_MS, hour / TIME_PLAY_COMPRESS)));
  });

  it('falls back when timestamps are invalid', () => {
    expect(playStepDelayMs('time', 1, undefined, undefined)).toBe(800);
    expect(playStepDelayMs('time', 1, '2026-07-14T11:00:00Z', '2026-07-14T10:00:00Z')).toBe(800);
  });
});
