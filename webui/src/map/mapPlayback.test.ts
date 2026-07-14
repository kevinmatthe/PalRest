import { describe, expect, it } from 'vitest';
import {
  BASE_PLAY_INTERVAL_MS,
  PLAY_SPEEDS,
  nextCursorIndex,
  playIntervalMs,
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
